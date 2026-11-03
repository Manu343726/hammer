package openapi_test

import (
	"encoding/json"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/openapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests hold the renderer to the promise its doc comments make: a
// description written by any reader becomes a document, and anything the
// renderer had to invent is reported rather than presented as declared. A
// renderer that silently derives a path or drops an operation produces a
// document that describes an API nobody declared.

// covRenderDocument renders a description and decodes the primary file, so a
// test asserts what a reader of the document would see.
func covRenderDocument(t *testing.T, described api.API, options openapi.RenderOptions) (openapi.Rendered, map[string]any) {
	t.Helper()
	rendered, err := openapi.Render(described, options)
	require.NoError(t, err)
	require.Len(t, rendered.Files, 1)
	require.True(t, rendered.Files[0].Primary, "the file a client should start from is marked")
	var document map[string]any
	require.NoError(t, json.Unmarshal(rendered.Files[0].Content, &document))
	return rendered, document
}

func TestCovRendererClaimsTheOpenAPITarget(t *testing.T) {
	// An index asks a renderer what it can publish before asking it to publish;
	// an empty claim would make this package invisible to that index.
	assert.Equal(t, []string{openapi.Target}, openapi.NewRenderer().Targets())
}

func TestCovRenderDerivesTheHTTPMappingItHasToInvent(t *testing.T) {
	// A description parsed from a protobuf contract carries a method name and no
	// path. Refusing it would make the standard model useless for the conversion it
	// exists to enable, so the conventional mapping is applied — and every derived
	// value is reported, because a rendered document must never pretend a derived
	// path was declared.
	described := api.API{
		ID:   "rpc",
		Name: "rpc",
		Services: []api.Service{
			{Name: "pets.v1.PetService", Operations: []api.Operation{
				{Name: "ListPets", Method: "ListPets", Service: "pets.v1.PetService"},
			}},
			{Name: "payments$api", Operations: []api.Operation{
				{Name: "Charge", Method: "", Service: "payments$api"},
			}},
			{Name: "!!!", Operations: []api.Operation{
				{Name: "Ping", Method: "get", Service: "!!!"},
			}},
			{Name: "", Operations: []api.Operation{
				{Name: "", Method: "get"},
			}},
		},
	}
	_, document := covRenderDocument(t, described, openapi.RenderOptions{
		Options: map[string]string{
			"path-prefix":                    "rpc",
			"x-toolbox-include-capabilities": "no",
			"unknown-switch":                 "maybe",
		},
	})

	paths, ok := document["paths"].(map[string]any)
	require.True(t, ok)
	// The package prefix is path noise, so the service's short name is used and
	// lowercased so two services differing only in case cannot collide.
	assert.Contains(t, paths, "/rpc/pet/ListPets")
	// An illegal segment character becomes a dash, and a segment with nothing left
	// becomes a word rather than a bare slash.
	assert.Contains(t, paths, "/rpc/payments-api/Charge")
	assert.Contains(t, paths, "/rpc/operation/Ping")
	// An operation with no name still needs a path, and "call" is the only thing
	// left to call it.
	assert.Contains(t, paths, "/rpc/operation/call")
	// A method that is not an HTTP verb is rendered as POST, and an operation with
	// no method at all is too.
	post, ok := paths["/rpc/pet/ListPets"].(map[string]any)["post"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ListPets", post["operationId"])
}

func TestCovRenderRefusesOrOmitsOperationsItCannotRepresent(t *testing.T) {
	// A path that does not start with / is not an HTTP path, and building a
	// document around it would produce something no server can route. The
	// operation is omitted with a warning; a description with nothing else left is
	// refused rather than rendered empty.
	broken := api.Operation{Name: "Bad", Method: "get", Path: "pets"}
	good := api.Operation{Name: "Good", Method: "get", Path: "/pets"}

	mixed := api.API{ID: "mixed", Name: "mixed", Services: []api.Service{{
		Name: "svc", Operations: []api.Operation{broken, good},
	}}}
	_, document := covRenderDocument(t, mixed, openapi.RenderOptions{})
	paths, ok := document["paths"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, paths, "/pets")
	assert.NotContains(t, paths, "pets", "an unrepresentable path is omitted, not rendered as written")

	only := api.API{ID: "broken", Name: "broken", Services: []api.Service{{
		Name: "svc", Operations: []api.Operation{broken},
	}}}
	_, err := openapi.Render(only, openapi.RenderOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `api "broken" has no operation that OpenAPI can represent`)

	// With the conventional mapping disabled, an operation that declares neither a
	// method nor a path has nothing to render, and the refusal names the reason.
	pathless := api.API{ID: "pathless", Name: "pathless", Services: []api.Service{{
		Name: "svc", Operations: []api.Operation{{Name: "Call", Method: ""}},
	}}}
	_, err = openapi.Render(pathless, openapi.RenderOptions{Options: map[string]string{"derive-http-mapping": "false"}})
	require.Error(t, err)

	noPath := api.API{ID: "nopath", Name: "nopath", Services: []api.Service{{
		Name: "svc", Operations: []api.Operation{{Name: "Call", Method: "get"}},
	}}}
	_, err = openapi.Render(noPath, openapi.RenderOptions{Options: map[string]string{"derive-http-mapping": "false"}})
	require.Error(t, err)
}

func TestCovRenderWritesServersSecurityAndInfo(t *testing.T) {
	// The document's own identity and the credential schemes are what a client
	// reads before it can call anything, so every field a description carries has
	// to appear in the document.
	described := api.API{
		ID:          "shop",
		Title:       "Shop",
		Version:     "2.0",
		Description: "A shop",
		DeclaredServers: []api.DeclaredServer{
			{URL: "https://primary.test", Description: "primary"},
			{URL: "https://secondary.test"},
		},
		SecuritySchemes: []api.SecurityScheme{
			{Name: "key", Type: "apiKey", In: "header", ParameterName: "X-Key", Description: "the key"},
			{Name: "oauth", Type: "oauth2", Scopes: []string{"read", "write"}},
		},
		Services: []api.Service{{Name: "things", Operations: []api.Operation{
			{Name: "list", Method: "get", Path: "/things"},
		}}},
	}
	rendered, document := covRenderDocument(t, described, openapi.RenderOptions{})

	assert.Equal(t, "application/json", rendered.MediaType)
	assert.Equal(t, "json", rendered.FileExtension)
	assert.Equal(t, "openapi.json", rendered.Files[0].Name)
	assert.Equal(t, "3.1.0", document["openapi"])

	info, ok := document["info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Shop", info["title"])
	assert.Equal(t, "2.0", info["version"])
	assert.Equal(t, "A shop", info["description"])

	servers, ok := document["servers"].([]any)
	require.True(t, ok)
	require.Len(t, servers, 2)
	primary, _ := servers[0].(map[string]any)
	assert.Equal(t, "https://primary.test", primary["url"])
	assert.Equal(t, "primary", primary["description"])
	secondary, _ := servers[1].(map[string]any)
	assert.NotContains(t, secondary, "description", "an absent description is not written as an empty one")

	components, ok := document["components"].(map[string]any)
	require.True(t, ok)
	schemes, ok := components["securitySchemes"].(map[string]any)
	require.True(t, ok)
	key, _ := schemes["key"].(map[string]any)
	assert.Equal(t, "apiKey", key["type"])
	assert.Equal(t, "header", key["in"])
	assert.Equal(t, "X-Key", key["name"])
	assert.Equal(t, "the key", key["description"])
	oauth, _ := schemes["oauth"].(map[string]any)
	scopes, ok := oauth["scopes"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, scopes, "read")
	assert.Contains(t, scopes, "write")
}

func TestCovRenderWritesEverySchemaDetail(t *testing.T) {
	// A rendered schema is what a client builds its values from. Every detail a
	// description can carry is asserted here, including the reuse of a repeated
	// named schema as a component rather than repeating it inline.
	detailed := &api.Schema{
		Ref:                  "#/components/schemas/Thing",
		Type:                 api.TypeObject,
		Title:                "Thing",
		Description:          "A thing",
		Deprecated:           true,
		ReadOnly:             true,
		Pattern:              "^[a-z]+$",
		Enum:                 []string{"a", "b"},
		Items:                api.StringSchema(),
		AdditionalProperties: &api.Schema{Type: api.TypeBoolean},
		Properties:           []api.Property{{Name: "id", Schema: api.StringSchema()}},
		Required:             []string{"id"},
		OneOf:                []*api.Schema{api.StringSchema()},
		AnyOf:                []*api.Schema{{Type: api.TypeInteger}},
	}
	described := api.API{
		ID: "schemas", Name: "schemas",
		Services: []api.Service{{Name: "things", Operations: []api.Operation{
			{
				Name:        "create",
				Method:      "post",
				Path:        "/things",
				Description: "Creates a thing",
				Deprecated:  true,
				Parameters: []api.Parameter{
					{Name: "tenant", In: "header", Description: "Tenant", Required: true, Schema: &api.Schema{Ref: "#/components/schemas/Tenant"}},
					{Name: "", In: "query", Schema: &api.Schema{Ref: "#/components/schemas/Tenant"}},
				},
				Request:  detailed,
				Response: detailed,
			},
			{
				Name:    "replace",
				Method:  "put",
				Path:    "/things",
				Request: detailed,
			},
		}}},
	}
	_, document := covRenderDocument(t, described, openapi.RenderOptions{})

	paths, _ := document["paths"].(map[string]any)
	item, ok := paths["/things"].(map[string]any)
	require.True(t, ok)
	create, ok := item["post"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Creates a thing", create["description"])
	assert.Equal(t, true, create["deprecated"])

	parameters, ok := create["parameters"].([]any)
	require.True(t, ok)
	require.Len(t, parameters, 2)
	first, _ := parameters[0].(map[string]any)
	assert.Equal(t, "Tenant", first["description"])
	assert.Equal(t, true, first["required"])

	// The first use of a named body schema becomes a component and the operation
	// points at it, which is what a reader of the document expects to see.
	body, ok := create["requestBody"].(map[string]any)
	require.True(t, ok)
	content, _ := body["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	requestSchema, _ := media["schema"].(map[string]any)
	assert.Equal(t, "#/components/schemas/ThingRequest", requestSchema["$ref"])

	components, _ := document["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	thing, ok := schemas["ThingRequest"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "object", thing["type"])
	assert.Equal(t, "Thing", thing["title"])
	assert.Equal(t, "A thing", thing["description"])
	assert.Equal(t, true, thing["deprecated"])
	assert.Equal(t, true, thing["readOnly"])
	assert.Equal(t, "^[a-z]+$", thing["pattern"])
	assert.Equal(t, []any{"a", "b"}, thing["enum"])
	assert.Equal(t, []any{"id"}, thing["required"])
	assert.Contains(t, thing, "properties")
	assert.Contains(t, thing, "items")
	assert.Contains(t, thing, "additionalProperties")
	assert.Contains(t, thing, "oneOf")
	assert.Contains(t, thing, "anyOf")

	// A second use of the same schema points at the component rather than
	// duplicating it, and so does a response using the same shape.
	responses, _ := create["responses"].(map[string]any)
	success, _ := responses["200"].(map[string]any)
	responseContent, _ := success["content"].(map[string]any)
	responseMedia, _ := responseContent["application/json"].(map[string]any)
	responseSchema, _ := responseMedia["schema"].(map[string]any)
	assert.Equal(t, "#/components/schemas/ThingRequest", responseSchema["$ref"])

	replace, ok := item["put"].(map[string]any)
	require.True(t, ok)
	replaceBody, _ := replace["requestBody"].(map[string]any)
	replaceContent, _ := replaceBody["content"].(map[string]any)
	replaceMedia, _ := replaceContent["application/json"].(map[string]any)
	replaceSchema, _ := replaceMedia["schema"].(map[string]any)
	assert.Equal(t, "#/components/schemas/ThingRequest", replaceSchema["$ref"],
		"a repeated schema is referenced, not re-declared")
}

func TestCovRenderSerializesYAMLWhenAsked(t *testing.T) {
	// A document meant for a person or a repository is YAML, and the file is named
	// for the content so a caller is never handed YAML named .json. The switch is
	// available both as a media type and as a target option.
	described := api.API{
		ID: "yaml", Name: "yaml",
		Services: []api.Service{{Name: "things", Operations: []api.Operation{
			{Name: "list", Method: "get", Path: "/things"},
		}}},
	}

	byMediaType, err := openapi.Render(described, openapi.RenderOptions{MediaType: "text/yaml"})
	require.NoError(t, err)
	assert.Equal(t, "openapi.yaml", byMediaType.Files[0].Name)
	assert.Equal(t, "application/yaml", byMediaType.MediaType)
	assert.Equal(t, "yaml", byMediaType.FileExtension)
	assert.Contains(t, string(byMediaType.Files[0].Content), "openapi: 3.1.0")

	byOption, err := openapi.Render(described, openapi.RenderOptions{
		Options: map[string]string{"format": "yml"},
	})
	require.NoError(t, err)
	assert.Contains(t, string(byOption.Files[0].Content), "openapi: 3.1.0",
		"the format option selects YAML even though the media type is the default")
}

func TestCovRenderFallsBackForAMissingTitle(t *testing.T) {
	// A description read from a document always has a title, but a description
	// built by hand need not. An empty title in the document would make it
	// unreadable by this package's own reader, so the field is written and left
	// empty rather than omitted — the reader rejects it and says why.
	described := api.API{
		Services: []api.Service{{Name: "things", Operations: []api.Operation{
			{Name: "list", Method: "get", Path: "/things"},
		}}},
	}
	_, document := covRenderDocument(t, described, openapi.RenderOptions{})

	info, ok := document["info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "", info["title"])
	assert.Equal(t, "0.0.0", info["version"], "a document needs a version, so one is supplied")
}

func TestCovRenderAcceptsEverySpellingOfATargetSwitch(t *testing.T) {
	// A target switch is data a caller wrote by hand, so it is read leniently: a
	// recognizable truthy or falsy value selects the branch, and anything else keeps
	// the default instead of failing the render. The include-capabilities switch is
	// the one this target documents, and both spellings must render the same API.
	described := api.API{
		ID: "switches", Name: "switches",
		Services: []api.Service{{Name: "things", Operations: []api.Operation{
			{Name: "list", Method: "get", Path: "/things"},
		}}},
	}
	for _, value := range []string{"true", "1", "yes", "false", "0", "no", "maybe"} {
		t.Run(value, func(t *testing.T) {
			_, document := covRenderDocument(t, described, openapi.RenderOptions{
				Options: map[string]string{"x-toolbox-include-capabilities": value},
			})
			paths, ok := document["paths"].(map[string]any)
			require.True(t, ok)
			assert.Contains(t, paths, "/things")
		})
	}
}

func TestCovRenderRefusesADescriptionWithNoOperation(t *testing.T) {
	// An empty document describes nothing, and pretending otherwise would publish
	// an API with no surface.
	_, err := openapi.Render(api.API{ID: "empty", Name: "empty"}, openapi.RenderOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `api "empty" has no operation that OpenAPI can represent`)
}
