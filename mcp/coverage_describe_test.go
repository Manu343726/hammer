package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	api "github.com/Manu343726/hammer"
	mcp "github.com/Manu343726/hammer/mcp"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover what the readers do with a server that describes itself
// badly, not at all, or with a constraint the standard model cannot hold. A
// description is what a policy is written against, so every gap here is a place
// where a tool could otherwise arrive looking safer than it is.

// covTool is one tool a coverage server offers.
type covTool struct {
	name        string
	description string
	arguments   any
	result      any
	readOnly    bool
	destructive bool
	idempotent  bool
	answer      string
	fails       bool
}

// covServer runs a real MCP server over Streamable HTTP and returns its endpoint,
// so the reader is exercised the way a deployment exercises it: over the protocol.
func covServer(t *testing.T, tools ...covTool) string {
	t.Helper()
	return covStoppableServer(t, tools...).URL
}

// covStoppableServer is covServer for a test that has to take the server down
// mid-conversation, which is how a transport failure is produced honestly.
func covStoppableServer(t *testing.T, tools ...covTool) *httptest.Server {
	t.Helper()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "petstore", Version: "1.2.3"}, nil)
	for _, tool := range tools {
		tool := tool
		annotations := &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   tool.readOnly,
			IdempotentHint: tool.idempotent,
		}
		if tool.destructive {
			destructive := true
			annotations.DestructiveHint = &destructive
		}
		arguments := tool.arguments
		if arguments == nil {
			// The protocol requires an input schema of type object, and the SDK
			// refuses to register a tool without one; a tool that declares no
			// arguments declares an empty object, not nothing.
			arguments = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		server.AddTool(&sdkmcp.Tool{
			Name:         tool.name,
			Description:  tool.description,
			InputSchema:  arguments,
			OutputSchema: tool.result,
			Annotations:  annotations,
		}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			answer := tool.answer
			if answer == "" {
				answer = "ok"
			}
			return &sdkmcp.CallToolResult{
				IsError: tool.fails,
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: answer}},
			}, nil
		})
	}
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)
	endpoint := httptest.NewServer(handler)
	t.Cleanup(endpoint.Close)
	return endpoint
}

// covPetArguments is an argument schema a tool can carry without findings.
func covPetArguments() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"petId": map[string]any{"type": "string", "description": "Pet identifier"},
		},
		"required": []any{"petId"},
	}
}

// covDescribe reads a live endpoint as an API named petstore.
func covDescribe(t *testing.T, endpoint string) api.API {
	t.Helper()
	described, _, err := mcp.Describe(context.Background(), endpoint, mcp.DescribeOptions{APIID: "petstore"})
	require.NoError(t, err)
	return described
}

// covFailingToolListServer answers the handshake and then refuses the tool list.
// A working server cannot be made to produce that answer through the SDK, so the
// protocol is spoken by hand: the failure is a real one at the wire, not a stub.
func covFailingToolListServer(t *testing.T) string {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if request.Method == "initialize" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{`+
				`"protocolVersion":"2025-06-18","capabilities":{"tools":{}},`+
				`"serverInfo":{"name":"broken","version":"1.0.0"}}}`, request.ID)
			return
		}
		if len(request.ID) == 0 || string(request.ID) == "null" {
			// A notification carries no identifier and expects no answer, so the
			// transport's own "accepted" is the correct reply.
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{`+
			`"code":-32603,"message":"the tool list is unavailable"}}`, request.ID)
	})
	endpoint := httptest.NewServer(handler)
	t.Cleanup(endpoint.Close)
	return endpoint.URL
}

func TestCovDescribeRefusesASchemaTheModelCannotCarry(t *testing.T) {
	// A tool's schema is JSON Schema, which can state a constraint the standard
	// model does not hold: here an array with no item shape. Reading it anyway
	// would describe the argument as unconstrained, which is the unsafe direction
	// for a policy, so the reader refuses the server instead of dropping a fact.
	endpoint := covServer(t, covTool{
		name:        "list_tags",
		description: "List tags",
		arguments: map[string]any{
			"type":       "object",
			"properties": map[string]any{"tags": map[string]any{"type": "array"}},
		},
	})

	_, _, err := mcp.Describe(context.Background(), endpoint, mcp.DescribeOptions{APIID: "petstore"})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "normalize the described API")
}

func TestCovDescribeReportsAServerThatWillNotListItsTools(t *testing.T) {
	// A server that completes the handshake and then refuses the tool list is
	// unavailable, not invalid: the description failed for a reason the operator
	// can fix, and saying so is what points them at the right server.
	_, _, err := mcp.Describe(context.Background(), covFailingToolListServer(t), mcp.DescribeOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindUnavailable, api.KindOf(err))
	assert.Contains(t, err.Error(), "list the tools")
}

func TestCovDescribeCarriesEveryDeclaredConsequenceInOrder(t *testing.T) {
	// The protocol states consequences as three independent hints, and each is a
	// fact a policy filters on. A tool that declares all three must arrive with all
	// three, sorted: an unsorted list would make two reads of one server unequal,
	// and a missing hint would make a consequential tool look harmless.
	endpoint := covServer(t, covTool{
		name:        "refresh",
		description: "Refresh the cache",
		readOnly:    true,
		destructive: true,
		idempotent:  true,
		arguments:   covPetArguments(),
	})

	described := covDescribe(t, endpoint)
	operation, found := described.Operation("petstore/mcp/refresh")
	require.True(t, found)
	assert.Equal(t, []api.SideEffect{
		api.SideEffectIrreversible, api.SideEffectReadOnly, api.SideEffect("repeatable"),
	}, operation.SideEffects, "every declared consequence is carried, in a stable order")
}

func TestCovDescribeOptionsReportsAReadThatFailed(t *testing.T) {
	// The composition shape returns the findings it gathered even when it fails, so
	// a host can show why a registration did not happen. Claiming a format while
	// publishing no description would leave the host with a half-registration.
	unreachable := httptest.NewServer(http.NotFoundHandler())
	closed := unreachable.URL
	unreachable.Close()

	result, err := mcp.DescribeOptions{}.Describe(context.Background(), api.DescribeRequest{BaseURL: closed})
	require.Error(t, err)
	assert.Equal(t, api.KindUnavailable, api.KindOf(err))
	assert.Empty(t, result.Formats, "a read that failed claims no format")
}

func TestCovDescribeOptionsHonoursTheSourceItWasTold(t *testing.T) {
	// How a description was obtained is the host's record, not the reader's: a
	// deployment that fetched the server through its own machinery says so, and the
	// name the host passed in wins over the one the options carried.
	endpoint := covServer(t, covTool{name: "ping", description: "Ping", answer: "pong"})

	result, err := mcp.DescribeOptions{APIID: "petstore"}.Describe(context.Background(), api.DescribeRequest{
		BaseURL: endpoint,
		APIID:   "shop",
		Source:  api.Source{Kind: "fetched-by-the-host"},
	})
	require.NoError(t, err)
	assert.Equal(t, "shop", result.API.ID, "the host's identifier is the one recorded")
	assert.Equal(t, "fetched-by-the-host", result.API.Source.Kind)
	_, found := result.API.Operation("shop/mcp/ping")
	assert.True(t, found, "the operations are re-identified against the host's name")
}

func TestCovDescribeRefusesARequestThatKeepsNoTool(t *testing.T) {
	// A filter that matches nothing describes no API. Returning an empty
	// description would register a server that offers nothing, which a caller
	// cannot tell apart from a server whose tools were all filtered away.
	endpoint := covServer(t, covTool{name: "ping", description: "Ping", answer: "pong"})

	_, _, err := mcp.Describe(context.Background(), endpoint, mcp.DescribeOptions{Tools: []string{"absent"}})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "matched the request")
}

func TestCovSchemaDocumentReadsEveryShapeTheProtocolAllows(t *testing.T) {
	// The protocol leaves a schema as "any value that marshals to valid JSON
	// Schema", so the reader meets an absent schema, a decoded map, raw JSON, a
	// byte slice, and a struct it must encode first. Each must read as the same
	// document: a shape silently read as nil would describe a constrained tool as
	// unconstrained, and one silently read as a map would drop the constraint.
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"petId": map[string]any{"type": "string"}},
	}
	encoded, err := json.Marshal(schema)
	require.NoError(t, err)

	// A struct is how a schema the SDK has already decoded arrives.
	type covStructSchema struct {
		Type string `json:"type"`
	}
	decoded := mcp.CovSchemaDocument(covStructSchema{Type: "object"})
	assert.Equal(t, map[string]any{"type": "object"}, decoded)

	for name, value := range map[string]any{
		"a decoded map": schema,
		"raw JSON":      json.RawMessage(encoded),
		"a byte slice":  encoded,
		"a struct":      covStructSchema{Type: "object"},
		"a pointer":     &covStructSchema{Type: "object"},
	} {
		assert.NotNil(t, mcp.CovSchemaDocument(value), name)
	}

	assert.Nil(t, mcp.CovSchemaDocument(nil), "a tool that declares no schema is unconstrained")
	assert.Nil(t, mcp.CovSchemaDocument(json.RawMessage("{")), "malformed raw JSON is a schema nobody can read")
	assert.Nil(t, mcp.CovSchemaDocument([]byte("{")), "malformed bytes are a schema nobody can read")
	assert.Nil(t, mcp.CovSchemaDocument([]byte(nil)), "an empty document declares no shape")
	assert.Nil(t, mcp.CovSchemaDocument(func() {}), "a value that cannot be encoded is not a schema")
	assert.Nil(t, mcp.CovSchemaDocument("a string"), "a schema that is not an object is not a schema")
}
