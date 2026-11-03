package discovery_test

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/grpcreflect"
	"github.com/Manu343726/hammer/discovery"
	"github.com/Manu343726/hammer/docs"
	"github.com/Manu343726/hammer/internal/testcontract"
	testcontractv1 "github.com/Manu343726/hammer/internal/testcontract/testcontractv1"
	testcontractv1connect "github.com/Manu343726/hammer/internal/testcontract/testcontractv1/testcontractv1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// The client's job is to read a contract it was never compiled against and then call it. The tests here
// are the ones that decide what a caller sees when that goes wrong: a schema that is refetched on every
// call, a streaming method attempted as unary, a request of the wrong type silently coerced, an endpoint
// that lists a service it cannot describe, and a client shared by several goroutines. Each of those
// either wastes the endpoint's time or, worse, dispatches a call nobody meant to make.

// covCountingTransport counts the HTTP requests that pass through it. It is how a test asserts that the
// reflection cache did its job — the alternative is asserting that a second answer *looks* cached, which
// passes whether or not a round trip happened.
type covCountingTransport struct {
	next http.RoundTripper

	mu    sync.Mutex
	count int
}

func (transport *covCountingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.mu.Lock()
	transport.count++
	transport.mu.Unlock()
	return transport.next.RoundTrip(request)
}

func (transport *covCountingTransport) Count() int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.count
}

// covH2CTransport is the transport the client builds for a plain-HTTP endpoint: reflection is a
// bidirectional stream over HTTP/2 with prior knowledge, so a counting transport has to carry the same
// protocol the default one does or the count would be of failed connections.
func covH2CTransport() http.RoundTripper {
	return &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, address string, _ *tls.Config) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		},
	}
}

// covContractEndpoint compiles the given contract source, serves one of its services on a private
// endpoint together with reflection for it, and returns a client pointed at it.
//
// The descriptors are the real ones — compiled from .proto text by the same path a package's own contract
// takes — but the reflection resolver is private to this endpoint, so a test can serve a shape the checked-in
// fixture does not have (an empty service, a documentation service narrower than the client expects) without
// putting that shape into the process-wide registry where the next test would meet it. build receives the
// compiled contract so a handler can answer with the messages the contract declares rather than with Go
// types the test made up.
func covContractEndpoint(
	t *testing.T, source, service string, build func(protoreflect.FileDescriptor) http.Handler,
) *discovery.Client {
	t.Helper()
	path := "cov/contracts/" + strings.ReplaceAll(t.Name(), "/", "_") + ".proto"
	require.NoError(t, docs.RegisterProtoSource(path, []byte(source)))
	compiled, err := docs.CompileProtoSource(path)
	require.NoError(t, err)

	files, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(compiled)},
	})
	require.NoError(t, err)
	reflector := grpcreflect.NewReflector(
		grpcreflect.NamerFunc(func() []string { return []string{service} }),
		grpcreflect.WithDescriptorResolver(files),
	)

	mux := http.NewServeMux()
	mux.Handle("/"+service+"/", build(compiled))
	mux.Handle(grpcreflect.NewHandlerV1(reflector))
	mux.Handle(grpcreflect.NewHandlerV1Alpha(reflector))

	server := httptest.NewUnstartedServer(h2c.NewHandler(mux, &http2.Server{}))
	server.Start()
	t.Cleanup(server.Close)
	return discovery.New(server.URL)
}

// A schema is fetched over reflection, which is a round trip per call, so it is cached. A cache that did
// not hold would make every invocation pay for a description the process already has; a cache that was
// never consulted is invisible until an endpoint is under load, which is when it matters.
func TestACachedSchemaCostsOneReflectionRoundTrip(t *testing.T) {
	endpoint, _, _ := serve(t)
	reflection := &covCountingTransport{next: covH2CTransport()}
	client := discovery.New(endpoint, discovery.WithReflectionClient(&http.Client{Transport: reflection}))

	first, err := client.DescribeService(context.Background(), invokerService)
	require.NoError(t, err)
	require.Equal(t, 1, reflection.Count(), "the first description should be one reflection round trip")

	second, err := client.DescribeService(context.Background(), invokerService)
	require.NoError(t, err)

	assert.Equal(t, 1, reflection.Count(), "the second description went back to the endpoint instead of using the cache")
	assert.Equal(t, first.Name, second.Name)
	require.NotEmpty(t, second.Methods)
}

// The options exist so a caller can supply its own transports. Supplying none is legal and has to change
// nothing: it is what a caller that builds the client from configuration passes when the configuration
// says nothing. The non-nil cases are asserted through a transport that sees the traffic, because an
// option that is stored and never used is a transport a caller believes it installed.
func TestTheClientOptionsAreAppliedOrHarmless(t *testing.T) {
	endpoint, _, fixture := serve(t)

	connectTransport := &covCountingTransport{next: http.DefaultTransport}
	interceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("X-Coverage", "interceptor")
			return next(ctx, request)
		}
	})
	client := discovery.New(endpoint,
		discovery.WithHTTPClient(&http.Client{Transport: connectTransport}),
		discovery.WithReflectionClient(nil),
		discovery.WithConnectOptions(connect.WithInterceptors(interceptor)),
	)

	_, err := client.Invoke(context.Background(), invokerService, invokeMethod,
		&testcontractv1.InvokeApiRequest{OperationId: "api/getThing"})
	require.NoError(t, err)

	// The Connect call went through the caller's HTTP client...
	assert.GreaterOrEqual(t, connectTransport.Count(), 1, "the supplied HTTP client was not used for the call")
	// ...and through the caller's interceptor, which is what WithConnectOptions adds.
	assert.Equal(t, "interceptor", fixture.seen.Get("X-Coverage"),
		"the connect option was stored but never applied to the call")

	// No options at all is a client that has to work exactly like the default one.
	_, err = discovery.New(endpoint).Invoke(context.Background(), invokerService, invokeMethod,
		&testcontractv1.InvokeApiRequest{OperationId: "api/getThing"})
	require.NoError(t, err)

	// An explicit nil is the same as not supplying one: a caller building the option list from
	// configuration hands nil when it has nothing to add.
	_, err = discovery.New(endpoint, discovery.WithHTTPClient(nil),
		discovery.WithReflectionClient(nil), discovery.WithConnectOptions()).
		Invoke(context.Background(), invokerService, invokeMethod,
			&testcontractv1.InvokeApiRequest{OperationId: "api/getThing"})
	require.NoError(t, err)
}

// Discover is the call a tool makes when it has been pointed at an endpoint and knows nothing else, so it
// has to describe everything the endpoint lists — including the reflection service itself, which is
// listed and documented like any other.
func TestDiscoverDescribesEveryServiceTheEndpointLists(t *testing.T) {
	_, client, _ := serve(t)

	schemas, err := client.Discover(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, schemas)

	names := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		names = append(names, schema.Name)
		// A schema without methods is a service a caller cannot call, and it would be reported as an
		// empty one rather than as a failure to describe it.
		assert.NotEmpty(t, schema.Methods, "service %q was described with no methods", schema.Name)
	}
	assert.Contains(t, names, invokerService)
}

// An endpoint can list a service it cannot resolve — a deployment where a generated contract was dropped
// or a reflection implementation that advertises more than it serves. Discover has to report which name
// failed, because the caller's next step is to fix that service and not the endpoint.
func TestDiscoverReportsAServiceItCannotDescribe(t *testing.T) {
	const vanished = "cov.v1.Vanished"
	server, err := testcontract.Serve(testcontract.Mount{
		Name:    vanished,
		Path:    "/cov/vanished/",
		Handler: http.NotFoundHandler(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })

	_, err = discovery.New(server.Endpoint()).Discover(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), vanished, "the error must name the service that could not be described")
}

// A name has to be given: describing the empty name is a caller that derived it from something empty, and
// the answer must be that, not a reflection request for "" whose error would blame the endpoint.
func TestDescribingNothingIsRefusedWithoutAskingTheEndpoint(t *testing.T) {
	// A closed port, so any attempt to ask would fail differently — and slowly.
	client := discovery.New("http://127.0.0.1:1")

	for _, name := range []string{"", "   ", "\t\n"} {
		_, err := client.DescribeService(context.Background(), name)
		require.Error(t, err, "%q was accepted as a service name", name)
		assert.Contains(t, err.Error(), "service name is required")
	}
}

// A streaming method cannot be invoked by a unary caller, and the refusal has to come from the schema
// rather than from the transport: calling it anyway would open a stream the caller has no way to read and
// hang until the context expired. The streaming method used here is the endpoint's own reflection service,
// which is bidirectional streaming, so the test needs no contract of its own.
func TestAStreamingMethodIsRefusedRatherThanCalledAsUnary(t *testing.T) {
	_, client, _ := serve(t)
	const reflectionService = "grpc.reflection.v1.ServerReflection"

	schema, err := client.DescribeService(context.Background(), reflectionService)
	require.NoError(t, err)
	require.Len(t, schema.Methods, 1)
	require.True(t, schema.Methods[0].ServerStreaming && schema.Methods[0].ClientStreaming,
		"the fixture's reflection service should be bidirectional streaming")

	_, err = client.Invoke(context.Background(), reflectionService, "ServerReflectionInfo", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "streaming", "a streaming method was not reported as streaming when invoked")

	_, err = client.InvokeJSON(context.Background(), reflectionService, "ServerReflectionInfo", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "streaming", "a streaming method was not reported as streaming on the JSON path")
}

// A request of the wrong message type is refused before anything reaches the wire. Coercing it would
// encode the fields it happens to share and silently drop the rest, so a caller would get an answer to a
// request it never made — the worst possible outcome for a dynamic caller, which has no generated type to
// compare against.
func TestARequestOfTheWrongTypeIsRefused(t *testing.T) {
	_, client, fixture := serve(t)

	_, err := client.Invoke(context.Background(), invokerService, invokeMethod,
		&testcontractv1.ParseApiRequest{BaseUrl: "http://elsewhere"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
	assert.Empty(t, fixture.seen, "a request of the wrong type reached the endpoint")
}

// A request that cannot be encoded is refused as an encoding failure, not as a call failure: the endpoint
// never sees it, and telling the caller the RPC failed would send them looking at the server. The value
// used is a proto3 string that is not valid UTF-8, which is the one way a message that type-checks still
// cannot be put on the wire.
func TestARequestThatCannotBeEncodedIsRefused(t *testing.T) {
	_, client, fixture := serve(t)

	_, err := client.Invoke(context.Background(), invokerService, invokeMethod,
		&testcontractv1.InvokeApiRequest{OperationId: "\xff\xfe"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "encode request")
	assert.Empty(t, fixture.seen, "a request that could not be encoded reached the endpoint")
}

// A message with the contract's full name and an incompatible field type encodes but cannot be decoded
// into the reflected type. This is what a caller gets for passing a message from a contract that drifted
// from the endpoint's — the bytes are well formed and mean something else — and the alternative to an
// error is a dynamic request with silently dropped fields.
func TestARequestThatCannotBeDecodedIntoTheReflectedTypeIsRefused(t *testing.T) {
	const path = "cov/drifted-request.proto"
	require.NoError(t, docs.RegisterProtoSource(path, []byte(`syntax = "proto3";

package testcontract.v1;

// InvokeApiRequest is a contract that drifted: the endpoint declares operation_id as
// a string, this one as bytes, which encodes happily and cannot be decoded into the
// reflected message because the bytes are not valid UTF-8.
message InvokeApiRequest {
  bytes operation_id = 1;
}
`)))
	compiled, err := docs.CompileProtoSource(path)
	require.NoError(t, err)
	descriptor := compiled.Messages().ByName("InvokeApiRequest")
	drifted := dynamicpb.NewMessage(descriptor)
	drifted.Set(descriptor.Fields().ByName("operation_id"), protoreflect.ValueOfBytes([]byte{0xff, 0xfe}))

	_, client, fixture := serve(t)
	_, err = client.Invoke(context.Background(), invokerService, invokeMethod, drifted)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode request")
	assert.Empty(t, fixture.seen, "a request that could not be decoded reached the endpoint")
}

// A service with no methods is a contract that declares nothing yet. It has to describe as empty rather
// than as a failure, because a host listing what an endpoint offers needs to tell "this service has no
// operations" from "this service could not be read".
func TestAServiceWithNoMethodsIsDescribedAsEmpty(t *testing.T) {
	client := covContractEndpoint(t, `syntax = "proto3";

package cov.empty.v1;

// EmptyService declares nothing yet.
service EmptyService {}
`, "cov.empty.v1.EmptyService", func(protoreflect.FileDescriptor) http.Handler {
		return http.NotFoundHandler()
	})

	schema, err := client.DescribeService(context.Background(), "cov.empty.v1.EmptyService")

	require.NoError(t, err)
	assert.Equal(t, "cov.empty.v1.EmptyService", schema.Name)
	assert.Empty(t, schema.Methods)
	require.NotNil(t, schema.Documentation)
	assert.Equal(t, "EmptyService declares nothing yet.", schema.Documentation.Description,
		"the description of a service with no methods still comes from its contract")
	assert.Empty(t, schema.Documentation.Methods)
}

// Reflection is a stream that stays open for as long as the client holds it, so a cancelled context has to
// end it. A client that ignored cancellation would hold a stream per abandoned call, and a gateway that
// cancelled a request would leak one for every client that disconnected early.
func TestACancelledContextStopsDescribing(t *testing.T) {
	_, client, _ := serve(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.DescribeService(ctx, "testcontract.v1.ApiParserService")

	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "cancel",
		"a cancelled context was reported as something else, so the caller cannot tell it was their own doing")
}

// covFailingEndpoint serves the invoker's name without answering its method, which is the shape of a
// deployment whose handler is broken rather than absent: reflection describes the contract and the call
// fails.
func covFailingEndpoint(t *testing.T) *discovery.Client {
	t.Helper()
	server, err := testcontract.Serve(testcontract.Mount{
		Name: invokerService,
		Path: "/" + invokerService + "/",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })
	return discovery.New(server.Endpoint())
}

// A call the endpoint rejects is reported as an invocation failure that names the method, because a caller
// that cannot tell "the endpoint refused this" from "the contract is wrong" has nowhere to go next.
func TestACallThatFailsIsReportedAgainstTheMethod(t *testing.T) {
	client := covFailingEndpoint(t)

	_, err := client.Invoke(context.Background(), invokerService, invokeMethod,
		&testcontractv1.InvokeApiRequest{OperationId: "api/getThing"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), invokerService+"."+invokeMethod)
}

// The JSON path reports its own failures rather than falling through: a method the schema does not have, a
// body that is not JSON, and a call the endpoint rejects are three different mistakes with three different
// fixes, and a caller handed a generic error would have to guess which one it made.
func TestTheJSONPathReportsWhatWentWrong(t *testing.T) {
	_, client, _ := serve(t)

	_, err := client.InvokeJSON(context.Background(), "cov.v1.NoSuchService", invokeMethod, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cov.v1.NoSuchService")

	_, err = client.InvokeJSON(context.Background(), invokerService, "NoSuchMethod", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	_, err = client.InvokeJSON(context.Background(), invokerService, invokeMethod, []byte(`{"operationId":`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode request JSON")

	failing := covFailingEndpoint(t)
	_, err = failing.InvokeJSON(context.Background(), invokerService, invokeMethod,
		[]byte(`{"operationId":"api/getThing"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invoke")
}

// covStatelessInvoker answers every call without recording anything, so a concurrent test exercises the
// client's cache and transports rather than a fixture that keeps the last request it saw for another test
// to assert on.
type covStatelessInvoker struct{}

// InvokeApi implements the reflected service's method.
func (covStatelessInvoker) InvokeApi(
	_ context.Context, _ *connect.Request[testcontractv1.InvokeApiRequest],
) (*connect.Response[testcontractv1.InvokeApiResponse], error) {
	return connect.NewResponse(&testcontractv1.InvokeApiResponse{Status: 200}), nil
}

// One client serves a gateway, so it is used by many goroutines at once: describing while invoking, over
// the same cache and the same transports. Under -race this is where a map read that raced a write shows up,
// and the failure it causes in production is a corrupted cache rather than a slow one.
func TestAReusedClientIsSafeUnderConcurrentUse(t *testing.T) {
	path, handler := testcontractv1connect.NewApiInvokerServiceHandler(covStatelessInvoker{})
	server, err := testcontract.Serve(testcontract.Mount{Name: invokerService, Path: path, Handler: handler})
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })
	client := discovery.New(server.Endpoint())
	ctx := context.Background()

	const workers = 6
	var wait sync.WaitGroup
	errors := make(chan error, workers*3)
	for i := 0; i < workers; i++ {
		wait.Add(3)
		go func() {
			defer wait.Done()
			_, err := client.DescribeService(ctx, invokerService)
			errors <- err
		}()
		go func() {
			defer wait.Done()
			_, err := client.Invoke(ctx, invokerService, invokeMethod,
				&testcontractv1.InvokeApiRequest{OperationId: "api/getThing"})
			errors <- err
		}()
		go func() {
			defer wait.Done()
			_, err := client.InvokeJSON(ctx, invokerService, invokeMethod, []byte(`{"operationId":"api/getThing"}`))
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)

	for err := range errors {
		require.NoError(t, err)
	}

	// And the cache still answers after all of that, which is the property a caller depends on rather than
	// the absence of a crash.
	schema, describeErr := client.DescribeService(ctx, invokerService)
	require.NoError(t, describeErr)
	assert.Equal(t, invokerService, schema.Name)
}

// An endpoint that does not answer reflection at all is reported by Discover as the listing failure it is,
// rather than as a discovery that found no services: "this endpoint does not reflect" and "this endpoint
// serves nothing" are different deployments and different fixes.
func TestDiscoverReportsAnEndpointThatDoesNotReflect(t *testing.T) {
	_, err := discovery.New("http://127.0.0.1:1").Discover(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "list services")
}

// A name that exists in the contract but is not a service is refused where it is found. A caller that asked
// for a message — a typo, or a name derived from a schema — must be told what is wrong rather than handed a
// schema that cannot be called.
func TestDescribingASymbolThatIsNotAServiceIsRefused(t *testing.T) {
	_, client, _ := serve(t)

	_, err := client.DescribeService(context.Background(), "testcontract.v1.InvokeApiRequest")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a service")
}

// A schema is cached, and a cache is shared, so one client serves a gateway. This asserts the answer a
// caller gets from the cache is the same as the one it got from the endpoint — the case a stale or partial
// cache would break in a way that looks like the endpoint answering differently.
func TestASecondLookupAnswersTheSameSchema(t *testing.T) {
	_, client, _ := serve(t)

	first, err := client.DescribeService(context.Background(), invokerService)
	require.NoError(t, err)
	second, err := client.DescribeService(context.Background(), invokerService)
	require.NoError(t, err)

	assert.Equal(t, first.Name, second.Name)
	assert.Equal(t, len(first.Methods), len(second.Methods))
	require.NotNil(t, second.Documentation)
	assert.Equal(t, first.Documentation.Name, second.Documentation.Name)
}

// A response initializer is what makes a dynamic call possible: it is how the reflected output type is
// installed in a client that was constructed before the answer existed. This asserts the shape a caller
// receives, since a response built from the caller's own type instead would make every call a silent
// no-op.
func TestAnInvocationAnswersWithTheReflectedOutputType(t *testing.T) {
	_, client, _ := serve(t)

	answer, err := client.Invoke(context.Background(), invokerService, invokeMethod,
		&testcontractv1.InvokeApiRequest{OperationId: "api/getThing"})
	require.NoError(t, err)

	assert.Equal(t, "testcontract.v1.InvokeApiResponse",
		string(answer.ProtoReflect().Descriptor().FullName()),
		"the answer is not of the type the contract declares")
}
