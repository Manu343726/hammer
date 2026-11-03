package protocontract_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/discovery"
	"github.com/Manu343726/hammer/internal/testcontract"
	testcontractv1 "github.com/Manu343726/hammer/internal/testcontract/testcontractv1"
	testcontractv1connect "github.com/Manu343726/hammer/internal/testcontract/testcontractv1/testcontractv1connect"
	"github.com/Manu343726/hammer/protocontract"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// The invoker resolves an operation through the endpoint's own reflection and then
// performs a dynamic unary call. Its promises are about classification: a caller
// decides what to retry and whom to page from the kind of error it gets, so a
// transport failure and a description failure must not arrive as the same thing.

// covOperation describes the fixture's ParseApi operation, as a description read
// from a live endpoint names it.
func covOperation(t *testing.T, endpoint, service, method string) (api.API, api.Operation) {
	t.Helper()
	described, _, err := protocontract.NewDescriptor(protocontract.Descriptor{}).FromEndpoint(t.Context(), endpoint)
	require.NoError(t, err)
	operation, ok := described.Operation(api.OperationID(described.ID, service, method))
	require.True(t, ok, "%s/%s was not described", service, method)
	return described, operation
}

func TestCoverInvokeCopiesTheClientsItWasGiven(t *testing.T) {
	// A caller that shares one discovery client per endpoint hands a map in, and the
	// invoker must copy it: aliasing would let the caller take away a client under a
	// call already in flight.
	endpoint := covServe(t, covAnsweringParserMount())
	given := map[string]*discovery.Client{endpoint: discovery.New(endpoint)}
	invoker := protocontract.NewInvoker(protocontract.InvokerOptions{Clients: given})

	delete(given, endpoint)
	described, operation := covOperation(t, endpoint, "testcontract.v1.ApiParserService", "ParseApi")
	result, err := invoker.Invoke(t.Context(), api.Call{
		Server:    api.Server{ID: "fixture", BaseURL: endpoint},
		API:       described,
		Operation: operation,
		Arguments: json.RawMessage(`{}`),
	})
	require.NoError(t, err, "the constructor copied the map, so the caller's later edit is invisible")
	assert.Equal(t, 200, result.Status)
}

func TestCoverInvokeReportsARequestThatDoesNotMatchTheMethod(t *testing.T) {
	// The arguments are the caller's, and a value of the wrong type is a bad
	// request: reporting it as a failed call would send the caller looking at a
	// server that was never asked anything.
	endpoint := covServe(t, covParserMount())
	described, operation := covOperation(t, endpoint, "testcontract.v1.ApiParserService", "ParseApi")

	_, err := protocontract.NewInvoker(protocontract.InvokerOptions{}).Invoke(t.Context(), api.Call{
		Server:    api.Server{ID: "fixture", BaseURL: endpoint},
		API:       described,
		Operation: operation,
		Arguments: json.RawMessage(`{"base_url": 42}`),
	})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "decode the request for")
}

func TestCoverInvokeReportsAFailedCallAsUnavailable(t *testing.T) {
	// A method that exists in the contract and fails when called is a reachability
	// problem: the description was right, the call was not answered.
	stub := &covFailingParser{}
	path, handler := testcontractv1connect.NewApiParserServiceHandler(stub)
	server, err := testcontract.Serve(testcontract.Mount{
		Name:    testcontractv1connect.ApiParserServiceName,
		Path:    path,
		Handler: handler,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })

	described, operation := covOperation(t, server.Endpoint(), "testcontract.v1.ApiParserService", "ParseApi")
	_, err = protocontract.NewInvoker(protocontract.InvokerOptions{}).Invoke(t.Context(), api.Call{
		Server:    api.Server{ID: "fixture", BaseURL: server.Endpoint()},
		API:       described,
		Operation: operation,
		Arguments: json.RawMessage(`{"format":"grpc"}`),
	})
	require.Error(t, err)
	assert.Equal(t, api.KindUnavailable, api.KindOf(err))
	assert.Contains(t, err.Error(), "call ")
}

func TestCoverInvokeClassifiesACallItCouldNotStart(t *testing.T) {
	// An endpoint that was never asked anything because the caller gave up is
	// unavailable, not describing something wrong: a caller that retries needs to
	// know the request may never have left.
	endpoint := covServe(t, covParserMount())
	described, operation := covOperation(t, endpoint, "testcontract.v1.ApiParserService", "ParseApi")

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := protocontract.NewInvoker(protocontract.InvokerOptions{}).Invoke(cancelled, api.Call{
		Server:    api.Server{ID: "fixture", BaseURL: endpoint},
		API:       described,
		Operation: operation,
		Arguments: json.RawMessage(`{"format":"grpc"}`),
	})
	require.Error(t, err)
	assert.Equal(t, api.KindUnavailable, api.KindOf(err))
}

func TestCoverInvokeReportsAServiceTheEndpointDoesNotDescribe(t *testing.T) {
	// The endpoint answered and does not know the service, which is a description
	// problem and not an outage: the fix is the catalog entry, not the network.
	endpoint := covServe(t, covParserMount())
	_, err := protocontract.NewInvoker(protocontract.InvokerOptions{}).Invoke(t.Context(), api.Call{
		Server: api.Server{ID: "fixture", BaseURL: endpoint},
		Operation: api.Operation{
			ID: "cov.ghost/ghost", Service: "cov.ghost.v1.GhostService", Name: "Haunt",
		},
	})
	require.Error(t, err)
	assert.Equal(t, api.KindFailedPrecondition, api.KindOf(err),
		"an endpoint that answers but does not describe the service is not unreachable")
}

func TestCoverInvokeTreatsANullBodyAsNoRequest(t *testing.T) {
	// A description with a named body lets a caller wrap the request, and a caller
	// that wrapped nothing said nothing: an empty message is what a method with no
	// required input takes.
	payload, err := protocontract.RequestPayload(json.RawMessage(`{"body":null}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(payload))

	payload, err = protocontract.RequestPayload(json.RawMessage("  "))
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(payload))

	payload, err = protocontract.RequestPayload(json.RawMessage(`null`))
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(payload))
}

func TestCoverInvokeReportsAResponseItCannotEncode(t *testing.T) {
	// The answer is re-encoded as protobuf JSON for the caller. A value that cannot
	// be encoded is an internal problem — the contract described a message the
	// encoder cannot write — and reporting it as a bad request would blame the
	// caller for the server's shape.
	//
	// The trigger is a google.protobuf.Any no registry knows: the message arrives
	// over the wire, and it is the JSON encoding that cannot resolve its type. The
	// service is declared here rather than in the fixture because the fixture is a
	// real contract and deliberately carries no well-known types.
	file := covAnyContract(t)
	method := file.Services().Get(0).Methods().Get(0)
	reply := dynamicpb.NewMessage(method.Output())
	detail := reply.Mutable(reply.Descriptor().Fields().ByName("detail")).Message()
	detail.Set(detail.Descriptor().Fields().ByName("type_url"),
		protoreflect.ValueOfString("type.googleapis.com/cov.marshal.v1.Unknown"))
	body, err := proto.Marshal(reply)
	require.NoError(t, err)

	endpoint := covServe(t, testcontract.Mount{
		Name: "cov.marshal.v1.MarshalService",
		Path: "/cov.marshal.v1.MarshalService/",
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/proto")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write(body)
		}),
	})

	described, warnings, err := protocontract.NewDescriptor(protocontract.Descriptor{APIID: "marshal"}).
		FromEndpoint(t.Context(), endpoint)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	operation, ok := described.Operation("marshal/cov.marshal.v1.MarshalService/Get")
	require.True(t, ok, "the dynamic contract was described over its own reflection")

	_, err = protocontract.NewInvoker(protocontract.InvokerOptions{}).Invoke(t.Context(), api.Call{
		Server:    api.Server{ID: "fixture", BaseURL: endpoint},
		API:       described,
		Operation: operation,
		Arguments: json.RawMessage(`{}`),
	})
	require.Error(t, err)
	assert.Equal(t, api.KindInternal, api.KindOf(err))
	assert.Contains(t, err.Error(), "encode the response for")
}

// covAnyContract registers one contract that carries a google.protobuf.Any and
// answers with its descriptor.
//
// Registration is process-wide because that is what reflection reads, and it is
// done once so a repeated run of the suite in one process finds the file already
// there rather than failing to declare it twice.
func covAnyContract(t *testing.T) protoreflect.FileDescriptor {
	t.Helper()
	const path = "cov/marshal/v1/marshal.proto"
	if existing, err := protoregistry.GlobalFiles.FindFileByPath(path); err == nil {
		return existing
	}

	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:       proto.String(path),
		Package:    proto.String("cov.marshal.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/any.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Reply"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("detail"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".google.protobuf.Any"),
			}},
		}},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("MarshalService"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("Get"),
				InputType:  proto.String(".cov.marshal.v1.Reply"),
				OutputType: proto.String(".cov.marshal.v1.Reply"),
			}},
		}},
	}, protoregistry.GlobalFiles)
	require.NoError(t, err)
	require.NoError(t, protoregistry.GlobalFiles.RegisterFile(file))
	return file
}

// covFailingParser is a fixture service whose method is described but never
// answers, which is the shape of a broken deployment: reflection works, the call
// does not.
type covFailingParser struct {
	testcontractv1connect.UnimplementedApiParserServiceHandler
}

// covAnsweringParser is the same contract served by something that answers, so a
// test about how the invoker carries its clients is not decided by a failed call.
type covAnsweringParser struct {
	testcontractv1connect.UnimplementedApiParserServiceHandler
}

func (p *covAnsweringParser) ParseApi(
	_ context.Context,
	_ *connect.Request[testcontractv1.ParseApiRequest],
) (*connect.Response[testcontractv1.ParseApiResponse], error) {
	return connect.NewResponse(&testcontractv1.ParseApiResponse{
		Api: &testcontractv1.Api{Id: "answered", Name: "answered", Format: "grpc"},
	}), nil
}

// covAnsweringParserMount serves the parser contract with an implementation.
func covAnsweringParserMount() testcontract.Mount {
	path, handler := testcontractv1connect.NewApiParserServiceHandler(&covAnsweringParser{})
	return testcontract.Mount{Name: testcontractv1connect.ApiParserServiceName, Path: path, Handler: handler}
}
