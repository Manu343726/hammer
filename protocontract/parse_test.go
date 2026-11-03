package protocontract

import (
	"testing"

	api "github.com/Manu343726/hammer"
	testcontractv1 "github.com/Manu343726/hammer/internal/testcontract/testcontractv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// The fixture for these tests is a real contract, internal/testcontract, whose generated Go is
// checked in. A package that reads contracts should be tested against one: hand-building every
// descriptor would prove that the reader reads what the test wrote, and would never exercise the
// path a deployment takes, where the contract arrives as bytes a build produced.
//
// The fixture also declares an annotated method, which no library contract is needed for and
// which a bare descriptor cannot supply — a comment only exists in a source file.

// descriptorSetFor renders a file's FileDescriptorSet, which is what a reader receives when a
// caller hands it a compiled contract rather than an endpoint.
func descriptorSetFor(t *testing.T, file protoreflect.FileDescriptor) []byte {
	t.Helper()
	encoded, err := proto.Marshal(&descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(file)},
	})
	require.NoError(t, err)
	return encoded
}

// contractDescriptorSet is the fixture contract, as bytes.
func contractDescriptorSet(t *testing.T) []byte {
	t.Helper()
	return descriptorSetFor(t, testcontractv1.File_testcontract_proto)
}

// streamingDescriptorSet builds a contract with a streaming method, because the fixture contract
// has none and the streaming path still has to be described honestly.
func streamingDescriptorSet(t *testing.T) []byte {
	t.Helper()
	descriptor := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("toolbox/streaming/v1/stream.proto"),
		Package: proto.String("toolbox.streaming.v1"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Request"),
		}, {
			Name: proto.String("Response"),
		}},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("StreamService"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:            proto.String("Stream"),
				InputType:       proto.String(".toolbox.streaming.v1.Request"),
				OutputType:      proto.String(".toolbox.streaming.v1.Response"),
				ServerStreaming: proto.Bool(true),
			}},
		}},
	}
	encoded, err := proto.Marshal(&descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{descriptor},
	})
	require.NoError(t, err)
	return encoded
}

func parseForTest(t *testing.T, document []byte) api.API {
	t.Helper()
	described, _, err := NewDescriptor(Descriptor{APIID: "contract"}).FromDescriptorSet(document)
	require.NoError(t, err)
	return described
}

func TestDescriptorReadsContractFromDescriptorSet(t *testing.T) {
	parsed := parseForTest(t, contractDescriptorSet(t))

	assert.Equal(t, "contract", parsed.ID)
	assert.Equal(t, Format, parsed.Format)
	assert.Equal(t, SourceKindDescriptorSet, parsed.Source.Kind)
	assert.NotEmpty(t, parsed.Source.Digest, "a described contract carries the digest of what was read")
	require.NotEmpty(t, parsed.Services)

	names := make([]string, 0, len(parsed.Services))
	for _, service := range parsed.Services {
		names = append(names, service.Name)
		assert.Equal(t, service.ID, "contract/"+service.Name)
	}
	assert.Contains(t, names, "testcontract.v1.ApiParserService")
	assert.Contains(t, names, "testcontract.v1.ApiAdapterService")
	assert.Contains(t, names, "testcontract.v1.ApiInvokerService")
}

func TestDescriptorDerivesAnIdentifierWhenNoneIsGiven(t *testing.T) {
	described, _, err := NewDescriptor(Descriptor{}).FromDescriptorSet(contractDescriptorSet(t))
	require.NoError(t, err)
	// The identifier comes from the first service in a deterministic order, so the same contract
	// always describes the same API. The contract's services are sorted, and the adapter service
	// comes first alphabetically — a fixture whose declaration order differed from its sorted
	// order would catch a reader that stopped sorting.
	assert.Equal(t, "api-adapter", described.ID)
}

func TestDescriptorDescribesRequestAndResponse(t *testing.T) {
	parsed := parseForTest(t, contractDescriptorSet(t))
	render, ok := parsed.Operation(operationID(t, parsed, "testcontract.v1.ApiAdapterService", "RenderApi"))
	require.True(t, ok)

	assert.Equal(t, "RenderApi", render.Method)
	require.NotNil(t, render.Request)
	target, ok := render.Request.Property("target")
	require.True(t, ok)
	assert.Equal(t, api.TypeString, target.Schema.Type)

	require.NotNil(t, render.Response)
	files, ok := render.Response.Property("files")
	require.True(t, ok)
	assert.Equal(t, api.TypeArray, files.Schema.Type)
}

func TestDescriptorMarksStreamingWithoutInvokingIt(t *testing.T) {
	parsed := parseForTest(t, streamingDescriptorSet(t))
	stream, ok := parsed.Operation(operationID(t, parsed, "toolbox.streaming.v1.StreamService", "Stream"))
	require.True(t, ok)

	assert.True(t, stream.Streaming.Server, "a server-streaming method is described as such")
	assert.False(t, stream.Streaming.Client)
	assert.True(t, stream.Streaming.Streaming())
}

func TestDescriptorInventsNoSideEffectWhereTheContractDeclaresNone(t *testing.T) {
	// A contract says what invoking a method does, and a contract says nothing about who may
	// invoke it. A reader must not invent the second from the first: the policy answers that
	// question, from the deployment.
	//
	// The fixture is a hand-built contract that declares no effects at all, rather than the
	// shared one — which does declare them, and which the reflection test below reads from its
	// .proto. Using it here would have made this test assert the opposite of its name.
	parsed := parseForTest(t, streamingDescriptorSet(t))
	for _, service := range parsed.Services {
		for _, operation := range service.Operations {
			assert.Empty(t, operation.SideEffects,
				"this fixture's contract declares none, so its operations are unclassified")
		}
	}
}

func TestDescriptorCarriesTheSideEffectsTheContractDeclares(t *testing.T) {
	// The other half of the same rule: what a contract does declare must survive, or every
	// operation is unclassified and a policy naming read or write covers none of them.
	parsed := parseForTest(t, contractDescriptorSet(t))

	declared := 0
	for _, service := range parsed.Services {
		for _, operation := range service.Operations {
			if len(operation.SideEffects) > 0 {
				declared++
			}
		}
	}
	assert.NotZero(t, declared,
		"the fixture's contract declares side effects and none of them reached the descriptor")
}

func TestDescriptorRejectsUnusableInput(t *testing.T) {
	_, _, err := NewDescriptor(Descriptor{}).FromDescriptorSet(nil)
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))

	_, _, err = NewDescriptor(Descriptor{}).FromDescriptorSet([]byte("not a descriptor set"))
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "FileDescriptorSet")

	_, _, err = NewDescriptor(Descriptor{}).FromEndpoint(t.Context(), "")
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
}

func TestDescriptorRejectsAnExcludedService(t *testing.T) {
	described, _, err := NewDescriptor(Descriptor{
		APIID:           "contract",
		ExcludeServices: []string{"testcontract.v1.ApiParserService"},
	}).FromDescriptorSet(contractDescriptorSet(t))
	require.NoError(t, err)
	for _, service := range described.Services {
		assert.NotEqual(t, "testcontract.v1.ApiParserService", service.Name)
	}
}

func TestFormatDescriptorDescribesTheFormat(t *testing.T) {
	descriptor := FormatDescriptor()
	assert.Equal(t, string(Format), descriptor.ID)
	assert.Contains(t, descriptor.FileExtensions, "protoset")
	assert.NotEmpty(t, descriptor.Description)

	transports := TransportDescriptors()
	require.NotEmpty(t, transports)
	ids := make([]string, 0, len(transports))
	for _, transport := range transports {
		ids = append(ids, transport.ID)
	}
	assert.Equal(t, []string{"connectrpc", "grpc", "grpc-web"}, ids)
}

func TestSlugDerivesReadableIdentifiers(t *testing.T) {
	assert.Equal(t, "echo", Slug("toolbox.testecho.v1.EchoService"), "the service suffix is dropped, since every service carries one")
	assert.Equal(t, "api-parser", Slug("testcontract.v1.ApiParserService"))
	assert.Equal(t, "api", Slug("v1."))
}

func operationID(t *testing.T, described api.API, service, method string) string {
	t.Helper()
	for _, candidate := range described.Services {
		if candidate.Name != service {
			continue
		}
		for _, operation := range candidate.Operations {
			if operation.Name == method {
				return operation.ID
			}
		}
	}
	t.Fatalf("contract declares no %s/%s", service, method)
	return ""
}
