package protocontract

import (
	"strings"
	"testing"

	api "github.com/Manu343726/hammer"
	testcontractv1 "github.com/Manu343726/hammer/internal/testcontract/testcontractv1"
	"github.com/bufbuild/protocompile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Rendering is only half a feature until something reads the result. These tests
// take the fixture contract, render it, and hand the rendered bytes to a real
// protobuf compiler and to this package's own reader, because an artifact no
// compiler accepts is not a contract and an artifact this package cannot read back
// is a description with extra steps.
//
// The fixture is the same one the reader is tested against, deliberately: a
// round trip measured against a second fixture would prove that the renderer and
// the reader agree on a contract neither of them was built for.

// fixtureDescription is the checked-in contract, read into the standard model.
func fixtureDescription(t *testing.T) api.API {
	t.Helper()
	encoded, err := proto.Marshal(&descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(testcontractv1.File_testcontract_proto)},
	})
	require.NoError(t, err)
	described, warnings, err := NewDescriptor(Descriptor{APIID: "contract"}).FromDescriptorSet(encoded)
	require.NoError(t, err)
	require.Empty(t, warnings)
	return described
}

// renderedSource is the contract's source and nothing else, which is what a
// compiler and a person read.
func renderedSource(t *testing.T, rendered Rendered) string {
	t.Helper()
	require.NotEmpty(t, rendered.Files)
	assert.True(t, rendered.Files[0].Primary, "the contract is the file a client starts from")
	assert.Equal(t, MediaTypeContract, rendered.Files[0].MediaType)
	source := string(rendered.Files[0].Content)
	require.NotEmpty(t, source)
	return source
}

// compileRendered compiles a rendered contract the way a build would, so the test
// asserts against descriptors a compiler produced rather than against this
// package's own idea of what it wrote.
func compileRendered(t *testing.T, rendered Rendered) protoreflect.FileDescriptor {
	t.Helper()
	source := renderedSource(t, rendered)
	compiler := protocompile.Compiler{
		Resolver: &protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(map[string]string{rendered.Files[0].Name: source}),
		},
		SourceInfoMode: protocompile.SourceInfoStandard,
	}
	compiled, err := compiler.Compile(t.Context(), rendered.Files[0].Name)
	require.NoError(t, err, "a rendered contract has to compile:\n%s", source)
	require.Len(t, compiled, 1)
	return compiled[0]
}

// comparable drops the one fact that cannot survive a render: the digest of the
// document the description was read from, which is by definition a different
// document once it has been written again.
func comparable(described api.API) api.API {
	clone := described.Clone()
	clone.Source.Digest = ""
	return clone
}

func TestRenderedContractCompilesAndDeclaresWhatTheDescriptionSays(t *testing.T) {
	described := fixtureDescription(t)
	rendered, err := Render(described, RenderOptions{})
	require.NoError(t, err)
	assert.Empty(t, rendered.Warnings, "a description read from a contract loses nothing when it is written back")

	compiled := compileRendered(t, rendered)
	assert.Equal(t, "testcontract.v1", string(compiled.Package()))

	// Every service, and every operation inside it, has to be there: a contract
	// that compiled but dropped a service would pass a weaker test and lose an API.
	for _, service := range described.Services {
		declared := compiled.Services().ByName(protoreflect.Name(lastSegment(service.Name)))
		require.NotNil(t, declared, "service %q is missing from the rendered contract", service.Name)
		assert.Equal(t, len(service.Operations), declared.Methods().Len())
		for _, operation := range service.Operations {
			method := declared.Methods().ByName(protoreflect.Name(operation.Name))
			require.NotNil(t, method, "operation %q is missing from service %q", operation.Name, service.Name)
			assert.Equal(t, operation.Streaming.Client, method.IsStreamingClient())
			assert.Equal(t, operation.Streaming.Server, method.IsStreamingServer())
		}
	}

	// The field a caller supplies is named the way the description names it,
	// because that name is what an agent tool's argument is called.
	request := compiled.Messages().ByName("RenderApiRequest")
	require.NotNil(t, request)
	assert.Equal(t, protoreflect.StringKind, request.Fields().ByName("target").Kind())
	assert.Equal(t, protoreflect.BytesKind, request.Fields().ByName("document").Kind())

	// A camelCase JSON name survives even though the field is spelled the protobuf
	// way, which is the one thing a naive renderer gets wrong quietly.
	invoke := compiled.Messages().ByName("InvokeApiRequest")
	require.NotNil(t, invoke)
	field := invoke.Fields().ByName("operation_id")
	require.NotNil(t, field)
	assert.Equal(t, "operationId", field.JSONName())
}

func TestRenderedContractReadsBackAsTheDescriptionItCameFrom(t *testing.T) {
	// This is the property that makes the format bidirectional: a description that
	// was read from a contract, written out again, and read back has to describe
	// the same API — service names with their package, operation names, streaming,
	// side effects, and every request and response shape.
	//
	// Both artifacts are checked, because they are two answers to the same question.
	// The descriptor set is compiled from the source by an independent compile here,
	// which is what proves the source says what the description says; the emitted
	// .pb is checked too, because that is the artifact a caller feeds a parser.
	described := fixtureDescription(t)
	rendered, err := Render(described, RenderOptions{})
	require.NoError(t, err)

	for name, declared := range map[string][]byte{
		"compiled from the source": descriptorSetFromSource(t, rendered),
		"emitted beside it":        descriptorSetFile(t, rendered),
	} {
		reparsed, warnings, err := NewDescriptor(Descriptor{APIID: "contract"}).FromDescriptorSet(declared)
		require.NoError(t, err, name)
		require.Empty(t, warnings, name)
		assert.Equal(t, comparable(described), comparable(reparsed), name)
	}
}

func TestRenderedContractCarriesStreamingOnTheCorrectSide(t *testing.T) {
	// A streaming direction that swaps sides is the worst kind of rendering defect:
	// the contract compiles, the service looks right, and every call fails at run
	// time because the client streams where the server waits.
	described := api.API{
		ID: "feeds", Name: "feeds", Format: Format,
		Services: []api.Service{{
			Name: "feeds.v1.FeedService",
			Operations: []api.Operation{
				{Name: "Watch", Streaming: api.Streaming{Server: true}},
				{Name: "Publish", Streaming: api.Streaming{Client: true}},
				{Name: "Chat", Streaming: api.Streaming{Client: true, Server: true}},
				{Name: "Unary"},
			},
		}},
	}
	normalized, err := described.Normalize()
	require.NoError(t, err)

	rendered, err := Render(normalized, RenderOptions{})
	require.NoError(t, err)
	source := renderedSource(t, rendered)
	assert.Contains(t, source, "rpc Watch(WatchRequest) returns (stream WatchResponse);")
	assert.Contains(t, source, "rpc Publish(stream PublishRequest) returns (PublishResponse);")
	assert.Contains(t, source, "rpc Chat(stream ChatRequest) returns (stream ChatResponse);")
	assert.Contains(t, source, "rpc Unary(UnaryRequest) returns (UnaryResponse);")

	reparsed, _, err := NewDescriptor(Descriptor{APIID: "feeds"}).FromDescriptorSet(descriptorSetFile(t, rendered))
	require.NoError(t, err)
	for _, operation := range normalized.Operations() {
		found, ok := reparsed.Operation(operation.ID)
		require.True(t, ok, "operation %q did not read back", operation.Name)
		assert.Equal(t, operation.Streaming, found.Streaming, "operation %q", operation.Name)
	}
}

func TestRenderedContractClassifiesAServiceOnceWhenItCan(t *testing.T) {
	// protobuf lets a service classify every method it declares, and a method that
	// declares nothing inherits that. Writing the service-level line is therefore
	// only safe when no method depends on staying unclassified.
	uniform := api.API{
		ID: "catalog", Name: "catalog", Format: Format,
		Services: []api.Service{{
			Name:        "catalog.v1.ItemService",
			SideEffects: []api.SideEffect{api.SideEffectReadOnly},
			Operations: []api.Operation{
				{Name: "Get", SideEffects: []api.SideEffect{api.SideEffectReadOnly}},
				{Name: "List", SideEffects: []api.SideEffect{api.SideEffectReadOnly}},
			},
		}},
	}
	normalized, err := uniform.Normalize()
	require.NoError(t, err)
	rendered, err := Render(normalized, RenderOptions{})
	require.NoError(t, err)
	assert.Empty(t, rendered.Warnings)
	source := renderedSource(t, rendered)
	assert.Equal(t, 1, strings.Count(source, "@toolbox.side-effects"), "one line classifies the whole service:\n%s", source)
	assert.Contains(t, source, "// @toolbox.side-effects read_only\nservice ItemService {")

	reparsed, _, err := NewDescriptor(Descriptor{}).FromDescriptorSet(descriptorSetFile(t, rendered))
	require.NoError(t, err)
	service, ok := reparsed.Service("catalog.v1.ItemService")
	require.True(t, ok)
	assert.Equal(t, []api.SideEffect{api.SideEffectReadOnly}, service.SideEffects)
	for _, operation := range service.Operations {
		assert.Equal(t, []api.SideEffect{api.SideEffectReadOnly}, operation.SideEffects, "operation %q", operation.Name)
	}

	// The same service with one unclassified method may not be classified at the
	// service level: a method nobody classified is the one state a policy cannot
	// grant, so inheriting a declaration would authorize what the description left
	// unknown.
	mixed := normalized.Clone()
	mixed.Services[0].Operations[1].SideEffects = nil
	renderedMixed, err := Render(mixed, RenderOptions{})
	require.NoError(t, err)
	source = renderedSource(t, renderedMixed)
	assert.Equal(t, 1, strings.Count(source, "@toolbox.side-effects"),
		"the classified method keeps its own line and the service declares nothing:\n%s", source)
	assert.Contains(t, source, "// @toolbox.side-effects read_only\n  rpc Get(GetRequest)")
	assert.Contains(t, source, "rpc List(ListRequest) returns (ListResponse);")
	warning, reported := warnsAbout(renderedMixed.Warnings, "inherit")
	assert.True(t, reported, "warnings: %v", renderedMixed.Warnings)
	assert.Contains(t, warning, "catalog.v1.ItemService")

	backAgain, _, err := NewDescriptor(Descriptor{APIID: "catalog"}).FromDescriptorSet(descriptorSetFile(t, renderedMixed))
	require.NoError(t, err)
	found, ok := backAgain.Operation("catalog/catalog.v1.ItemService/List")
	require.True(t, ok)
	assert.Empty(t, found.SideEffects, "a method that declared nothing must read back unclassified")
}

func TestRenderedContractReportsWhatProtobufCannotCarry(t *testing.T) {
	described := api.API{
		ID: "shop", Name: "shop", Format: "openapi",
		SecuritySchemes: []api.SecurityScheme{{Name: "apiKeyAuth", Type: "apiKey", In: api.ParameterInHeader}},
		Services: []api.Service{{
			Name: "pets",
			Operations: []api.Operation{{
				Name:        "getPet",
				Method:      "get",
				Path:        "/pets/{petId}",
				Tags:        []string{"pets"},
				Deprecated:  true,
				SideEffects: []api.SideEffect{api.SideEffectReadOnly},
				Parameters: []api.Parameter{{
					Name: "petId", In: api.ParameterInPath, Required: true, Schema: api.StringSchema(),
				}},
				Response: &api.Schema{
					Type:    api.TypeObject,
					Pattern: "^[a-z]+$",
					Properties: []api.Property{
						{Name: "name", Schema: api.StringSchema(), Required: true},
						{Name: "bornOn", Schema: &api.Schema{Type: api.TypeString, Format: "date-time"}},
					},
				},
				Responses: []api.Response{
					{Status: "200", Description: "The pet"},
					{Status: "404", Description: "Not found"},
				},
			}},
		}},
	}
	normalized, err := described.Normalize()
	require.NoError(t, err)

	rendered, err := Render(normalized, RenderOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, rendered.Warnings)
	for _, expectation := range []string{
		"request path", "transport method", "folded into its request message", "responses by status",
		"security", "deprecated", "bounds, lengths, patterns",
	} {
		_, found := warnsAbout(rendered.Warnings, expectation)
		assert.True(t, found, "the loss of %q has to be reported, not silently dropped; warnings: %v", expectation, rendered.Warnings)
	}
	// A format protobuf has no type for is a loss too: a caller that reads the
	// contract has to know the calendar was dropped.
	_, found := warnsAbout(rendered.Warnings, "date-time")
	assert.True(t, found, "warnings: %v", rendered.Warnings)

	// The parameters did not vanish: protobuf gives an operation one request value,
	// so the path parameter is a member of it.
	source := renderedSource(t, rendered)
	assert.Contains(t, source, `optional string pet_id = 1 [json_name = "petId"];`)

	// Deprecation is written in protobuf's own spelling, even though this package's
	// reader does not read it back.
	assert.Contains(t, source, "option deprecated = true;")
}

func TestRenderedContractHoldsAValueProtobufHasNoMessageFor(t *testing.T) {
	// A response that is an array is a legitimate description and not a legal rpc
	// signature, so the value is wrapped rather than dropped: an operation that
	// answers a list is still an operation that answers.
	described := api.API{
		ID: "shop", Name: "shop", Format: "openapi",
		Services: []api.Service{{
			Name: "items",
			Operations: []api.Operation{{
				Name:     "listItems",
				Response: api.ArraySchema(api.ObjectSchema(api.Property{Name: "sku", Schema: api.StringSchema()})),
			}},
		}},
	}
	normalized, err := described.Normalize()
	require.NoError(t, err)

	rendered, err := Render(normalized, RenderOptions{})
	require.NoError(t, err)
	source := renderedSource(t, rendered)
	assert.Contains(t, source, "message ListItemsResponse {")
	assert.Contains(t, source, "repeated ListItemsResponse.Items items = 1 [json_name = \"items\"];")

	reparsed, _, err := NewDescriptor(Descriptor{APIID: "shop"}).FromDescriptorSet(descriptorSetFile(t, rendered))
	require.NoError(t, err)
	operation, ok := reparsed.Operation("shop/items/listItems")
	require.True(t, ok)
	items, ok := operation.Response.Property("items")
	require.True(t, ok)
	assert.Equal(t, api.TypeArray, items.Schema.Type)
	require.NotNil(t, items.Schema.Items)
	sku, ok := items.Schema.Items.Property("sku")
	require.True(t, ok)
	assert.Equal(t, api.TypeString, sku.Schema.Type)
}

func TestRenderedContractDeclaresAReferencedShapeOnce(t *testing.T) {
	// Two operations that name the same shape have to name one message: two
	// messages with the same content and different names would describe the same
	// value twice and disagree the moment one of them changed.
	shared := api.ObjectSchema(
		api.Property{Name: "sku", Schema: api.StringSchema(), Required: true},
		api.Property{Name: "count", Schema: &api.Schema{Type: api.TypeInteger}, Required: true},
	)
	shared.Ref = "stock.v1.Item"
	described := api.API{
		ID: "stock", Name: "stock", Format: Format,
		Services: []api.Service{{
			Name: "stock.v1.StockService",
			Operations: []api.Operation{
				{Name: "Get", Request: shared.Clone(), Response: shared.Clone()},
				{Name: "Check", Request: shared.Clone(), Response: shared.Clone()},
			},
		}},
	}
	normalized, err := described.Normalize()
	require.NoError(t, err)

	rendered, err := Render(normalized, RenderOptions{})
	require.NoError(t, err)
	source := renderedSource(t, rendered)
	assert.Equal(t, 1, strings.Count(source, "message Item {"), "the shape is declared once:\n%s", source)
	assert.Contains(t, source, "rpc Get(Item) returns (Item);")
	assert.Contains(t, source, "rpc Check(Item) returns (Item);")

	reparsed, _, err := NewDescriptor(Descriptor{}).FromDescriptorSet(descriptorSetFile(t, rendered))
	require.NoError(t, err)
	for _, operation := range reparsed.Operations() {
		require.NotNil(t, operation.Request)
		assert.Equal(t, "stock.v1.Item", operation.Request.Ref)
	}
}

func TestRenderedContractDeclaresAMapAsAMap(t *testing.T) {
	// A description's free-form object with a declared value shape is a protobuf
	// map, and rendering it as one is what keeps the keys and the values apart.
	described := api.API{
		ID: "labels", Name: "labels", Format: Format,
		Services: []api.Service{{
			Name: "labels.v1.LabelService",
			Operations: []api.Operation{{
				Name: "Attach",
				Request: api.ObjectSchema(api.Property{
					Name:   "labels",
					Schema: &api.Schema{Type: api.TypeObject, AdditionalProperties: api.StringSchema()},
				}),
			}},
		}},
	}
	normalized, err := described.Normalize()
	require.NoError(t, err)

	rendered, err := Render(normalized, RenderOptions{})
	require.NoError(t, err)
	assert.Contains(t, renderedSource(t, rendered), `map<string, string> labels = 1 [json_name = "labels"];`)

	reparsed, _, err := NewDescriptor(Descriptor{APIID: "labels"}).FromDescriptorSet(descriptorSetFile(t, rendered))
	require.NoError(t, err)
	operation, ok := reparsed.Operation("labels/labels.v1.LabelService/Attach")
	require.True(t, ok)
	labels, ok := operation.Request.Property("labels")
	require.True(t, ok)
	require.NotNil(t, labels.Schema.AdditionalProperties)
	assert.Equal(t, api.TypeString, labels.Schema.AdditionalProperties.Type)
}

func TestRenderedContractKeepsPackagesApart(t *testing.T) {
	// One file declares one package, and a service's package is part of its name. An
	// API that describes services from two contracts therefore needs two files: one
	// file would have to rename one of them.
	described := api.API{
		ID: "merged", Name: "merged", Format: Format,
		Services: []api.Service{
			{Name: "alpha.v1.AlphaService", Operations: []api.Operation{{Name: "Do"}}},
			{Name: "beta.v1.BetaService", Operations: []api.Operation{{Name: "Do"}}},
		},
	}
	normalized, err := described.Normalize()
	require.NoError(t, err)

	rendered, err := Render(normalized, RenderOptions{})
	require.NoError(t, err)
	require.Len(t, rendered.Files, 3, "two contracts and one descriptor set")

	sources := make([]string, 0, 2)
	for _, file := range rendered.Files {
		if file.MediaType == MediaTypeContract {
			sources = append(sources, string(file.Content))
		}
	}
	assert.Contains(t, sources[0], "package alpha.v1;")
	assert.Contains(t, sources[1], "package beta.v1;")

	reparsed, _, err := NewDescriptor(Descriptor{}).FromDescriptorSet(descriptorSetFile(t, rendered))
	require.NoError(t, err)
	names := make([]string, 0, len(reparsed.Services))
	for _, service := range reparsed.Services {
		names = append(names, service.Name)
	}
	assert.ElementsMatch(t, []string{"alpha.v1.AlphaService", "beta.v1.BetaService"}, names)
}

func TestRenderRejectsADescriptionWithNothingToDeclare(t *testing.T) {
	_, err := Render(api.API{ID: "empty", Name: "empty", Format: Format}, RenderOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
}

func TestRenderedContractDoesNotPutAParameterIntoASharedMessage(t *testing.T) {
	// A parameter belongs to the operation that declares it. Folding one into a
	// message the description names would hand it to every other operation that
	// names the same shape, and a reader of the contract could not tell it had
	// happened: the field looks like a member of the value.
	shared := api.ObjectSchema(api.Property{Name: "sku", Schema: api.StringSchema()})
	shared.Ref = "stock.v1.Item"
	described := api.API{
		ID: "stock", Name: "stock", Format: Format,
		Services: []api.Service{{
			Name: "stock.v1.StockService",
			Operations: []api.Operation{
				{
					Name:       "Reserve",
					Request:    shared.Clone(),
					Parameters: []api.Parameter{{Name: "warehouse", In: api.ParameterInQuery, Schema: api.StringSchema()}},
				},
				{Name: "Get", Request: shared.Clone()},
			},
		}},
	}
	normalized, err := described.Normalize()
	require.NoError(t, err)

	rendered, err := Render(normalized, RenderOptions{})
	require.NoError(t, err)
	source := renderedSource(t, rendered)
	// The shared message keeps exactly the member the description gave it.
	assert.Contains(t, source, "message Item {\n  string sku = 1 [json_name = \"sku\"];\n}")
	assert.Contains(t, source, "rpc Get(Item) returns (GetResponse);")
	assert.Contains(t, source, "rpc Reserve(ReserveRequest) returns (ReserveResponse);")
	assert.Contains(t, source, "Item request = 1 [json_name = \"request\"];")
	assert.Contains(t, source, "string warehouse = 2 [json_name = \"warehouse\"];")
}

func TestRenderPublishesTheArtifactTheCallerAskedFor(t *testing.T) {
	// A consumer that feeds a parser wants the compiled contract, and marking the
	// source as primary would tell it to start from a file it cannot use.
	described := fixtureDescription(t)
	rendered, err := Render(described, RenderOptions{MediaType: MediaTypeDescriptorSet})
	require.NoError(t, err)

	assert.Equal(t, MediaTypeDescriptorSet, rendered.MediaType)
	assert.Equal(t, FileExtensionDescriptorSet, rendered.FileExtension)
	assert.False(t, rendered.Files[0].Primary)
	assert.True(t, rendered.Files[1].Primary)

	_, err = Render(described, RenderOptions{Options: map[string]string{"include-descriptor-set": "false"}})
	require.NoError(t, err)
	encoded, err := RenderDescriptorSet(described, RenderOptions{Options: map[string]string{"include-descriptor-set": "false"}})
	require.NoError(t, err, "asking for the descriptors directly is not a conflict with not publishing them")
	assert.NotEmpty(t, encoded)
}

func TestRenderEmitsTheDescriptorSetOnRequest(t *testing.T) {
	described := fixtureDescription(t)

	rendered, err := Render(described, RenderOptions{})
	require.NoError(t, err)
	require.Len(t, rendered.Files, 2)
	assert.Equal(t, MediaTypeDescriptorSet, rendered.Files[1].MediaType)
	assert.Equal(t, "contract.pb", rendered.Files[1].Name)
	assert.Equal(t, FileExtensionContract, rendered.FileExtension)

	encoded, err := RenderDescriptorSet(described, RenderOptions{})
	require.NoError(t, err)
	assert.Equal(t, rendered.Files[1].Content, encoded, "the two ways to ask for the descriptors answer the same bytes")

	compiled := &descriptorpb.FileDescriptorSet{}
	require.NoError(t, proto.Unmarshal(encoded, compiled))
	require.Len(t, compiled.File, 1)
	assert.Equal(t, "contract.proto", compiled.File[0].GetName())
	assert.NotEmpty(t, compiled.File[0].GetSourceCodeInfo().GetLocation(),
		"the descriptor set carries its source info, so the comments and annotations survive without a registry")
}

func TestRenderNamesTheContractAfterTheCallersChoice(t *testing.T) {
	described := fixtureDescription(t)
	rendered, err := Render(described, RenderOptions{Options: map[string]string{
		"filename":   "pets.proto",
		"go-package": "example.test/pets/v1;petsv1",
	}})
	require.NoError(t, err)
	assert.Equal(t, "pets.proto", rendered.Files[0].Name)
	assert.Equal(t, "pets.pb", rendered.Files[1].Name)
	assert.Contains(t, string(rendered.Files[0].Content), `option go_package = "example.test/pets/v1;petsv1";`)
}

// descriptorSetFromSource compiles a rendered contract independently of the compile
// the renderer performed, and returns it in the form a parser is handed.
func descriptorSetFromSource(t *testing.T, rendered Rendered) []byte {
	t.Helper()
	compiled := compileRendered(t, rendered)
	encoded, err := proto.Marshal(&descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(compiled)},
	})
	require.NoError(t, err)
	return encoded
}

// descriptorSetFile returns the compiled form of a rendered contract, asking for
// it explicitly so a test does not depend on the artifact being on by default.
func descriptorSetFile(t *testing.T, rendered Rendered) []byte {
	t.Helper()
	for _, file := range rendered.Files {
		if file.MediaType == MediaTypeDescriptorSet {
			return file.Content
		}
	}
	t.Fatal("the rendered contract carries no descriptor set")
	return nil
}

// warnsAbout reports whether a warning mentions a fragment, and answers with it so
// a failing assertion prints what was said instead of only what was not.
func warnsAbout(warnings []string, fragment string) (string, bool) {
	for _, warning := range warnings {
		if strings.Contains(warning, fragment) {
			return warning, true
		}
	}
	return "", false
}

func lastSegment(name string) string {
	index := strings.LastIndex(name, ".")
	return name[index+1:]
}
