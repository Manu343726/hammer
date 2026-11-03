package protocontract_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
	"google.golang.org/protobuf/types/descriptorpb"
)

// The reader's promises are about the contracts a deployment actually carries: a
// FileDescriptorSet a build produced, or a live endpoint that describes itself.
// These tests hold the reader to the parts of a contract the checked-in fixture
// cannot exercise — recursion, maps, oneofs, presence, streaming on either side —
// and to the failure modes, because a reader that turns a broken document into an
// empty description is worse than one that refuses it.

// covFileSet is the bytes a build hands a reader: a compiled FileDescriptorSet.
func covFileSet(t *testing.T, files ...*descriptorpb.FileDescriptorProto) []byte {
	t.Helper()
	encoded, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: files})
	require.NoError(t, err)
	return encoded
}

// covFixtureSet is the checked-in contract, compiled here rather than written by
// hand, so the fixture stays the one the rest of the package is tested against.
func covFixtureSet(t *testing.T) []byte {
	t.Helper()
	return covFileSet(t, protodesc.ToFileDescriptorProto(testcontractv1.File_testcontract_proto))
}

// covScalar is one scalar field of a hand-built contract.
func covScalar(name string, number int32, kind descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(number),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:   kind.Enum(),
	}
}

// covOneofField is one member of a oneof, which has to name the alternative it
// belongs to.
func covOneofField(name string, number int32, kind descriptorpb.FieldDescriptorProto_Type, oneof int32) *descriptorpb.FieldDescriptorProto {
	field := covScalar(name, number, kind)
	field.OneofIndex = proto.Int32(oneof)
	return field
}

// covMessageField is one message-valued field, repeated or singular.
func covMessageField(name string, number int32, typeName string, repeated bool) *descriptorpb.FieldDescriptorProto {
	label := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	if repeated {
		label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED
	}
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Label:    label.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(typeName),
	}
}

// covRecursiveFile declares, in one file, every construct a protobuf contract can
// state that the fixture does not: a self-referencing message, a repeated one, an
// enum, a map, a oneof, and a field with explicit presence.
//
// It is hand-built because the fixture is deliberately small and regenerating it
// for one test would make every other test depend on constructs it does not need.
func covRecursiveFile() *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Name:    proto.String("cov/parse/v1/graph.proto"),
		Package: proto.String("cov.parse.v1"),
		Syntax:  proto.String("proto3"),
		EnumType: []*descriptorpb.EnumDescriptorProto{{
			Name: proto.String("Kind"),
			Value: []*descriptorpb.EnumValueDescriptorProto{
				{Name: proto.String("KIND_UNSPECIFIED"), Number: proto.Int32(0)},
				{Name: proto.String("KIND_LEAF"), Number: proto.Int32(1)},
			},
		}},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				// Node names itself, directly and through a map: a reader that
				// expanded a recursive shape without noticing would never terminate.
				Name: proto.String("Node"),
				Field: []*descriptorpb.FieldDescriptorProto{
					covScalar("name", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					covMessageField("child", 2, ".cov.parse.v1.Node", false),
				},
			},
			{
				Name: proto.String("Request"),
				OneofDecl: []*descriptorpb.OneofDescriptorProto{
					{Name: proto.String("choice")},
					{Name: proto.String("_nickname")},
				},
				NestedType: []*descriptorpb.DescriptorProto{
					{
						Name:    proto.String("LinksEntry"),
						Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
						Field: []*descriptorpb.FieldDescriptorProto{
							covScalar("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
							covMessageField("value", 2, ".cov.parse.v1.Node", false),
						},
					},
					{
						Name:  proto.String("Marker"),
						Field: []*descriptorpb.FieldDescriptorProto{covScalar("note", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING)},
					},
				},
				Field: []*descriptorpb.FieldDescriptorProto{
					covMessageField("node", 1, ".cov.parse.v1.Node", false),
					covMessageField("children", 2, ".cov.parse.v1.Node", true),
					{
						Name:     proto.String("kind"),
						Number:   proto.Int32(3),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
						TypeName: proto.String(".cov.parse.v1.Kind"),
					},
					{
						Name:     proto.String("links"),
						Number:   proto.Int32(4),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: proto.String(".cov.parse.v1.Request.LinksEntry"),
					},
					{
						Name:           proto.String("nickname"),
						Number:         proto.Int32(5),
						Label:          descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:           descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						OneofIndex:     proto.Int32(1),
						Proto3Optional: proto.Bool(true),
					},
					covOneofField("text", 6, descriptorpb.FieldDescriptorProto_TYPE_STRING, 0),
					covOneofField("number", 7, descriptorpb.FieldDescriptorProto_TYPE_INT32, 0),
				},
			},
			{Name: proto.String("Response"), Field: []*descriptorpb.FieldDescriptorProto{covScalar("ok", 1, descriptorpb.FieldDescriptorProto_TYPE_BOOL)}},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("GraphService"),
			Method: []*descriptorpb.MethodDescriptorProto{
				{Name: proto.String("Unary"), InputType: proto.String(".cov.parse.v1.Request"), OutputType: proto.String(".cov.parse.v1.Response")},
				{Name: proto.String("Upload"), InputType: proto.String(".cov.parse.v1.Request"), OutputType: proto.String(".cov.parse.v1.Response"), ClientStreaming: proto.Bool(true)},
				{Name: proto.String("Watch"), InputType: proto.String(".cov.parse.v1.Request"), OutputType: proto.String(".cov.parse.v1.Response"), ServerStreaming: proto.Bool(true)},
				{Name: proto.String("Chat"), InputType: proto.String(".cov.parse.v1.Request"), OutputType: proto.String(".cov.parse.v1.Response"), ClientStreaming: proto.Bool(true), ServerStreaming: proto.Bool(true)},
			},
		}},
	}
}

func TestCoverParseDescribesEveryShapeAContractCanDeclare(t *testing.T) {
	described, warnings, err := protocontract.NewDescriptor(protocontract.Descriptor{APIID: "graph"}).
		FromDescriptorSet(covFileSet(t, covRecursiveFile()))
	require.NoError(t, err)
	assert.Empty(t, warnings)

	unary, ok := described.Operation("graph/cov.parse.v1.GraphService/Unary")
	require.True(t, ok)
	require.NotNil(t, unary.Request)

	// Recursion terminates and is described by name rather than by expansion: the
	// reference is what tells a caller the two shapes are the same message.
	node, ok := unary.Request.Property("node")
	require.True(t, ok)
	assert.Equal(t, "cov.parse.v1.Node", node.Schema.Ref)
	child, ok := node.Schema.Property("child")
	require.True(t, ok, "a self-referencing message still describes its members")
	assert.Equal(t, "cov.parse.v1.Node", child.Schema.Ref)

	// A repeated message is an array of the same shape, not a second declaration.
	children, ok := unary.Request.Property("children")
	require.True(t, ok)
	require.Equal(t, api.TypeArray, children.Schema.Type)
	require.NotNil(t, children.Schema.Items)
	assert.Equal(t, "cov.parse.v1.Node", children.Schema.Items.Ref)

	// An enum arrives as its literals: a reader that flattened it into a string
	// would lose the set of values a caller may send.
	kind, ok := unary.Request.Property("kind")
	require.True(t, ok)
	assert.Equal(t, []string{"KIND_UNSPECIFIED", "KIND_LEAF"}, kind.Schema.Enum)

	// A map is an open object with a declared value shape, which is the only way a
	// parameter-neutral description can state it.
	links, ok := unary.Request.Property("links")
	require.True(t, ok)
	require.NotNil(t, links.Schema.AdditionalProperties)
	assert.Equal(t, "cov.parse.v1.Node", links.Schema.AdditionalProperties.Ref)

	// A oneof's members are declared fields, and presence is a fact of the field:
	// a reader that dropped either would describe a request a caller cannot send.
	for _, name := range []string{"text", "number"} {
		_, found := unary.Request.Property(name)
		assert.True(t, found, "oneof member %q was dropped", name)
	}
	assert.True(t, unary.Request.IsRequired("nickname"),
		"an optional field has presence, and presence is what this reader calls required")
}

func TestCoverParseDescribesStreamingOnEitherSide(t *testing.T) {
	// A streaming direction that is read from the wrong side compiles and describes
	// a service no client can call.
	described, _, err := protocontract.NewDescriptor(protocontract.Descriptor{APIID: "graph"}).
		FromDescriptorSet(covFileSet(t, covRecursiveFile()))
	require.NoError(t, err)

	for name, expected := range map[string]api.Streaming{
		"Unary":  {},
		"Upload": {Client: true},
		"Watch":  {Server: true},
		"Chat":   {Client: true, Server: true},
	} {
		operation, ok := described.Operation("graph/cov.parse.v1.GraphService/" + name)
		require.True(t, ok, "method %q was not described", name)
		assert.Equal(t, expected, operation.Streaming, "method %q", name)
		assert.Equal(t, expected.Streaming(), operation.Streaming.Streaming(), "method %q", name)
	}
}

func TestCoverParseDropsTheReflectionServicesAServerAlwaysExposes(t *testing.T) {
	// Every gRPC server that can be described exposes reflection, and a description
	// that registered it would publish an operation no policy was written for.
	reflection := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("cov/reflection/reflection.proto"),
		Package: proto.String("grpc.reflection.v1"),
		Syntax:  proto.String("proto3"),
		Service: []*descriptorpb.ServiceDescriptorProto{{Name: proto.String("ServerReflection")}},
	}
	described, _, err := protocontract.NewDescriptor(protocontract.Descriptor{APIID: "graph"}).
		FromDescriptorSet(covFileSet(t, covRecursiveFile(), reflection))
	require.NoError(t, err)

	names := make([]string, 0, len(described.Services))
	for _, service := range described.Services {
		names = append(names, service.Name)
	}
	assert.Equal(t, []string{"cov.parse.v1.GraphService"}, names,
		"the reflection service matches the name the discovery package hides")
}

func TestCoverParseRefusesAContractWithNoService(t *testing.T) {
	// A file with messages and no service describes nothing a caller can invoke, so
	// an empty description is a failure rather than an API with no operations.
	_, _, err := protocontract.NewDescriptor(protocontract.Descriptor{}).FromDescriptorSet(covFileSet(t,
		&descriptorpb.FileDescriptorProto{
			Name:        proto.String("cov/empty/v1/empty.proto"),
			Package:     proto.String("cov.empty.v1"),
			Syntax:      proto.String("proto3"),
			MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Thing")}},
		}))
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "no service")
}

func TestCoverParseRefusesADocumentThatIsNotAFileDescriptorSet(t *testing.T) {
	// A caller that handed the wrong artifact — a single descriptor, say — has a
	// decoding problem, and reporting it as an empty contract would hide it.
	_, _, err := protocontract.NewDescriptor(protocontract.Descriptor{}).FromDescriptorSet(covFileSet(t,
		&descriptorpb.FileDescriptorProto{
			Name:       proto.String("cov/broken/v1/broken.proto"),
			Package:    proto.String("cov.broken.v1"),
			Syntax:     proto.String("proto3"),
			Dependency: []string{"cov/missing/v1/missing.proto"},
		}))
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "build descriptors")
}

func TestCoverParseRefusesAMethodWithNoTypes(t *testing.T) {
	// A method without an input or output type is not a contract a server can serve,
	// and a reader that invented the missing types would describe an API that does
	// not exist.
	document := covFileSet(t, &descriptorpb.FileDescriptorProto{
		Name:    proto.String("cov/untyped/v1/untyped.proto"),
		Package: proto.String("cov.untyped.v1"),
		Syntax:  proto.String("proto3"),
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name:   proto.String("UntypedService"),
			Method: []*descriptorpb.MethodDescriptorProto{{Name: proto.String("Call")}},
		}},
	})
	_, _, err := protocontract.NewDescriptor(protocontract.Descriptor{}).FromDescriptorSet(document)
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
}

func TestCoverParseDescribeChoosesTheDocumentOverTheEndpoint(t *testing.T) {
	// Describe is the entry point a caller with a description request uses, and the
	// document wins: a caller that supplied bytes has already resolved where the
	// contract comes from, so reaching for an endpoint would answer a question
	// nobody asked.
	result, err := protocontract.NewDescriptor(protocontract.Descriptor{APIID: "fallback"}).
		Describe(t.Context(), api.DescribeRequest{
			Document: covFixtureSet(t),
			BaseURL:  "http://127.0.0.1:1",
			APIID:    "renamed",
			Source:   api.Source{Kind: "bundle", Location: "https://example.test/contract.pb"},
		})
	require.NoError(t, err, "an unreachable base url must not be tried when a document was supplied")
	assert.Equal(t, "renamed", result.API.ID)
	assert.Equal(t, "bundle", result.API.Source.Kind, "the caller said where the document came from")
	assert.Equal(t, "https://example.test/contract.pb", result.API.Source.Location)
	assert.NotEmpty(t, result.Formats)
	for _, operation := range result.API.Operations() {
		assert.Equal(t, "renamed", operation.APIID, "the identifier reaches every operation")
	}
}

func TestCoverParseDescribeRecordsADescriptorSetAsSuch(t *testing.T) {
	// A caller that says nothing about the source still gets one: an unnamed origin
	// would leave a catalog unable to say where a description came from.
	result, err := protocontract.NewDescriptor(protocontract.Descriptor{}).
		Describe(t.Context(), api.DescribeRequest{Document: covFixtureSet(t)})
	require.NoError(t, err)
	assert.Equal(t, protocontract.SourceKindDescriptorSet, result.API.Source.Kind)
	assert.Equal(t, protocontract.SourceKindDescriptorSet, result.API.Source.Location)
	assert.NotEmpty(t, result.API.Source.Digest)
}

func TestCoverParseDescribeKeepsTheIdentifierItAlreadyHas(t *testing.T) {
	// Asking for the identifier a description already carries must not rewrite it:
	// a caller that repeats itself would otherwise re-derive every operation id.
	result, err := protocontract.NewDescriptor(protocontract.Descriptor{APIID: "contract"}).
		Describe(t.Context(), api.DescribeRequest{Document: covFixtureSet(t), APIID: "contract"})
	require.NoError(t, err)
	assert.Equal(t, "contract", result.API.ID)
	assert.Equal(t, "contract", result.API.Name)
}

func TestCoverParseDescribeRefusesAnEmptyRequest(t *testing.T) {
	// Neither a document nor an endpoint is not a description request.
	_, err := protocontract.NewDescriptor(protocontract.Descriptor{}).
		Describe(t.Context(), api.DescribeRequest{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "document or a base url")
}

func TestCoverParseDescribeReportsADocumentItCannotRead(t *testing.T) {
	// The error has to travel out of Describe rather than being turned into an empty
	// description by the layer that wraps the reader.
	result, err := protocontract.NewDescriptor(protocontract.Descriptor{}).
		Describe(t.Context(), api.DescribeRequest{Document: []byte("this is not a descriptor set")})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Empty(t, result.API.Services)
	assert.NotEmpty(t, result.Formats, "a failed read still says which formats the reader claims")
}

func TestCoverParseDescribeReadsAnEndpointAndRenamesIt(t *testing.T) {
	// The endpoint path is the one a host uses when nobody ships the contract, and
	// the identifier the request names has to reach the operations it described.
	endpoint := covServe(t, covParserMount())
	result, err := protocontract.NewDescriptor(protocontract.Descriptor{}).
		Describe(t.Context(), api.DescribeRequest{BaseURL: endpoint, APIID: "live"})
	require.NoError(t, err)
	assert.Equal(t, "live", result.API.ID)
	assert.Equal(t, protocontract.SourceKindReflection, result.API.Source.Kind)
	require.Len(t, result.API.DeclaredServers, 1)
	assert.Equal(t, endpoint, result.API.DeclaredServers[0].URL)
	for _, operation := range result.API.Operations() {
		assert.Contains(t, operation.ID, "live/", "the identifier reaches the operations")
	}
}

func TestCoverParseDescribeReportsAnEndpointItCannotReach(t *testing.T) {
	// A dead endpoint is not an empty contract: the caller has to be able to tell a
	// deployment that is down from a service that declares nothing.
	unreachable := httptest.NewServer(http.NotFoundHandler())
	endpoint := unreachable.URL
	unreachable.Close()

	_, err := protocontract.NewDescriptor(protocontract.Descriptor{}).
		Describe(t.Context(), api.DescribeRequest{BaseURL: endpoint})
	require.Error(t, err)
	assert.Equal(t, api.KindUnavailable, api.KindOf(err))
}

func TestCoverParseFromEndpointReportsAnEndpointWithoutReflection(t *testing.T) {
	// An endpoint that answers HTTP but not reflection is a deployment problem, and
	// the classification says so rather than blaming the contract.
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	_, _, err := protocontract.NewDescriptor(protocontract.Descriptor{}).FromEndpoint(t.Context(), server.URL)
	require.Error(t, err)
	assert.Equal(t, api.KindUnavailable, api.KindOf(err))
}

func TestCoverParseFromEndpointWarnsAboutAServiceItCannotDescribe(t *testing.T) {
	// One broken service must not make the endpoint undescribable: the contract's
	// other services are still worth registering, and the reader says what it lost
	// instead of silently shipping a smaller API.
	endpoint := covServe(t,
		covParserMount(),
		testcontract.Mount{
			Name:    "cov.ghost.v1.GhostService",
			Path:    "/cov.ghost.v1.GhostService/",
			Handler: http.NotFoundHandler(),
		})

	described, warnings, err := protocontract.NewDescriptor(protocontract.Descriptor{}).FromEndpoint(t.Context(), endpoint)
	require.NoError(t, err)
	require.NotEmpty(t, warnings)
	assert.Contains(t, warnings[0], "cov.ghost.v1.GhostService")
	assert.Contains(t, warnings[0], "could not be described")

	names := make([]string, 0, len(described.Services))
	for _, service := range described.Services {
		names = append(names, service.Name)
	}
	assert.Equal(t, []string{"testcontract.v1.ApiParserService"}, names)
}

func TestCoverParseFromEndpointHonoursExclusionsAndSorts(t *testing.T) {
	// A caller excludes the services it does not want to publish, and what remains
	// is described in a deterministic order so two reads of one endpoint agree.
	endpoint := covServe(t, covAdapterMount(), covParserMount())

	described, warnings, err := protocontract.NewDescriptor(protocontract.Descriptor{}).FromEndpoint(t.Context(), endpoint)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	names := make([]string, 0, len(described.Services))
	for _, service := range described.Services {
		names = append(names, service.Name)
	}
	assert.Equal(t, []string{"testcontract.v1.ApiAdapterService", "testcontract.v1.ApiParserService"}, names,
		"the endpoint's services are described in name order, not in the order reflection listed them")

	// Excluding one leaves the other, and the identifier derived from the first
	// service is a fact of the contract rather than of declaration order.
	filtered, _, err := protocontract.NewDescriptor(protocontract.Descriptor{
		ExcludeServices: []string{"testcontract.v1.ApiAdapterService"},
	}).FromEndpoint(t.Context(), endpoint)
	require.NoError(t, err)
	require.Len(t, filtered.Services, 1)
	assert.Equal(t, "testcontract.v1.ApiParserService", filtered.Services[0].Name)
}

func TestCoverParseFromEndpointRefusesOneItCannotUse(t *testing.T) {
	// Excluding every service the endpoint exposes leaves nothing to register, and
	// an empty registration is an error rather than an empty API.
	endpoint := covServe(t, covParserMount())
	_, _, err := protocontract.NewDescriptor(protocontract.Descriptor{
		ExcludeServices: []string{"testcontract.v1.ApiParserService"},
	}).FromEndpoint(t.Context(), endpoint)
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "no describable service")
}

func TestCoverParseReusesTheClientItWasGiven(t *testing.T) {
	// A caller that shares one reflection client per endpoint hands it in, and the
	// reader must use that one rather than opening a second connection: two clients
	// for one endpoint double the reflection streams an endpoint has to carry.
	endpoint := covServe(t, covParserMount())
	shared := discovery.New(endpoint)
	reader := protocontract.NewDescriptor(protocontract.Descriptor{
		Clients: map[string]*discovery.Client{endpoint: shared},
	})
	require.Same(t, shared, reader.Clients[endpoint],
		"the constructor copies the map; it does not rebuild the clients")

	described, _, err := reader.FromEndpoint(t.Context(), endpoint)
	require.NoError(t, err)
	assert.NotEmpty(t, described.Services)

	// A second read of the same endpoint finds the client already built, which is
	// what keeps a caller from re-describing an endpoint that has not changed.
	again, _, err := reader.FromEndpoint(t.Context(), endpoint)
	require.NoError(t, err)
	assert.Len(t, again.Services, len(described.Services))
	require.Same(t, shared, reader.Clients[endpoint])
}

// covParserMount serves the fixture's parser service, which is the one contract
// every endpoint test can rely on.
func covParserMount() testcontract.Mount {
	path, handler := testcontractv1connect.NewApiParserServiceHandler(
		&testcontractv1connect.UnimplementedApiParserServiceHandler{})
	return testcontract.Mount{Name: testcontractv1connect.ApiParserServiceName, Path: path, Handler: handler}
}

// covAdapterMount serves a second fixture service, so a test can exclude one and
// still have a contract left to describe.
func covAdapterMount() testcontract.Mount {
	path, handler := testcontractv1connect.NewApiAdapterServiceHandler(
		&testcontractv1connect.UnimplementedApiAdapterServiceHandler{})
	return testcontract.Mount{Name: testcontractv1connect.ApiAdapterServiceName, Path: path, Handler: handler}
}

// covServe starts a fixture endpoint with reflection over the mounts.
func covServe(t *testing.T, mounts ...testcontract.Mount) string {
	t.Helper()
	server, err := testcontract.Serve(mounts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })
	return server.Endpoint()
}
