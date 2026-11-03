package protocontract_test

import (
	"strings"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/openapi"
	"github.com/Manu343726/hammer/protocontract"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The point of a standard description is that a document written in one format can
// be published in another. This test is that claim, exercised in process: a real
// OpenAPI document is parsed by the package that owns it, rendered as a protobuf
// contract by this one, and read back by this one's own reader — with no wrapping
// layer and no transport between them.
//
// The comparison is operation by operation and member by member, because "the file
// compiled" is not the property a consumer depends on: it depends on the operations
// it can see still being there, still classified, and still taking and returning the
// values the document described.

// petStoreDocument is a document a deployment would actually hold: two operations,
// a shared component with an enum, an array, a nested object, a path and a query
// parameter, and two documented statuses.
const petStoreDocument = `{
  "openapi": "3.1.0",
  "info": {"title": "Pet Store", "version": "1.4.0", "description": "A sample API"},
  "servers": [{"url": "https://api.example.test/v1"}],
  "tags": [{"name": "pets", "description": "Pet operations"}],
  "paths": {
    "/pets/{petId}": {
      "parameters": [{"name": "petId", "in": "path", "required": true, "schema": {"type": "string"}}],
      "get": {
        "operationId": "getPetById",
        "summary": "Fetch one pet",
        "tags": ["pets"],
        "parameters": [{"name": "verbose", "in": "query", "schema": {"type": "boolean"}}],
        "responses": {
          "200": {"description": "The pet", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Pet"}}}},
          "404": {"description": "Not found"}
        }
      }
    },
    "/pets": {
      "post": {
        "operationId": "createPet",
        "summary": "Create a pet",
        "tags": ["pets"],
        "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/NewPet"}}}},
        "responses": {"201": {"description": "Created", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Pet"}}}}}
      }
    }
  },
  "components": {
    "schemas": {
      "Pet": {
        "type": "object",
        "required": ["id", "name"],
        "properties": {
          "id": {"type": "integer", "format": "int64"},
          "name": {"type": "string"},
          "status": {"type": "string", "enum": ["available", "pending", "sold"]},
          "tags": {"type": "array", "items": {"type": "string"}},
          "owner": {"$ref": "#/components/schemas/Owner"}
        }
      },
      "NewPet": {
        "type": "object",
        "required": ["name"],
        "properties": {"name": {"type": "string"}, "tag": {"type": "string"}}
      },
      "Owner": {"type": "object", "properties": {"name": {"type": "string"}}}
    }
  }
}`

func TestRendersAnOpenAPIDescriptionAsAContract(t *testing.T) {
	described, _, err := openapi.Parse([]byte(petStoreDocument), openapi.ParseOptions{APIID: "pet-store"})
	require.NoError(t, err)
	assert.Equal(t, openapi.Format, described.Format)

	rendered, err := protocontract.Render(described, protocontract.RenderOptions{})
	require.NoError(t, err, "a description parsed from a document has to be renderable: that conversion is the point of the standard model")
	require.NotEmpty(t, rendered.Files)

	source := string(rendered.Files[0].Content)
	assert.Equal(t, protocontract.MediaTypeContract, rendered.Files[0].MediaType)
	assert.Contains(t, source, `syntax = "proto3";`)
	assert.Contains(t, source, "service pets {")
	// The side effect the document's HTTP methods imply travels as the annotation
	// this package's reader understands, because a protobuf method's own comment is
	// the only classification it can carry.
	assert.Contains(t, source, "// @toolbox.side-effects read_only\n  rpc getPetById(")
	assert.Contains(t, source, "// @toolbox.side-effects create\n  rpc createPet(")
	// An enum the document declared becomes a protobuf enum, not a commented string.
	assert.Contains(t, source, "enum Status {")
	assert.Contains(t, source, "available = 0;")

	reparsed, warnings, err := protocontract.NewDescriptor(protocontract.Descriptor{APIID: "pet-store"}).
		FromDescriptorSet(descriptorSet(t, rendered))
	require.NoError(t, err)
	require.Empty(t, warnings)

	// One service, because the document's one tag groups its operations.
	require.Len(t, reparsed.Services, 1)
	assert.Equal(t, "pets", reparsed.Services[0].Name)
	assert.Equal(t, "Pet operations", reparsed.Services[0].Description)

	original := operationsByName(t, described)
	renderedOperations := operationsByName(t, reparsed)
	require.Len(t, renderedOperations, len(original))
	for name, source := range original {
		roundtripped, ok := renderedOperations[name]
		require.True(t, ok, "operation %q did not survive the contract", name)
		assert.Equal(t, source.SideEffects, roundtripped.SideEffects, "operation %q", name)
		assert.Equal(t, source.Streaming, roundtripped.Streaming, "operation %q", name)
		assert.Equal(t, source.Summary, roundtripped.Summary, "operation %q", name)
		assert.Equal(t, expectedRequest(source), shapeOf(roundtripped.Request),
			"operation %q takes what the document said it takes", name)
		assert.Equal(t, shapeOf(source.Response), shapeOf(roundtripped.Response),
			"operation %q returns what the document said it returns", name)
	}
}

func TestRenderedContractKeepsAValueThatIsNotAnObject(t *testing.T) {
	// A document whose operation answers a list is legal and common, and a protobuf
	// rpc cannot return one. Losing the list would make the published operation
	// answer nothing, so the value is wrapped and the wrapper is reported.
	document := `{
      "openapi": "3.1.0",
      "info": {"title": "Inventory", "version": "2.0"},
      "paths": {
        "/items": {
          "get": {
            "operationId": "listItems",
            "tags": ["inventory"],
            "responses": {"200": {"description": "Items", "content": {"application/json": {"schema": {
              "type": "array",
              "items": {"type": "object", "properties": {"sku": {"type": "string"}}}
            }}}}}
          }
        }
      }
    }`
	described, _, err := openapi.Parse([]byte(document), openapi.ParseOptions{APIID: "inventory"})
	require.NoError(t, err)

	rendered, err := protocontract.Render(described, protocontract.RenderOptions{})
	require.NoError(t, err)

	reparsed, _, err := protocontract.NewDescriptor(protocontract.Descriptor{APIID: "inventory"}).
		FromDescriptorSet(descriptorSet(t, rendered))
	require.NoError(t, err)

	operation, ok := reparsed.Operation("inventory/inventory/listItems")
	require.True(t, ok)
	items, ok := operation.Response.Property("items")
	require.True(t, ok, "the wrapped value is still reachable: %v", operation.Response.Properties)
	require.Equal(t, api.TypeArray, items.Schema.Type)
	sku, ok := items.Schema.Items.Property("sku")
	require.True(t, ok)
	assert.Equal(t, api.TypeString, sku.Schema.Type)
}

// descriptorSet is the compiled artifact a caller hands this package's reader. A
// test that compiled the source itself would be testing its own parse of the text;
// this is the artifact Render published, and reading it is what a consumer does.
func descriptorSet(t *testing.T, rendered protocontract.Rendered) []byte {
	t.Helper()
	for _, file := range rendered.Files {
		if file.MediaType == protocontract.MediaTypeDescriptorSet {
			return file.Content
		}
	}
	t.Fatalf("the rendered contract carries no descriptor set: %v", rendered.Files)
	return nil
}

func operationsByName(t *testing.T, described api.API) map[string]api.Operation {
	t.Helper()
	result := make(map[string]api.Operation, len(described.Operations()))
	for _, operation := range described.Operations() {
		result[operation.Name] = operation
	}
	return result
}

// expectedRequest is the request a rendered contract carries for an operation: the
// members of its request value followed by its declared parameters.
//
// protobuf gives an operation one request message and no place to say where a value
// travels, so the renderer folds the parameters into that message. Comparing
// against the unfolded shape would report a loss where there is a translation.
func expectedRequest(operation api.Operation) string {
	members := make([]string, 0, len(operation.Parameters)+2)
	if operation.Request != nil {
		members = append(members, membersOf(operation.Request)...)
	}
	for _, parameter := range operation.Parameters {
		members = append(members, memberOf(parameter.Name, parameter.Schema, parameter.Required))
	}
	if len(members) == 0 {
		return "{}"
	}
	return "{" + strings.Join(members, ",") + "}"
}

// shapeOf renders a value as text, so two descriptions can be compared member by
// member without depending on Go value identity or on reference spelling.
//
// A reference is deliberately not part of the shape: a protobuf contract carries
// the message a document's pointer named, and the message is the fact, while the
// pointer is how one document happened to spell it.
func shapeOf(schema *api.Schema) string {
	if schema == nil {
		return "nothing"
	}
	switch {
	case schema.Type == api.TypeArray:
		return "[" + shapeOf(schema.Items) + "]"
	case len(schema.Properties) > 0:
		return "{" + strings.Join(membersOf(schema), ",") + "}"
	case schema.AdditionalProperties != nil:
		return "map<" + shapeOf(schema.AdditionalProperties) + ">"
	case len(schema.Enum) > 0:
		return "enum(" + strings.Join(schema.Enum, "|") + ")"
	}
	spelled := schema.Type.String()
	if schema.Format != "" {
		spelled += "/" + schema.Format
	}
	return spelled
}

func membersOf(schema *api.Schema) []string {
	members := make([]string, 0, len(schema.Properties))
	for _, property := range schema.Properties {
		members = append(members, memberOf(property.Name, property.Schema, property.Required || schema.IsRequired(property.Name)))
	}
	return members
}

// memberOf renders one member. Requiredness is compared only where protobuf can
// state it: a message-valued member always has presence, and this package's reader
// reports presence as required, so comparing it would report a loss that is the
// reader's rule rather than the renderer's.
func memberOf(name string, schema *api.Schema, required bool) string {
	if required && !(schema != nil && len(schema.Properties) > 0) {
		return name + ":" + shapeOf(schema) + "!"
	}
	return name + ":" + shapeOf(schema)
}
