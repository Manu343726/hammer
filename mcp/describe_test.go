package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	api "github.com/Manu343726/hammer"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests run against a real Model Context Protocol server over real HTTP, so
// the description is produced the way a deployment would produce it: by speaking
// the protocol to a server that serves it.

type testTool struct {
	name        string
	description string
	readOnly    bool
	destructive bool
	arguments   map[string]any
	result      any
	answer      string
}

// startServer runs a real MCP server with the given tools, reachable over
// Streamable HTTP, and returns its endpoint.
func startServer(t *testing.T, tools ...testTool) string {
	t.Helper()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "petstore", Version: "1.2.3"}, nil)
	for _, tool := range tools {
		tool := tool
		annotations := &sdkmcp.ToolAnnotations{ReadOnlyHint: tool.readOnly}
		if tool.destructive {
			destructive := true
			annotations.DestructiveHint = &destructive
		}
		declared := &sdkmcp.Tool{
			Name:         tool.name,
			Description:  tool.description,
			InputSchema:  tool.arguments,
			OutputSchema: tool.result,
			Annotations:  annotations,
		}
		server.AddTool(declared, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: tool.answer}},
			}, nil
		})
	}
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)
	endpoint := httptest.NewServer(handler)
	t.Cleanup(endpoint.Close)
	return endpoint.URL
}

func petArguments() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"petId": map[string]any{"type": "string", "description": "Pet identifier"},
		},
		"required": []any{"petId"},
	}
}

func petResult() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}
}

func TestDescribeReadsALiveServer(t *testing.T) {
	endpoint := startServer(t,
		testTool{
			name:        "get_pet",
			description: "Fetch one pet.\nReturns the pet as JSON.",
			readOnly:    true,
			arguments:   petArguments(),
			result:      petResult(),
			answer:      `{"name":"rex"}`,
		},
		testTool{
			name:        "delete_pet",
			description: "Remove a pet",
			destructive: true,
			arguments:   petArguments(),
			answer:      "gone",
		},
	)

	described, warnings, err := Describe(context.Background(), endpoint, DescribeOptions{APIID: "petstore"})
	require.NoError(t, err)
	assert.Empty(t, warnings, "a well-described server produces no findings")

	assert.Equal(t, Format, described.Format)
	assert.Equal(t, Transport, described.Transport,
		"a description knows how its operations are called, so a catalog needs no guess")
	assert.Equal(t, SourceKind, described.Source.Kind)
	assert.Equal(t, endpoint, described.Source.Location)
	assert.Equal(t, "petstore", described.Title, "the server's own identity is used")
	require.Len(t, described.DeclaredServers, 1)
	assert.Equal(t, endpoint, described.DeclaredServers[0].URL)

	require.Len(t, described.Services, 1)
	service := described.Services[0]
	require.Len(t, service.Operations, 2, "operations are in a stable order")
	assert.Equal(t, "delete_pet", service.Operations[0].Name, "the list is sorted, so two reads are equal")
	assert.Equal(t, "get_pet", service.Operations[1].Name)

	fetch, found := described.Operation("petstore/mcp/get_pet")
	require.True(t, found)
	assert.Equal(t, ToolCallMethod, fetch.Method, "an MCP operation is called by the protocol's method")
	assert.Equal(t, "Fetch one pet.", fetch.Summary, "the first line is what a model reads first")
	assert.Contains(t, fetch.Description, "Returns the pet as JSON.")
	assert.Equal(t, []api.SideEffect{api.SideEffectReadOnly}, fetch.SideEffects)

	// The argument schema arrives as a description, not as a document: a caller
	// reads the parameters by name.
	require.NotNil(t, fetch.Request)
	assert.Equal(t, api.TypeObject, fetch.Request.Type)
	petID, ok := fetch.Request.Property("petId")
	require.True(t, ok)
	assert.Equal(t, api.TypeString, petID.Schema.Type)
	assert.Equal(t, "Pet identifier", petID.Schema.Description)
	assert.Equal(t, []string{"petId"}, fetch.Request.Required, "a required argument stays required")

	require.NotNil(t, fetch.Response)
	name, ok := fetch.Response.Property("name")
	require.True(t, ok)
	assert.Equal(t, api.TypeString, name.Schema.Type)

	remove, found := described.Operation("petstore/mcp/delete_pet")
	require.True(t, found)
	assert.Equal(t, []api.SideEffect{api.SideEffectIrreversible}, remove.SideEffects,
		"a destructive hint is a consequence the description carries")
	// A tool that declares no result shape still gets the protocol's envelope, so
	// a caller is told what comes back rather than nothing at all.
	content, ok := remove.Response.Property("content")
	require.True(t, ok)
	assert.Equal(t, api.TypeArray, content.Schema.Type)
}

func TestDescribeNamesTheAPIFromTheEndpoint(t *testing.T) {
	endpoint := startServer(t, testTool{name: "ping", description: "Ping", arguments: petArguments(), answer: "pong"})
	described, _, err := Describe(context.Background(), endpoint, DescribeOptions{})
	require.NoError(t, err)
	assert.Equal(t, apiSlugFromEndpoint(endpoint), described.ID)
	assert.NotEqual(t, "mcp-server", described.ID, "an endpoint is enough to name a server")
	assert.Equal(t, "petstore", described.Title, "the server's own identity is preferred")
}

func TestDescribeHonoursTheCallersIdentifierAndService(t *testing.T) {
	endpoint := startServer(t, testTool{name: "ping", description: "Ping", arguments: petArguments(), answer: "pong"})
	described, _, err := Describe(context.Background(), endpoint, DescribeOptions{
		APIID:      "shop-tools",
		ServerName: "shop",
	})
	require.NoError(t, err)
	assert.Equal(t, "shop-tools", described.ID)
	require.Len(t, described.Services, 1)
	assert.Equal(t, "shop", described.Services[0].Name)

	_, found := described.Operation("shop-tools/shop/ping")
	assert.True(t, found, "the operation belongs to the named service")
}

func TestDescribeRestrictsToTheNamedTools(t *testing.T) {
	endpoint := startServer(t,
		testTool{name: "one", description: "First", arguments: petArguments(), answer: "1"},
		testTool{name: "two", description: "Second", arguments: petArguments(), answer: "2"},
	)
	described, _, err := Describe(context.Background(), endpoint, DescribeOptions{Tools: []string{"two"}})
	require.NoError(t, err)
	require.Len(t, described.Services, 1)
	require.Len(t, described.Services[0].Operations, 1)
	assert.Equal(t, "two", described.Services[0].Operations[0].Name)
}

func TestDescribeReportsAnUndescribedServer(t *testing.T) {
	// A server that declares no tools describes nothing, and a caller is told so
	// rather than handed an empty description.
	empty := startServer(t)
	_, _, err := Describe(context.Background(), empty, DescribeOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "offers no tool")
}

func TestDescribeWarnsAboutUndocumentedTools(t *testing.T) {
	// The protocol requires a tool's input schema to be an object, so the gap this
	// reports is prose, not shape.
	endpoint := startServer(t, testTool{name: "bare", arguments: map[string]any{"type": "object"}, answer: "ok"})
	described, warnings, err := Describe(context.Background(), endpoint, DescribeOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, described.Services[0].Operations)

	joined := ""
	for _, warning := range warnings {
		joined += warning + "\n"
	}
	assert.Contains(t, joined, `tool "bare" declares no description`,
		"a tool with no prose is described, and the gap is reported")
}

func TestDescribeRefusesAnEmptyEndpoint(t *testing.T) {
	_, _, err := Describe(context.Background(), "  ", DescribeOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
}

func TestDescribeReportsAnUnreachableServer(t *testing.T) {
	unreachable := httptest.NewServer(http.NotFoundHandler())
	closed := unreachable.URL
	unreachable.Close()

	_, _, err := Describe(context.Background(), closed, DescribeOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindUnavailable, api.KindOf(err),
		"a server that cannot be reached is unavailable, which says what to fix")
}

func TestDescribeFitsTheCompositionShape(t *testing.T) {
	// A host reads a live endpoint through one interface, whatever the format, so
	// this implementation satisfies the same shape a contract reader does.
	var describer interface {
		Describe(context.Context, api.DescribeRequest) (api.DescribeResult, error)
	} = DescribeOptions{}
	endpoint := startServer(t, testTool{
		name:        "get_pet",
		description: "Fetch one pet",
		readOnly:    true,
		arguments:   petArguments(),
		answer:      "rex",
	})

	result, err := describer.Describe(context.Background(), api.DescribeRequest{
		BaseURL: endpoint,
		APIID:   "shop",
		Format:  Format,
	})
	require.NoError(t, err)
	assert.Equal(t, "shop", result.API.ID)
	assert.Equal(t, endpoint, result.API.Source.Location)
	require.Len(t, result.Formats, 1)
	assert.Equal(t, string(Format), result.Formats[0].ID)

	operation, found := result.API.Operation("shop/mcp/get_pet")
	require.True(t, found)
	assert.Equal(t, ToolCallMethod, operation.Method)
	assert.Equal(t, []api.SideEffect{api.SideEffectReadOnly}, operation.SideEffects,
		"a published manifest carries what calling a tool does, so a deployment can classify it")
}

func TestDescribeToolReportsAToolWithNoInputSchema(t *testing.T) {
	// A manifest that declares no argument shape describes an operation whose
	// arguments are unconstrained, and the gap is reported rather than filled in
	// with an assumption.
	operation, warnings := describeTool(&sdkmcp.Tool{Name: "bare", Description: "Does something"})
	assert.Nil(t, operation.Request)
	joined := ""
	for _, warning := range warnings {
		joined += warning + "\n"
	}
	assert.Contains(t, joined, "declares no input schema")
	assert.Empty(t, operation.SideEffects, "a tool that says nothing about its consequences has declared none")
	require.NotNil(t, operation.Response)
	failed, ok := operation.Response.Property("isError")
	require.True(t, ok, "a tool with no declared result still returns the protocol's envelope")
	assert.Equal(t, api.TypeBoolean, failed.Schema.Type)
}

func TestApiSlugFromEndpointIsStable(t *testing.T) {
	// A port changes every run; an identifier built from one would replace a
	// registration instead of refreshing it.
	assert.Equal(t, "petstore.example.com", apiSlugFromEndpoint("https://petstore.example.com/mcp/"))
	assert.Equal(t, "127.0.0.1", apiSlugFromEndpoint("http://127.0.0.1:39485"))
	assert.Equal(t, "mcp-server", apiSlugFromEndpoint("http://:"))
	assert.Equal(t, "petstore", apiSlugFromEndpoint("petstore"))
}

func TestManifestIsTheDocumentAReaderInspects(t *testing.T) {
	endpoint := startServer(t, testTool{
		name:        "get_pet",
		description: "Fetch one pet",
		readOnly:    true,
		arguments:   petArguments(),
		result:      petResult(),
		answer:      "rex",
	})
	described, _, err := Describe(context.Background(), endpoint, DescribeOptions{APIID: "shop"})
	require.NoError(t, err)

	document, err := Manifest(described, RenderOptions{})
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(document, &manifest))
	assert.Equal(t, "2025-06-18", manifest["protocolVersion"])

	server, ok := manifest["server"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "shop", server["name"])

	tools, ok := manifest["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "mcp__get_pet", tool["name"],
		"the manifest names tools the way a client sees them")
	assert.Equal(t, "Fetch one pet", tool["description"])
	assert.Equal(t, "shop/mcp/get_pet", tool["x-toolbox-operation"],
		"the document names the operation it came from, so a reader can find it again")
	assert.Equal(t, true, tool["annotations"].(map[string]any)["readOnlyHint"],
		"a manifest states what calling a tool does, in the protocol's own vocabulary")
	assert.Contains(t, tool, "outputSchema")
	assert.Contains(t, tool, "inputSchema")
}

func TestManifestCarriesTheTranslationFindings(t *testing.T) {
	// A tool the translation could not represent is written into the document as a
	// finding, so a reader learns it from the manifest instead of from what is
	// missing.
	described := describedShop(t)
	described.Services[0].Operations[0].Streaming = api.Streaming{Client: true}
	document, err := Manifest(described, RenderOptions{})
	require.NoError(t, err)

	var manifest map[string]any
	require.NoError(t, json.Unmarshal(document, &manifest))
	warnings, ok := manifest["x-toolbox-warnings"].([]any)
	require.True(t, ok)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "is streaming")
}

func TestDescriptorsDescribeWhatThisImplementationClaims(t *testing.T) {
	assert.Equal(t, []api.Format{Format}, Formats())
	assert.Equal(t, []string{string(Format)}, Targets())
	assert.True(t, HandlesFormat("MCP"))
	assert.True(t, HandlesTarget(" mcp "))
	assert.False(t, HandlesFormat("openapi"), "a target this implementation does not produce is not claimed")

	format := FormatDescriptor()
	assert.Equal(t, string(Format), format.ID)
	assert.NotEmpty(t, format.Description)
	target := TargetDescriptor()
	assert.Equal(t, string(Format), target.ID)
	assert.Equal(t, "2025-06-18", target.SpecificationVersion)

	transports := TransportDescriptors()
	require.Len(t, transports, 1)
	assert.Equal(t, string(Transport), transports[0].ID)
}

func TestDescriptionSurvivesTheMcpRoundTrip(t *testing.T) {
	// A server is read, the description is published as a tool manifest, and the
	// manifest is read back. What this library needs to expose an operation has to
	// survive: the tool name, the arguments, and the consequences. Without that,
	// publishing an API in MCP loses what a policy depends on.
	endpoint := startServer(t, testTool{
		name:        "get_pet",
		description: "Fetch one pet",
		readOnly:    true,
		arguments:   petArguments(),
		result:      petResult(),
		answer:      "rex",
	})
	read, _, err := Describe(context.Background(), endpoint, DescribeOptions{APIID: "shop"})
	require.NoError(t, err)

	document, err := Manifest(read, RenderOptions{})
	require.NoError(t, err)
	republished, _, err := DescribeDocument(document, DescribeOptions{})
	require.NoError(t, err)

	first, found := read.Operation("shop/mcp/get_pet")
	require.True(t, found)
	second, found := republished.Operation("shop/mcp/get_pet")
	require.True(t, found, "the operation keeps its identity through the manifest")

	assert.Equal(t, first.Name, second.Name)
	assert.Equal(t, first.Method, second.Method)
	assert.Equal(t, first.Summary, second.Summary)
	assert.Equal(t, first.SideEffects, second.SideEffects, "declared consequences survive")

	firstID, ok := first.Request.Property("petId")
	require.True(t, ok)
	secondID, ok := second.Request.Property("petId")
	require.True(t, ok)
	assert.Equal(t, firstID.Schema.Type, secondID.Schema.Type)
	assert.Equal(t, first.Request.Required, second.Request.Required,
		"a required argument stays required, or a policy would offer a tool that cannot be called")
}

func TestDescribeDocumentReadsAPublishedManifest(t *testing.T) {
	// A manifest is a document, so the same reader takes one as well as a live
	// endpoint. That is what lets a deployment register an API published earlier,
	// or carried in a configuration file, without a server running.
	document := map[string]any{
		"protocolVersion": "2025-06-18",
		"server":          map[string]any{"name": "shop", "description": "The shop tools"},
		"tools": []any{map[string]any{
			"name":        "mcp__get_pet",
			"description": "Fetch one pet",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"petId": map[string]any{"type": "string"}},
				"required":   []any{"petId"},
			},
			"x-toolbox-operation": "shop/mcp/get_pet",
			"annotations":         map[string]any{"readOnlyHint": true},
		}},
	}
	encoded, err := json.Marshal(document)
	require.NoError(t, err)

	described, warnings, err := DescribeDocument(encoded, DescribeOptions{APIID: "shop"})
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, "shop", described.ID)
	assert.Equal(t, "The shop tools", described.Description)

	operation, found := described.Operation("shop/mcp/get_pet")
	require.True(t, found)
	assert.Equal(t, "get_pet", operation.Name, "the manifest's tool name is reduced to the operation's")
	assert.Equal(t, ToolCallMethod, operation.Method)
	assert.Equal(t, []api.SideEffect{api.SideEffectReadOnly}, operation.SideEffects,
		"a published manifest carries what calling a tool does, so a deployment can classify it")

	petID, ok := operation.Request.Property("petId")
	require.True(t, ok)
	assert.Equal(t, api.TypeString, petID.Schema.Type)
}

func TestDescribeDocumentRecoversTheOperationNameFromTheToolName(t *testing.T) {
	// A manifest written by another tool names its tools its own way and records no
	// operation identifier. The operation is still recovered from the name, so the
	// API a deployment registered is the API it published.
	encoded := []byte(`{"server":{"name":"shop"},"tools":[{"name":"mcp__get_pet","description":"Fetch"}]}`)
	described, _, err := DescribeDocument(encoded, DescribeOptions{})
	require.NoError(t, err)
	_, found := described.Operation("shop/mcp/get_pet")
	assert.True(t, found, "the service segment is stripped from the tool name")
}

func TestDescribeDocumentKeepsTheManifestFindings(t *testing.T) {
	// A manifest states what its writer could not represent. Reading it back without
	// those findings would lose the difference between a tool that is absent and a
	// tool nobody could publish.
	encoded := []byte(`{
		"server": {"name": "shop"},
		"tools": [{"name": "ping", "description": "Ping"}],
		"x-toolbox-warnings": ["operation contract/stream/list is streaming and is not offered as a tool"]
	}`)
	_, warnings, err := DescribeDocument(encoded, DescribeOptions{APIID: "shop"})
	require.NoError(t, err)
	assert.Equal(t, []string{"operation contract/stream/list is streaming and is not offered as a tool"}, warnings)
}

func TestDescribeDocumentRefusesSomethingItCannotRead(t *testing.T) {
	_, _, err := DescribeDocument([]byte("{not json"), DescribeOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))

	_, _, err = DescribeDocument([]byte(`{"tools":[]}`), DescribeOptions{})
	require.Error(t, err, "a manifest with no tool describes no API")
}
