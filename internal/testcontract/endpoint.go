// Package testcontract is the contract this module's own tests describe and call.
//
// The library itself has no .proto contract, and does not need one: it reads contracts rather
// than being described by one. Testing a reader nevertheless needs a contract it has never seen
// written in code — one that arrives over reflection, with the documentation and annotations its
// source declared — so the fixture lives here, out of the library's way, with its generated Go
// checked in.
//
// The endpoint helper is here for the same reason. Reflection is a bidirectional stream, so every
// test that describes a service needs a transport, and a test that taught each of them to serve
// reflection would be teaching the same lesson three times with three chances to get it wrong.
package testcontract

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"connectrpc.com/grpcreflect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// Mount binds one service's generated Connect handler to the name it declares.
//
// The name is the fully-qualified protobuf name rather than something derivable from the path,
// because it is what reflection publishes: a fixture that let the two drift would serve a service
// nothing could describe.
type Mount struct {
	// Name is the fully-qualified protobuf service name.
	Name string
	// Path is the handler path the generated constructor returned.
	Path string
	// Handler answers the service's calls.
	Handler http.Handler
}

// Server is a running fixture endpoint.
type Server struct {
	http     *http.Server
	listener net.Listener
	endpoint string
}

// Serve starts an endpoint that serves the mounts over h2c, with gRPC reflection for their
// services, and returns it.
//
// A real listener rather than an in-process handler, because the reflection client speaks HTTP/2
// with prior knowledge and the connect client does not, and a fake that answered both would not
// exercise the transport either of them actually uses. The address is on the loopback interface
// and on a port the kernel picks, so concurrent tests do not meet each other.
func Serve(mounts ...Mount) (*Server, error) {
	if len(mounts) == 0 {
		return nil, fmt.Errorf("a fixture endpoint needs at least one service")
	}
	mux := http.NewServeMux()
	names := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		if strings.TrimSpace(mount.Name) == "" {
			return nil, fmt.Errorf("a fixture service needs its protobuf name")
		}
		if mount.Path == "" || mount.Handler == nil {
			return nil, fmt.Errorf("fixture service %q needs a path and a handler", mount.Name)
		}
		mux.Handle(mount.Path, mount.Handler)
		names = append(names, mount.Name)
	}
	reflector := grpcreflect.NewStaticReflector(names...)
	mux.Handle(grpcreflect.NewHandlerV1(reflector))
	mux.Handle(grpcreflect.NewHandlerV1Alpha(reflector))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for the fixture: %w", err)
	}
	server := &Server{
		http:     &http.Server{Handler: h2c.NewHandler(mux, &http2.Server{})},
		listener: listener,
		endpoint: "http://" + listener.Addr().String(),
	}
	go func() { _ = server.http.Serve(listener) }()
	return server, nil
}

// Endpoint returns the address to reach the fixture at.
func (s *Server) Endpoint() string {
	if s == nil {
		return ""
	}
	return s.endpoint
}

// Close stops the endpoint and every connection it holds.
//
// Close rather than a graceful shutdown: a reflection stream stays open for as long as the client
// keeps it, so waiting for in-flight requests to finish would wait for a test that has already
// ended.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	return s.http.Close()
}
