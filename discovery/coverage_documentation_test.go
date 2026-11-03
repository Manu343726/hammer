package discovery_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/Manu343726/hammer/discovery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// GetDocumentation is the fallback a reader uses for a contract this process has no source for: it asks
// the endpoint's documentation service and turns its answer back into the model. What is tested here is
// that the answer is read correctly and, more importantly, what a caller is told when the endpoint serves
// a documentation service that is missing, misnamed or empty — every one of which otherwise arrives as a
// documentation with nothing in it, which a help page renders as a contract nobody documented.
//
// The documentation service is not part of the fixture in internal/testcontract: the fixture is the
// contract the client *calls*, and this is a contract an endpoint serves about itself. So each test here
// writes the document service's contract as a source, compiles it, and serves its reflection from a
// private descriptor set. That keeps the tests off the process-wide registry — two tests need two different
// shapes of the same service name — and it keeps the descriptors the real ones, compiled from .proto text
// by the same path a package's own contract takes.

const covDocumentationService = "toolbox.documentation.v1.DocumentationService"

// covDocumentationContract is the document service as the client expects to find it: the request names a
// service, the response carries the model, and the model holds methods that hold parameters that hold
// further parameters.
const covDocumentationContract = `syntax = "proto3";

package toolbox.documentation.v1;

message Documentation {
  string name = 1;
  string description = 2;
  repeated Method methods = 3;
}

message Method {
  string name = 1;
  string description = 2;
  string input_type = 3;
  string output_type = 4;
  bool client_streaming = 5;
  bool server_streaming = 6;
  repeated Parameter parameters = 7;
}

message Parameter {
  string name = 1;
  string description = 2;
  string type_name = 3;
  string default_value = 4;
  bool repeated = 5;
  bool required = 6;
  repeated Parameter fields = 7;
}

message GetDocumentationRequest {
  string service_name = 1;
}

message GetDocumentationResponse {
  Documentation documentation = 1;
}

service DocumentationService {
  rpc GetDocumentation(GetDocumentationRequest) returns (GetDocumentationResponse);
}
`

// covDocumentationEndpoint serves one documentation contract on a private endpoint, built by
// covContractEndpoint with the one service name the client looks for.
func covDocumentationEndpoint(t *testing.T, source string, build func(protoreflect.FileDescriptor) http.Handler) *discovery.Client {
	t.Helper()
	return covContractEndpoint(t, source, covDocumentationService, build)
}

// covMessageInitializer installs a message type on a handler that was written before the contract existed,
// which is what lets one handler answer for every shape of documentation service these tests serve.
func covMessageInitializer(descriptor protoreflect.MessageDescriptor) func(connect.Spec, any) error {
	return func(_ connect.Spec, message any) error {
		target, ok := message.(*dynamicpb.Message)
		if !ok {
			return fmt.Errorf("the handler was given %T instead of a dynamic message", message)
		}
		*target = *dynamicpb.NewMessage(descriptor)
		return nil
	}
}

// covDocumentationHandler answers GetDocumentation from answer, over the contract's own request and
// response messages.
func covDocumentationHandler(
	t *testing.T, file protoreflect.FileDescriptor,
	answer func(*dynamicpb.Message) (*dynamicpb.Message, error),
) http.Handler {
	t.Helper()
	request := file.Messages().ByName("GetDocumentationRequest")
	response := file.Messages().ByName("GetDocumentationResponse")
	require.NotNil(t, request)
	require.NotNil(t, response)

	return connect.NewUnaryHandler[dynamicpb.Message, dynamicpb.Message](
		"/"+covDocumentationService+"/GetDocumentation",
		func(_ context.Context, call *connect.Request[dynamicpb.Message]) (*connect.Response[dynamicpb.Message], error) {
			message, err := answer(call.Msg)
			if err != nil {
				return nil, err
			}
			return connect.NewResponse(message), nil
		},
		connect.WithRequestInitializer(covMessageInitializer(request)),
	)
}

// covSetField sets one field of a dynamic message, failing where a nil descriptor would otherwise panic.
func covSetField(t *testing.T, message *dynamicpb.Message, field string, value protoreflect.Value) {
	t.Helper()
	descriptor := message.Descriptor().Fields().ByName(protoreflect.Name(field))
	require.NotNil(t, descriptor, "the message has no field named %q", field)
	message.Set(descriptor, value)
}

// covAppendMessage appends to a repeated message field.
func covAppendMessage(t *testing.T, message *dynamicpb.Message, field string, value protoreflect.Message) {
	t.Helper()
	descriptor := message.Descriptor().Fields().ByName(protoreflect.Name(field))
	require.NotNil(t, descriptor, "the message has no field named %q", field)
	message.Mutable(descriptor).List().Append(protoreflect.ValueOfMessage(value))
}

// covStringField reads one string field, answering empty for a field the message does not declare.
func covStringField(message *dynamicpb.Message, field string) string {
	descriptor := message.Descriptor().Fields().ByName(protoreflect.Name(field))
	if descriptor == nil {
		return ""
	}
	return message.Get(descriptor).String()
}

// covDocumentationAnswer builds the documentation an endpoint would answer with: one method, one parameter
// with a field of its own, and every scalar a parameter carries. Reading it back is what proves the model
// is built from the answer rather than from the request.
func covDocumentationAnswer(t *testing.T, file protoreflect.FileDescriptor) *dynamicpb.Message {
	t.Helper()
	documentation := dynamicpb.NewMessage(file.Messages().ByName("Documentation"))
	covSetField(t, documentation, "name", protoreflect.ValueOfString("cov.target.v1.Target"))
	covSetField(t, documentation, "description", protoreflect.ValueOfString("Target is documented by its endpoint."))

	parameter := dynamicpb.NewMessage(file.Messages().ByName("Parameter"))
	covSetField(t, parameter, "name", protoreflect.ValueOfString("operation_id"))
	covSetField(t, parameter, "description", protoreflect.ValueOfString("The operation to call."))
	covSetField(t, parameter, "type_name", protoreflect.ValueOfString("string"))
	covSetField(t, parameter, "default_value", protoreflect.ValueOfString("-"))
	covSetField(t, parameter, "repeated", protoreflect.ValueOfBool(true))
	covSetField(t, parameter, "required", protoreflect.ValueOfBool(true))

	inner := dynamicpb.NewMessage(file.Messages().ByName("Parameter"))
	covSetField(t, inner, "name", protoreflect.ValueOfString("nested"))
	covAppendMessage(t, parameter, "fields", inner)

	method := dynamicpb.NewMessage(file.Messages().ByName("Method"))
	covSetField(t, method, "name", protoreflect.ValueOfString("InvokeApi"))
	covSetField(t, method, "description", protoreflect.ValueOfString("InvokeApi calls an operation."))
	covSetField(t, method, "input_type", protoreflect.ValueOfString("cov.target.v1.InvokeApiRequest"))
	covSetField(t, method, "output_type", protoreflect.ValueOfString("cov.target.v1.InvokeApiResponse"))
	covSetField(t, method, "client_streaming", protoreflect.ValueOfBool(true))
	covSetField(t, method, "server_streaming", protoreflect.ValueOfBool(false))
	covAppendMessage(t, method, "parameters", parameter)
	covAppendMessage(t, documentation, "methods", method)

	return documentation
}

// A contract this process already has documentation for is answered from that documentation, without a
// call. The endpoint here is a closed port, so a call would fail: succeeding is the assertion that the
// registry was consulted first. Without that preference every description would cost a round trip and a
// deployment whose documentation service is not published would lose descriptions it already holds.
func TestLocalDocumentationIsAnsweredWithoutAskingTheEndpoint(t *testing.T) {
	client := discovery.New("http://127.0.0.1:1")

	documentation, err := client.GetDocumentation(context.Background(), invokerService)

	require.NoError(t, err, "the endpoint was asked for documentation this process already had")
	require.NotNil(t, documentation)
	assert.Equal(t, invokerService, documentation.Name)
	assert.Contains(t, documentation.Description, "invoker")
	require.NotEmpty(t, documentation.Methods)
	assert.NotEmpty(t, documentation.Methods[0].Description)
}

// A contract this process does not have is described by the endpoint. Every part of the answer has to
// reach the model: a description lost here is a help page that says less, and a parameter lost here is a
// flag a caller cannot set.
func TestDocumentationIsReadFromTheEndpointForAContractThisProcessDoesNotHave(t *testing.T) {
	target := make(chan string, 1)
	client := covDocumentationEndpoint(t, covDocumentationContract, func(file protoreflect.FileDescriptor) http.Handler {
		response := file.Messages().ByName("GetDocumentationResponse")
		return covDocumentationHandler(t, file, func(request *dynamicpb.Message) (*dynamicpb.Message, error) {
			target <- covStringField(request, "service_name")
			answer := dynamicpb.NewMessage(response)
			answer.Set(response.Fields().ByName("documentation"),
				protoreflect.ValueOfMessage(covDocumentationAnswer(t, file)))
			return answer, nil
		})
	})

	documentation, err := client.GetDocumentation(context.Background(), "cov.target.v1.Target")

	require.NoError(t, err)
	require.NotNil(t, documentation)

	// The request named the service it asked about: a request with an empty name would be answered with
	// something else, and the caller would never know.
	assert.Equal(t, "cov.target.v1.Target", <-target, "the request did not carry the service being asked about")

	assert.Equal(t, "cov.target.v1.Target", documentation.Name)
	assert.Equal(t, "Target is documented by its endpoint.", documentation.Description)
	require.Len(t, documentation.Methods, 1)

	method := documentation.Methods[0]
	assert.Equal(t, "InvokeApi", method.Name)
	assert.Equal(t, "InvokeApi calls an operation.", method.Description)
	assert.Equal(t, "cov.target.v1.InvokeApiRequest", method.InputType)
	assert.Equal(t, "cov.target.v1.InvokeApiResponse", method.OutputType)
	assert.True(t, method.ClientStreaming)
	assert.False(t, method.ServerStreaming)

	require.Len(t, method.Parameters, 1)
	parameter := method.Parameters[0]
	assert.Equal(t, "operation_id", parameter.Name)
	assert.Equal(t, "The operation to call.", parameter.Description)
	assert.Equal(t, "string", parameter.TypeName)
	assert.Equal(t, "-", parameter.DefaultValue)
	assert.True(t, parameter.Repeated)
	assert.True(t, parameter.Required)

	// A parameter with fields of its own is a message-typed flag, and its fields are what a caller actually
	// sets. Losing them leaves a flag that cannot be filled in.
	require.Len(t, parameter.Fields, 1)
	assert.Equal(t, "nested", parameter.Fields[0].Name)
}

// An endpoint that serves no documentation service is the ordinary case — most deployments publish their
// contract and nothing else — and it has to be reported as that, naming the service that was looked for.
func TestAnEndpointWithoutADocumentationServiceIsReported(t *testing.T) {
	_, client, _ := serve(t)

	_, err := client.GetDocumentation(context.Background(), "cov.target.v1.Target")

	require.Error(t, err)
	assert.Contains(t, err.Error(), covDocumentationService,
		"the error must say which service could not be described")
}

// A service that is named like the documentation service but has no GetDocumentation method is a
// deployment whose contract is not the one the client speaks. The answer must say so rather than fall
// through to a nil method.
func TestADocumentationServiceWithoutTheMethodIsReported(t *testing.T) {
	client := covDocumentationEndpoint(t, `syntax = "proto3";

package toolbox.documentation.v1;

message Request {
  string service_name = 1;
}

message Response {
  string note = 1;
}

service DocumentationService {
  rpc Describe(Request) returns (Response);
}
`, func(protoreflect.FileDescriptor) http.Handler {
		return http.NotFoundHandler()
	})

	_, err := client.GetDocumentation(context.Background(), "cov.target.v1.Target")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no GetDocumentation method")
}

// A request without the field the client sets is a contract drift, and calling anyway would ask the
// endpoint about nothing at all — it would answer about whatever an empty name matches, and the caller
// would be handed another service's documentation.
func TestADocumentationRequestWithoutTheTargetFieldIsReported(t *testing.T) {
	client := covDocumentationEndpoint(t, `syntax = "proto3";

package toolbox.documentation.v1;

message Documentation {
  string name = 1;
}

message GetDocumentationRequest {
  string target = 1;
}

message GetDocumentationResponse {
  Documentation documentation = 1;
}

service DocumentationService {
  rpc GetDocumentation(GetDocumentationRequest) returns (GetDocumentationResponse);
}
`, func(protoreflect.FileDescriptor) http.Handler {
		return http.NotFoundHandler()
	})

	_, err := client.GetDocumentation(context.Background(), "cov.target.v1.Target")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no service_name field")
}

// A response without the documentation field is the same drift in the other direction. Reading it would
// report an empty documentation, which is indistinguishable from an endpoint that answered nothing.
func TestADocumentationResponseWithoutTheDocumentationFieldIsReported(t *testing.T) {
	client := covDocumentationEndpoint(t, `syntax = "proto3";

package toolbox.documentation.v1;

message GetDocumentationRequest {
  string service_name = 1;
}

message GetDocumentationResponse {
  string note = 1;
}

service DocumentationService {
  rpc GetDocumentation(GetDocumentationRequest) returns (GetDocumentationResponse);
}
`, func(file protoreflect.FileDescriptor) http.Handler {
		return covDocumentationHandler(t, file, func(*dynamicpb.Message) (*dynamicpb.Message, error) {
			return dynamicpb.NewMessage(file.Messages().ByName("GetDocumentationResponse")), nil
		})
	})

	_, err := client.GetDocumentation(context.Background(), "cov.target.v1.Target")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no documentation field")
}

// An endpoint that answers with no documentation at all has said nothing about the service, and that is an
// error rather than an empty documentation: a caller that received an empty one would render a contract
// nobody documented instead of falling back to another source.
func TestAnEmptyDocumentationAnswerIsReported(t *testing.T) {
	client := covDocumentationEndpoint(t, covDocumentationContract, func(file protoreflect.FileDescriptor) http.Handler {
		return covDocumentationHandler(t, file, func(*dynamicpb.Message) (*dynamicpb.Message, error) {
			// The response is valid and its documentation field is left unset.
			return dynamicpb.NewMessage(file.Messages().ByName("GetDocumentationResponse")), nil
		})
	})

	_, err := client.GetDocumentation(context.Background(), "cov.target.v1.Target")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "documentation response is empty")
}

// A documentation model narrower than the one this client reads is a deployment running a different
// version of the documentation service. The answer is still usable — the fields it does have are read and
// the rest are absent — and asserting that keeps a version skew from turning into a failure to read any of
// it.
func TestANarrowerDocumentationModelIsStillReadable(t *testing.T) {
	client := covDocumentationEndpoint(t, `syntax = "proto3";

package toolbox.documentation.v1;

message Documentation {
  string description = 1;
  repeated Method methods = 2;
}

message Method {
  string name = 1;
  repeated Parameter parameters = 2;
}

message Parameter {
  string name = 1;
}

message GetDocumentationRequest {
  string service_name = 1;
}

message GetDocumentationResponse {
  Documentation documentation = 1;
}

service DocumentationService {
  rpc GetDocumentation(GetDocumentationRequest) returns (GetDocumentationResponse);
}
`, func(file protoreflect.FileDescriptor) http.Handler {
		responseDescriptor := file.Messages().ByName("GetDocumentationResponse")
		return covDocumentationHandler(t, file, func(*dynamicpb.Message) (*dynamicpb.Message, error) {
			parameter := dynamicpb.NewMessage(file.Messages().ByName("Parameter"))
			covSetField(t, parameter, "name", protoreflect.ValueOfString("id"))

			method := dynamicpb.NewMessage(file.Messages().ByName("Method"))
			covSetField(t, method, "name", protoreflect.ValueOfString("Read"))
			covAppendMessage(t, method, "parameters", parameter)

			documentation := dynamicpb.NewMessage(file.Messages().ByName("Documentation"))
			covSetField(t, documentation, "description", protoreflect.ValueOfString("Older documentation service."))
			covAppendMessage(t, documentation, "methods", method)

			answer := dynamicpb.NewMessage(responseDescriptor)
			answer.Set(responseDescriptor.Fields().ByName("documentation"), protoreflect.ValueOfMessage(documentation))
			return answer, nil
		})
	})

	documentation, err := client.GetDocumentation(context.Background(), "cov.target.v1.Target")

	require.NoError(t, err)
	require.NotNil(t, documentation)
	assert.Empty(t, documentation.Name, "a field the answer does not carry cannot be reported")
	assert.Equal(t, "Older documentation service.", documentation.Description)
	require.Len(t, documentation.Methods, 1)
	assert.Empty(t, documentation.Methods[0].InputType)
	assert.False(t, documentation.Methods[0].ClientStreaming)
	require.Len(t, documentation.Methods[0].Parameters, 1)
	assert.Equal(t, "id", documentation.Methods[0].Parameters[0].Name)
	assert.Empty(t, documentation.Methods[0].Parameters[0].TypeName)
	assert.False(t, documentation.Methods[0].Parameters[0].Repeated)
}

// A documentation call the endpoint refuses is reported as that call's failure. Substituting an empty
// documentation would make a broken documentation service look like a contract with no descriptions.
func TestAFailedDocumentationCallIsReported(t *testing.T) {
	client := covDocumentationEndpoint(t, covDocumentationContract, func(file protoreflect.FileDescriptor) http.Handler {
		return covDocumentationHandler(t, file, func(*dynamicpb.Message) (*dynamicpb.Message, error) {
			return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("the documentation index is not ready"))
		})
	})

	_, err := client.GetDocumentation(context.Background(), "cov.target.v1.Target")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetDocumentation")
}
