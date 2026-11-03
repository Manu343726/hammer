package mcp

import (
	"context"
	"encoding/json"
	"testing"

	api "github.com/Manu343726/hammer"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests call a real tool of a real server, so the invoker is exercised the
// way a deployment exercises it: over the protocol, against a server that answers.

func TestInvokerCallsATool(t *testing.T) {
	endpoint := startServer(t, testTool{
		name:        "get_pet",
		description: "Fetch one pet",
		arguments:   petArguments(),
		answer:      `{"name":"rex"}`,
	})
	described, _, err := Describe(context.Background(), endpoint, DescribeOptions{APIID: "petstore"})
	require.NoError(t, err)
	operation, found := described.Operation("petstore/mcp/get_pet")
	require.True(t, found)

	invoker := NewInvoker(InvokerOptions{})
	t.Cleanup(func() { _ = invoker.Close() })
	result, err := invoker.Invoke(context.Background(), api.Call{
		Server:    api.Server{ID: "petstore", Name: "Pet store", BaseURL: endpoint, Transport: Transport},
		API:       described,
		Operation: operation,
		Arguments: json.RawMessage(`{"petId":"7"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, 200, result.Status)
	assert.Equal(t, "application/json", result.ContentType)
	assert.Contains(t, string(result.Body), "rex", "the server's own result is what comes back")

	// The whole protocol result is returned, so nothing a server sent is lost.
	var payload map[string]any
	require.NoError(t, json.Unmarshal(result.Body, &payload))
	content, ok := payload["content"].([]any)
	require.True(t, ok, "the server's own content comes back: %s", string(result.Body))
	require.NotEmpty(t, content)
	first, ok := content[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "text", first["type"])
}

func TestInvokerReusesOneSessionPerEndpoint(t *testing.T) {
	endpoint := startServer(t, testTool{
		name:        "get_pet",
		description: "Fetch one pet",
		arguments:   petArguments(),
		answer:      "rex",
	})
	described, _, err := Describe(context.Background(), endpoint, DescribeOptions{APIID: "petstore"})
	require.NoError(t, err)
	operation, _ := described.Operation("petstore/mcp/get_pet")

	invoker := NewInvoker(InvokerOptions{})
	t.Cleanup(func() { _ = invoker.Close() })
	call := api.Call{
		Server:    api.Server{ID: "petstore", Name: "Pet store", BaseURL: endpoint, Transport: Transport},
		API:       described,
		Operation: operation,
		Arguments: json.RawMessage(`{"petId":"7"}`),
	}
	first, err := invoker.Invoke(context.Background(), call)
	require.NoError(t, err)
	second, err := invoker.Invoke(context.Background(), call)
	require.NoError(t, err)
	assert.JSONEq(t, string(first.Body), string(second.Body))

	invoker.mu.Lock()
	sessions := len(invoker.sessions)
	invoker.mu.Unlock()
	assert.Equal(t, 1, sessions, "one endpoint is one negotiated conversation, not one per call")
}

func TestInvokerRefusesArgumentsThatAreNotAnObject(t *testing.T) {
	endpoint := startServer(t, testTool{name: "get_pet", description: "Fetch", arguments: petArguments(), answer: "rex"})
	described, _, err := Describe(context.Background(), endpoint, DescribeOptions{APIID: "petstore"})
	require.NoError(t, err)
	operation, _ := described.Operation("petstore/mcp/get_pet")

	invoker := NewInvoker(InvokerOptions{})
	t.Cleanup(func() { _ = invoker.Close() })
	_, err = invoker.Invoke(context.Background(), api.Call{
		Server:    api.Server{ID: "petstore", Name: "Pet store", BaseURL: endpoint},
		API:       described,
		Operation: operation,
		Arguments: json.RawMessage(`["not","an","object"]`),
	})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "JSON object")
}

func TestInvokerRefusesAStreamingOperation(t *testing.T) {
	_, err := NewInvoker(InvokerOptions{}).Invoke(context.Background(), api.Call{
		Server:    api.Server{ID: "petstore", Name: "Pet store", BaseURL: "http://petstore.test"},
		Operation: api.Operation{ID: "petstore/mcp/stream", Name: "stream", Streaming: api.Streaming{Server: true}},
	})
	require.Error(t, err)
	assert.Equal(t, api.KindUnsupported, api.KindOf(err),
		"a tool call is one request and one response")
}

func TestInvokerRequiresAnEndpoint(t *testing.T) {
	_, err := NewInvoker(InvokerOptions{}).Invoke(context.Background(), api.Call{
		Server:    api.Server{ID: "petstore", Name: "Pet store"},
		Operation: api.Operation{ID: "petstore/mcp/ping", Name: "ping"},
	})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
}

func TestInvokerPresentsTheIdentityItWasGiven(t *testing.T) {
	// A deployment may need to be told apart in a server's own logs, so the identity
	// the caller supplied is what the handshake carries — and the package's own name
	// is only the default.
	identity := &sdkmcp.Implementation{Name: "shop-gateway", Version: "9.9.9"}
	invoker := NewInvoker(InvokerOptions{ClientInfo: identity})
	assert.Same(t, identity, invoker.identity)

	fallback := NewInvoker(InvokerOptions{})
	assert.Equal(t, ClientName, fallback.identity.Name)
	assert.Equal(t, Version, fallback.identity.Version)
}
