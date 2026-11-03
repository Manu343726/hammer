package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	api "github.com/Manu343726/hammer"
	mcp "github.com/Manu343726/hammer/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the translation from the standard model into a tool: the name
// a client will learn, the arguments it will validate, and the findings a caller
// needs to see. A tool name is a promise to whatever already learned it, so the
// naming cases here are the reason they exist.

// covShop is a small description with one service and one operation, so a test can
// change one fact and see what the translation did with it.
func covShop() api.API {
	return api.API{
		ID:          "shop",
		Name:        "shop",
		Version:     "1.0.0",
		Title:       "Shop",
		Description: "The shop API",
		Format:      "openapi",
		Transport:   "http",
		Services: []api.Service{{
			Name: "shop.pets",
			Operations: []api.Operation{{
				Name:        "getPet",
				Method:      "get",
				Path:        "/pets/{petId}",
				Summary:     "Fetch one pet",
				SideEffects: []api.SideEffect{api.SideEffectReadOnly},
				Parameters: []api.Parameter{{
					Name:        "petId",
					In:          api.ParameterInPath,
					Description: "Pet identifier",
					Required:    true,
					Schema:      api.StringSchema(),
				}},
				Response: api.ObjectSchema(api.Property{Name: "name", Schema: api.StringSchema()}),
			}},
		}},
	}
}

func TestCovRenderRefusesAfterAFilterHidesEveryOperation(t *testing.T) {
	// A filter that keeps nothing must not publish an empty server: a client would
	// connect, learn no tool, and have no way to tell an empty description from one
	// whose every operation a caller had filtered away.
	_, err := mcp.Render(covShop(), mcp.RenderOptions{Only: []string{"no-such-tool"}})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "no operation that MCP can represent")
}

func TestCovRenderWarnsWhenEveryOperationIsStreaming(t *testing.T) {
	// A description whose every operation streams still renders. The findings are
	// the answer, not a failure: the caller learns that the API exists and that
	// none of it fits a tool call, which is more than an error would say.
	described := covShop()
	described.Services[0].Operations[0].Streaming = api.Streaming{Client: true}

	rendered, err := mcp.Render(described, mcp.RenderOptions{})
	require.NoError(t, err)
	assert.Empty(t, rendered.Tools)
	require.Len(t, rendered.Warnings, 1)
	assert.Contains(t, rendered.Warnings[0], "is streaming and is not offered as a tool")
}

func TestCovRenderTypesAParameterThatDeclaresNoShape(t *testing.T) {
	// A parameter the description left shapeless has to be offered as something: an
	// untyped member would validate as "anything", so it is offered as the one type
	// every protocol carries.
	described := covShop()
	described.Services[0].Operations[0].Parameters[0].Schema = nil
	normalized, err := described.Normalize()
	require.NoError(t, err)

	rendered, err := mcp.Render(normalized, mcp.RenderOptions{})
	require.NoError(t, err)
	require.Len(t, rendered.Tools, 1)
	properties, ok := rendered.Tools[0].InputSchema["properties"].(map[string]any)
	require.True(t, ok)
	petID, ok := properties["petId"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "string", petID["type"])
	assert.Equal(t, "Pet identifier", petID["description"], "the prose survives a shape the description omitted")
	assert.Equal(t, []string{"petId"}, rendered.Tools[0].InputSchema["required"],
		"a parameter that must be supplied stays required")
}

func TestCovRenderKeepsALongNameInsideTheProtocolLimit(t *testing.T) {
	// The protocol refuses a name over its own length limit, so a long service or
	// method is shortened here rather than sent and rejected. Shortening alone would
	// let two long names collapse into one, so the shortened name carries a digest
	// and two different long names must stay different.
	long := api.API{
		ID:     "contract",
		Name:   "contract",
		Format: "grpc",
		Services: []api.Service{{
			Name: strings.Repeat("Service", 20),
			Operations: []api.Operation{{
				Name: strings.Repeat("Method", 20),
			}},
		}},
	}
	rendered, err := mcp.Render(long, mcp.RenderOptions{})
	require.NoError(t, err)
	require.Len(t, rendered.Tools, 1)
	assert.LessOrEqual(t, len(rendered.Tools[0].Name), 128,
		"a name the protocol would refuse is shortened before it is offered")

	again, err := mcp.Render(long, mcp.RenderOptions{})
	require.NoError(t, err)
	assert.Equal(t, rendered.Tools[0].Name, again.Tools[0].Name, "the shortening is deterministic")

	long.Services[0].Operations[0].Name = strings.Repeat("Method", 21)
	other, err := mcp.Render(long, mcp.RenderOptions{})
	require.NoError(t, err)
	require.Len(t, other.Tools, 1)
	assert.NotEqual(t, rendered.Tools[0].Name, other.Tools[0].Name,
		"two long names must not collapse into one and reach the wrong operation")
}

func TestCovRenderNamesAToolWithOnlyTheCharactersTheProtocolAllows(t *testing.T) {
	// A format this package reads may carry a service or method name with spaces,
	// slashes, or punctuation, all of which the protocol forbids in a tool name. The
	// name is reduced rather than passed through, because a client that cannot parse
	// the name cannot call the tool.
	described := api.API{
		ID:     "shop",
		Name:   "shop",
		Format: "openapi",
		Services: []api.Service{{
			Name: "shop.pets v1/legacy",
			Operations: []api.Operation{{
				Name: "Get Pet!",
			}},
		}},
	}
	rendered, err := mcp.Render(described, mcp.RenderOptions{})
	require.NoError(t, err)
	require.Len(t, rendered.Tools, 1)
	name := rendered.Tools[0].Name
	assert.Regexp(t, `^[a-z0-9_]+$`, name,
		"the protocol admits no space, slash, dot, or punctuation in a tool name")
	// Each forbidden character becomes a separator of its own, so a capital that
	// follows one is not glued onto the previous word.
	assert.Equal(t, "pets_v1_legacy__get__pet", name)
}

func TestCovRenderCarriesARepeatableOperationIntoTheTool(t *testing.T) {
	// Idempotence is a consequence a policy can use: a tool that may be retried
	// without further effect is a different decision from one that may not. The
	// translation carries it as a fact of the tool, not only inside the effect list.
	described := covShop()
	described.Services[0].Operations[0].SideEffects = []api.SideEffect{
		api.SideEffectReadOnly, api.SideEffect("repeatable"),
	}
	normalized, err := described.Normalize()
	require.NoError(t, err)

	rendered, err := mcp.Render(normalized, mcp.RenderOptions{})
	require.NoError(t, err)
	require.Len(t, rendered.Tools, 1)
	tool := rendered.Tools[0]
	assert.True(t, tool.ReadOnly)
	assert.True(t, tool.Idempotent)
	assert.False(t, tool.Destructive)
	assert.Equal(t, []api.SideEffect{api.SideEffectReadOnly, api.SideEffect("repeatable")}, tool.SideEffects,
		"the declared effects travel untouched, including one this package names itself")

	// The same consequence is written into the manifest as the protocol's own
	// annotation, because a document read back has to say what the description did.
	document, err := mcp.Manifest(normalized, mcp.RenderOptions{})
	require.NoError(t, err)
	var manifest struct {
		Tools []struct {
			Name        string         `json:"name"`
			Annotations map[string]any `json:"annotations"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(document, &manifest))
	require.Len(t, manifest.Tools, 1)
	assert.Equal(t, true, manifest.Tools[0].Annotations["readOnlyHint"])
	assert.Equal(t, true, manifest.Tools[0].Annotations["idempotentHint"],
		"an idempotent operation is published as idempotent, or a reader would have to assume it may not be retried")
	assert.NotContains(t, manifest.Tools[0].Annotations, "destructiveHint",
		"a consequence nobody declared is not invented")
}

func TestCovRenderQualifiesAnUnnamedAPIAndServiceByItsName(t *testing.T) {
	// An API whose description carries neither an identifier nor a service name
	// still has to name its tools. The description's own name is the only thing
	// left, and inventing a prefix would make the tool name unrecoverable.
	described := api.API{
		Name:   "shop",
		Format: "grpc",
		Services: []api.Service{{
			Operations: []api.Operation{{Name: "ping"}},
		}},
	}
	rendered, err := mcp.Render(described, mcp.RenderOptions{})
	require.NoError(t, err)
	require.Len(t, rendered.Tools, 1)
	assert.Equal(t, "shop", rendered.Tools[0].Qualified)
	assert.Equal(t, "shop__ping", rendered.Tools[0].Name)
}

func TestCovRenderHasNoInstructionsWhenTheDescriptionHasNoProse(t *testing.T) {
	// Instructions are what a client shows the model before it chooses a tool. A
	// description with no title, prose, or name has nothing to say, and inventing
	// something would put this package's words in the API's mouth.
	described := api.API{
		ID:     "shop",
		Format: "grpc",
		Services: []api.Service{{
			Name:       "mcp",
			Operations: []api.Operation{{Name: "ping"}},
		}},
	}
	rendered, err := mcp.Render(described, mcp.RenderOptions{})
	require.NoError(t, err)
	assert.Empty(t, rendered.Instructions)
	assert.Len(t, rendered.Tools, 1)
}
