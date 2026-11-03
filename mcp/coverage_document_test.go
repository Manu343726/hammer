package mcp_test

import (
	"testing"

	api "github.com/Manu343726/hammer"
	mcp "github.com/Manu343726/hammer/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the published document in both directions: writing a
// description out, and reading one back with no server running. What survives the
// round trip is what a deployment can rely on, so the assertions are about
// identity and consequences, not about JSON layout.

func TestCovManifestRefusesADescriptionItCannotRender(t *testing.T) {
	// The manifest is Render written out, so a description with nothing to offer
	// produces no document at all rather than a document with an empty tool list. An
	// empty list would publish a server that a client connects to and finds useless.
	_, err := mcp.Manifest(api.API{ID: "empty", Name: "empty", Format: "mcp"}, mcp.RenderOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
}

func TestCovManifestReportsADocumentItCannotEncode(t *testing.T) {
	// A schema may carry a default value the JSON encoder cannot represent (a
	// function here). Writing a partial document would publish a tool whose result
	// nobody can read, so the failure is reported as this package's own fault.
	described := covShop()
	described.Services[0].Operations[0].Response = &api.Schema{
		Type:    api.TypeObject,
		Default: func() {},
	}

	_, err := mcp.Manifest(described, mcp.RenderOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInternal, api.KindOf(err))
}

func TestCovDescribeDocumentRequiresADocument(t *testing.T) {
	// An absent document describes nothing. Reading it as an empty API would
	// register nothing at all while reporting success.
	_, _, err := mcp.DescribeDocument(nil, mcp.DescribeOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "tool manifest is required")
}

func TestCovDescribeDocumentNamesAManifestThatNamesNoServer(t *testing.T) {
	// A manifest carried in a configuration file may name no server. The description
	// still needs an identifier, and a fixed one is better than an empty one: two
	// such manifests then replace each other rather than describing nothing.
	document := []byte(`{"tools":[{"name":"ping","description":"Ping","x-toolbox-operation":"ping"}]}`)

	described, _, err := mcp.DescribeDocument(document, mcp.DescribeOptions{})
	require.NoError(t, err)
	assert.Equal(t, "mcp-tools", described.ID)
}

func TestCovDescribeDocumentHonoursTheToolFilter(t *testing.T) {
	// A caller that keeps one tool gets one tool. The filter names tools, because a
	// tool name is what a client learned, not the operation it was derived from. A
	// filter that matches nothing is refused rather than read as a manifest with no
	// operations: "not published" and "not requested" have to stay distinguishable.
	document := []byte(`{"server":{"name":"shop"},"tools":[
		{"name":"mcp__one","description":"First","x-toolbox-operation":"shop/mcp/one"},
		{"name":"mcp__two","description":"Second","x-toolbox-operation":"shop/mcp/two"}]}`)

	described, _, err := mcp.DescribeDocument(document, mcp.DescribeOptions{Tools: []string{"mcp__two"}})
	require.NoError(t, err)
	require.Len(t, described.Services, 1)
	require.Len(t, described.Services[0].Operations, 1)
	assert.Equal(t, "two", described.Services[0].Operations[0].Name)

	_, _, err = mcp.DescribeDocument(document, mcp.DescribeOptions{Tools: []string{"mcp__three"}})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "matched the request")
}

func TestCovDescribeDocumentRefusesAnEntryItCannotIdentify(t *testing.T) {
	// Two entries with one name are two operations with one identifier, so any
	// decision a policy later makes about that identifier would be a decision about
	// two things at once. The manifest is refused rather than read with a guess.
	document := []byte(`{"server":{"name":"shop"},"tools":[
		{"name":"mcp__get_pet","description":"Fetch","x-toolbox-operation":"shop/mcp/get_pet"},
		{"name":"mcp__get_pet","description":"Fetch again","x-toolbox-operation":"shop/mcp/get_pet"}]}`)

	_, _, err := mcp.DescribeDocument(document, mcp.DescribeOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "normalize the described API")
}

func TestCovDescribeDocumentKeepsEveryDeclaredConsequenceInOrder(t *testing.T) {
	// A manifest states consequences as the protocol's own annotations, and a reader
	// that dropped one would hand a policy an operation that looks harmless. The
	// order is asserted because two reads of one document must be equal.
	document := []byte(`{"server":{"name":"shop"},"tools":[{
		"name":"mcp__refresh",
		"description":"Refresh the cache",
		"x-toolbox-operation":"shop/mcp/refresh",
		"annotations":{"readOnlyHint":true,"destructiveHint":true,"idempotentHint":true}}]}`)

	described, _, err := mcp.DescribeDocument(document, mcp.DescribeOptions{})
	require.NoError(t, err)
	operation, found := described.Operation("shop/mcp/refresh")
	require.True(t, found)
	assert.Equal(t, []api.SideEffect{
		api.SideEffectIrreversible, api.SideEffectReadOnly, api.SideEffect("repeatable"),
	}, operation.SideEffects)
}

func TestCovDescribeDocumentWarnsAboutAnEntryWithNoProse(t *testing.T) {
	// A tool with no description is still a tool, and the gap is reported rather
	// than filled with prose the manifest did not contain.
	document := []byte(`{"server":{"name":"shop"},"tools":[
		{"name":"mcp__ping","x-toolbox-operation":"shop/mcp/ping"}]}`)

	described, warnings, err := mcp.DescribeDocument(document, mcp.DescribeOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{`tool "mcp__ping" declares no description`}, warnings)
	require.Len(t, described.Services[0].Operations, 1)
	assert.Empty(t, described.Services[0].Operations[0].Description)
}

func TestCovDescribeDocumentTrustsTheRecordedOperationName(t *testing.T) {
	// A manifest records the operation its tool name was derived from, and the
	// record is authoritative: it survives a change to the naming rule, which a tool
	// name does not. Here the two deliberately disagree, and the recorded identifier
	// wins, because that is what a policy is written against.
	document := []byte(`{"server":{"name":"shop"},"tools":[
		{"name":"mcp__delete_pet","description":"Remove","x-toolbox-operation":"deletePet"}]}`)

	described, _, err := mcp.DescribeDocument(document, mcp.DescribeOptions{})
	require.NoError(t, err)
	_, found := described.Operation("shop/mcp/deletePet")
	assert.True(t, found, "the recorded operation name is the operation's identity")
	_, found = described.Operation("shop/mcp/delete_pet")
	assert.False(t, found, "the tool name is a presentation, not an identity")
}

func TestCovDescribeDocumentKeepsAToolNamedOnlyByTheServicePrefix(t *testing.T) {
	// A tool whose whole name is the service segment recovers to nothing once the
	// segment is stripped. It is kept under its own name rather than dropped: an
	// operation with an odd name is still an operation, and refusing it would make
	// the document describe less than it published.
	document := []byte(`{"server":{"name":"shop"},"tools":[
		{"name":"mcp__","description":"Oddly named","x-toolbox-operation":""}]}`)

	described, _, err := mcp.DescribeDocument(document, mcp.DescribeOptions{})
	require.NoError(t, err)
	require.Len(t, described.Services[0].Operations, 1)
	assert.Equal(t, "mcp__", described.Services[0].Operations[0].Name)
}
