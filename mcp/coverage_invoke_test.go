package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	api "github.com/Manu343726/hammer"
	mcp "github.com/Manu343726/hammer/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests call a real tool of a real server. The two failures the protocol
// distinguishes — a conversation that broke and a tool that ran and failed — are
// asserted separately here, because collapsing one into the other changes what a
// caller is told to do next.

// covCall builds the call an invoker receives for one operation of a described API.
func covCall(described api.API, endpoint string, operation api.Operation, arguments string) api.Call {
	return api.Call{
		Server:    api.Server{ID: "petstore", Name: "Pet store", BaseURL: endpoint, Transport: mcp.Transport},
		API:       described,
		Operation: operation,
		Arguments: json.RawMessage(arguments),
	}
}

func TestCovInvokeReturnsAToolsErrorResultAsAResult(t *testing.T) {
	// A tool that ran and failed answers with the protocol's own error result, which
	// is a result. Reporting it as a Go error would discard the server's explanation
	// and leave a caller unable to tell a rejected argument from a dropped
	// connection.
	endpoint := covServer(t, covTool{
		name:        "get_pet",
		description: "Fetch one pet",
		arguments:   covPetArguments(),
		answer:      "no such pet",
		fails:       true,
	})
	described := covDescribe(t, endpoint)
	operation, found := described.Operation("petstore/mcp/get_pet")
	require.True(t, found)

	invoker := mcp.NewInvoker(mcp.InvokerOptions{})
	t.Cleanup(func() { _ = invoker.Close() })

	result, err := invoker.Invoke(context.Background(), covCall(described, endpoint, operation, `{"petId":"7"}`))
	require.NoError(t, err, "a tool's own failure is a result, not a transport failure")
	assert.Equal(t, 200, result.Status)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(result.Body, &payload))
	assert.Equal(t, true, payload["isError"], "the protocol's error marker is carried through")
	content, ok := payload["content"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, content)
	first, ok := content[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "no such pet", first["text"], "the server's own message is not lost")
}

func TestCovInvokeReportsAnUnknownToolAndReconnects(t *testing.T) {
	// A server that refuses one call ends the conversation: keeping it would make
	// every later call inherit the same failure. The next call must therefore
	// reconnect and succeed, which is what proves the bad session was dropped
	// rather than cached.
	endpoint := covServer(t, covTool{
		name:        "get_pet",
		description: "Fetch one pet",
		arguments:   covPetArguments(),
	})
	described := covDescribe(t, endpoint)
	operation, found := described.Operation("petstore/mcp/get_pet")
	require.True(t, found)

	invoker := mcp.NewInvoker(mcp.InvokerOptions{})
	t.Cleanup(func() { _ = invoker.Close() })

	unknown := api.Operation{ID: "petstore/mcp/absent", Name: "absent"}
	_, err := invoker.Invoke(context.Background(), covCall(described, endpoint, unknown, `{}`))
	require.Error(t, err)
	assert.Equal(t, api.KindUnavailable, api.KindOf(err))
	assert.Contains(t, err.Error(), "call petstore/mcp/absent")

	result, err := invoker.Invoke(context.Background(), covCall(described, endpoint, operation, `{"petId":"7"}`))
	require.NoError(t, err, "the failed conversation is dropped, not reused")
	assert.Equal(t, 200, result.Status)
}

func TestCovInvokeDropsOnlyTheConversationThatFailed(t *testing.T) {
	// Sessions are kept per endpoint, so one server going away must not strand the
	// calls to another. Dropping every session on any failure would turn one broken
	// endpoint into an outage of all of them.
	dead := covStoppableServer(t, covTool{
		name:        "get_pet",
		description: "Fetch one pet",
		arguments:   covPetArguments(),
	})
	alive := covServer(t, covTool{
		name:        "get_pet",
		description: "Fetch one pet",
		arguments:   covPetArguments(),
	})
	deadAPI := covDescribe(t, dead.URL)
	aliveAPI := covDescribe(t, alive)
	deadOperation, found := deadAPI.Operation("petstore/mcp/get_pet")
	require.True(t, found)
	aliveOperation, found := aliveAPI.Operation("petstore/mcp/get_pet")
	require.True(t, found)

	invoker := mcp.NewInvoker(mcp.InvokerOptions{})
	t.Cleanup(func() { _ = invoker.Close() })

	_, err := invoker.Invoke(context.Background(), covCall(deadAPI, dead.URL, deadOperation, `{"petId":"1"}`))
	require.NoError(t, err)
	_, err = invoker.Invoke(context.Background(), covCall(aliveAPI, alive, aliveOperation, `{"petId":"2"}`))
	require.NoError(t, err)

	// The server is gone, so the conversation cannot survive the next call.
	dead.Close()
	_, err = invoker.Invoke(context.Background(), covCall(deadAPI, dead.URL, deadOperation, `{"petId":"3"}`))
	require.Error(t, err)
	assert.Equal(t, api.KindUnavailable, api.KindOf(err))

	result, err := invoker.Invoke(context.Background(), covCall(aliveAPI, alive, aliveOperation, `{"petId":"4"}`))
	require.NoError(t, err, "the surviving endpoint keeps its conversation")
	assert.Equal(t, 200, result.Status)
}

func TestCovInvokeRefusesAnOperationThatNamesNoTool(t *testing.T) {
	// The operation's name is the tool name; without one there is nothing to call.
	// Sending an empty name would ask the server for a tool called "" and report the
	// server's refusal as if the description had been right.
	_, err := mcp.NewInvoker(mcp.InvokerOptions{}).Invoke(context.Background(), api.Call{
		Server:    api.Server{ID: "petstore", Name: "Pet store", BaseURL: "http://petstore.test"},
		Operation: api.Operation{ID: "petstore/mcp/unnamed"},
	})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "names no tool")
}

func TestCovInvokeRefusesANonUnaryOperation(t *testing.T) {
	// Streaming comes in both directions, and neither is one request and one
	// response. A client-streaming operation is refused before any connection is
	// opened, so a caller gets a reason rather than a hang.
	_, err := mcp.NewInvoker(mcp.InvokerOptions{}).Invoke(context.Background(), api.Call{
		Server:    api.Server{ID: "petstore", Name: "Pet store", BaseURL: "http://petstore.test"},
		Operation: api.Operation{ID: "petstore/mcp/upload", Name: "upload", Streaming: api.Streaming{Client: true}},
	})
	require.Error(t, err)
	assert.Equal(t, api.KindUnsupported, api.KindOf(err))
}

func TestCovInvokerCloseWithoutAConversationIsClean(t *testing.T) {
	// An invoker that never opened a session has nothing to end. Reporting an error
	// would make every caller's cleanup path look like a failure.
	require.NoError(t, mcp.NewInvoker(mcp.InvokerOptions{}).Close())
}

func TestCovInvokerCloseReportsASessionItCannotEnd(t *testing.T) {
	// Ending a conversation is a protocol call of its own. When the server is already
	// gone the session cannot be deleted, and that failure is reported rather than
	// swallowed: a caller that closes on shutdown needs to know the server still
	// believes the session exists.
	endpoint := covStoppableServer(t, covTool{
		name:        "get_pet",
		description: "Fetch one pet",
		arguments:   covPetArguments(),
	})
	described := covDescribe(t, endpoint.URL)
	operation, found := described.Operation("petstore/mcp/get_pet")
	require.True(t, found)

	invoker := mcp.NewInvoker(mcp.InvokerOptions{})
	_, err := invoker.Invoke(context.Background(), covCall(described, endpoint.URL, operation, `{"petId":"1"}`))
	require.NoError(t, err)

	endpoint.Close()
	require.Error(t, invoker.Close())
}
