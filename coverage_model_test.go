package hammer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	shareddocs "github.com/Manu343726/hammer/docs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file hold the promises the API-description model makes: that
// a description can be copied without one caller mutating another's view, that an
// identifier is derivable exactly one way, that a reader rejects a malformed
// document instead of quietly dropping the part it could not understand, and that
// a value absent from a description is admitted as absent rather than guessed at.

// modelOperation returns an operation that satisfies every rule, so a test only
// has to break the one field it is about.
func modelOperation() Operation {
	return Operation{
		Name:    "getPet",
		APIID:   "shop",
		Service: "pets",
		ID:      "shop/pets/getPet",
		Tags:    []string{"pets"},
		Parameters: []Parameter{{
			Name:   "petId",
			In:     ParameterInPath,
			Schema: StringSchema(),
		}},
		Response: ObjectSchema(Property{Name: "name", Schema: StringSchema()}),
	}
}

// modelAPI returns a description that satisfies every rule, so a table can
// enumerate one violation per row without spelling out a whole API each time.
func modelAPI() API {
	return API{
		ID:        "shop",
		Name:      "shop",
		Format:    "openapi",
		ServerIDs: []string{"prod"},
		Tags:      []string{"pets"},
		Metadata:  map[string]string{"team": "core"},
		Services: []Service{{
			Name:       "pets",
			Operations: []Operation{modelOperation()},
		}},
	}
}

func TestModelDescriptorsCloneWithoutSharingTheirSlices(t *testing.T) {
	// A catalog hands a descriptor to a caller that may want to edit it. If the
	// copy shared the media types or the metadata map, one advertisement of a
	// format would silently rewrite what every other caller reads.
	format := FormatDescriptor{
		ID:             "openapi",
		MediaTypes:     []string{"application/yaml"},
		FileExtensions: []string{".yaml"},
		Metadata:       map[string]string{"spec": "3.1"},
	}
	formatClone := format.Clone()
	formatClone.MediaTypes[0] = "application/json"
	formatClone.FileExtensions[0] = ".json"
	formatClone.Metadata["spec"] = "3.0"

	assert.Equal(t, "application/yaml", format.MediaTypes[0])
	assert.Equal(t, ".yaml", format.FileExtensions[0])
	assert.Equal(t, "3.1", format.Metadata["spec"])

	transport := TransportDescriptor{
		ID:       "http",
		Schemes:  []string{"https"},
		Metadata: map[string]string{"streaming": "no"},
	}
	transportClone := transport.Clone()
	transportClone.Schemes[0] = "http"
	transportClone.Metadata["streaming"] = "yes"

	assert.Equal(t, "https", transport.Schemes[0])
	assert.Equal(t, "no", transport.Metadata["streaming"])
}

func TestModelCloningAnAbsentMapKeepsItAbsent(t *testing.T) {
	// A descriptor that carried no metadata must come back carrying no metadata:
	// an empty map would read as "this format advertises nothing" rather than as
	// "nobody said anything about this format".
	assert.Nil(t, FormatDescriptor{}.Clone().Metadata)
	assert.Nil(t, TransportDescriptor{}.Clone().Metadata)
	assert.Nil(t, Server{}.Clone().Metadata)
	assert.Nil(t, API{}.Clone().Metadata)
}

func TestModelStreamingIsTrueInEitherDirection(t *testing.T) {
	// A client-streaming call is as much a stream as a server-streaming one; a
	// caller that missed the client direction would try to buffer a request the
	// transport can never finish.
	assert.False(t, Streaming{}.Streaming(), "an operation that streams nothing is not a stream")
	assert.True(t, Streaming{Client: true}.Streaming())
	assert.True(t, Streaming{Server: true}.Streaming())
	assert.True(t, Streaming{Client: true, Server: true}.Streaming())
}

func TestModelPropertyCloneDoesNotShareItsSchema(t *testing.T) {
	property := Property{Name: "name", Schema: &Schema{Type: TypeString, Description: "before"}, Required: true}
	clone := property.Clone()
	clone.Schema.Description = "after"

	assert.Equal(t, "before", property.Schema.Description)
	assert.Equal(t, property.Name, clone.Name)
	assert.Equal(t, property.Required, clone.Required)
	assert.Nil(t, Property{Name: "absent"}.Clone().Schema, "a property with no shape clones to one with no shape")
}

func TestModelOperationCloneCopiesEveryPartACallerCanEdit(t *testing.T) {
	// An exposure decision is made against a copy of an operation. Sharing the
	// parameters, the response shapes, or the declared side effects would let a
	// caller's edit change what a later policy decision sees.
	original := modelOperation()
	original.Request = ObjectSchema(Property{Name: "filter", Schema: StringSchema()})
	original.Responses = []Response{{Status: "404", Schema: ObjectSchema(Property{Name: "error", Schema: StringSchema()})}}
	original.Security = []SecurityRequirement{{Scheme: "oauth", Scopes: []string{"pets:read"}}}
	original.SideEffects = []SideEffect{SideEffectReadOnly}

	clone := original.Clone()
	clone.Tags[0] = "other"
	clone.SideEffects[0] = SideEffectDelete
	clone.Security[0].Scheme = "other"
	clone.Parameters[0].Schema.Description = "changed"
	clone.Request.Properties[0].Schema.Description = "changed"
	clone.Response.Properties[0].Schema.Description = "changed"
	clone.Responses[0].Schema.Properties[0].Schema.Description = "changed"

	assert.Equal(t, "pets", original.Tags[0])
	assert.Equal(t, SideEffectReadOnly, original.SideEffects[0])
	assert.Equal(t, "oauth", original.Security[0].Scheme)
	assert.Empty(t, original.Parameters[0].Schema.Description)
	assert.Empty(t, original.Request.Properties[0].Schema.Description)
	assert.Empty(t, original.Response.Properties[0].Schema.Description)
	assert.Empty(t, original.Responses[0].Schema.Properties[0].Schema.Description)
}

func TestModelAPICloneCopiesEveryPartACallerCanEdit(t *testing.T) {
	original := modelAPI()
	original.DeclaredServers = []DeclaredServer{{URL: "https://api.test"}}
	original.Security = []SecurityRequirement{{Scheme: "oauth", Scopes: []string{"pets:read"}}}
	original.SecuritySchemes = []SecurityScheme{{Name: "oauth", Scopes: []string{"pets:read"}}}

	clone := original.Clone()
	clone.ServerIDs[0] = "staging"
	clone.DeclaredServers[0].URL = "https://other.test"
	clone.Tags[0] = "other"
	clone.Security[0].Scheme = "other"
	clone.SecuritySchemes[0].Scopes[0] = "pets:write"
	clone.Services[0].Operations[0].Name = "other"
	clone.Metadata["team"] = "other"

	assert.Equal(t, "prod", original.ServerIDs[0])
	assert.Equal(t, "https://api.test", original.DeclaredServers[0].URL)
	assert.Equal(t, "pets", original.Tags[0])
	assert.Equal(t, "oauth", original.Security[0].Scheme)
	assert.Equal(t, "pets:read", original.SecuritySchemes[0].Scopes[0])
	assert.Equal(t, "getPet", original.Services[0].Operations[0].Name)
	assert.Equal(t, "core", original.Metadata["team"])
}

func TestModelAPIQueriesFindByNameOrIdentifier(t *testing.T) {
	// A policy or a tool names an operation by its identifier, while a person
	// names a service as it was written. Both lookups have to work, and a lookup
	// that misses must say so rather than return an empty operation that then
	// matches every pattern.
	described := modelAPI()
	second := Service{ID: "shop/orders", Name: "orders", Operations: []Operation{{Name: "createOrder", ID: "shop/orders/createOrder"}}}
	described.Services = append(described.Services, second)

	assert.Equal(t, []string{"shop/pets/getPet", "shop/orders/createOrder"}, described.OperationIDs(),
		"the operation order follows the description, so two readings of one API list the same identifiers")
	assert.Len(t, described.Operations(), 2)

	service, ok := described.Service("pets")
	require.True(t, ok)
	assert.Equal(t, "pets", service.Name)

	service, ok = described.Service(" shop/orders ")
	require.True(t, ok, "a service is also found by the identifier an exposure decision stores")
	assert.Equal(t, "orders", service.Name)

	_, ok = described.Service("missing")
	assert.False(t, ok)

	operation, ok := described.Operation(" shop/pets/getPet ")
	require.True(t, ok)
	assert.Equal(t, "getPet", operation.Name)

	_, ok = described.Operation("shop/missing/op")
	assert.False(t, ok)
}

func TestModelMetadataKeysAreSorted(t *testing.T) {
	// A report over metadata keys has to be deterministic, or two runs over one
	// catalog produce two different documents.
	metadata := map[string]string{"b": "2", "a": "1"}
	assert.Equal(t, []string{"a", "b"}, API{Metadata: metadata}.MetadataKeys())
	assert.Equal(t, []string{"a", "b"}, Server{Metadata: metadata}.MetadataKeys())
	assert.Equal(t, []string{"a", "b"}, FormatDescriptor{Metadata: metadata}.MetadataKeys())
	assert.Equal(t, []string{"a", "b"}, TransportDescriptor{Metadata: metadata}.MetadataKeys())
	assert.Empty(t, API{}.MetadataKeys())
}

func TestModelIdentifierConstructionNeverEmitsADanglingSeparator(t *testing.T) {
	// An identifier is what a policy pattern is written against. A stray leading
	// or trailing separator would make "shop/pets/getPet" and "/pets/getPet" two
	// different names for one operation.
	assert.Equal(t, "shop/pets/getPet", OperationID("shop", "pets", "getPet"))
	assert.Equal(t, "getPet", OperationID("", "", " getPet "))
	assert.Equal(t, "shop/getPet", OperationID("shop", " ", "getPet"))
	assert.Equal(t, "shop/pets", ServiceID(" shop ", " pets "))
	assert.Empty(t, ServiceID("", ""))
}

func TestModelAnnotateOperationIDsRejectsWhatItCannotName(t *testing.T) {
	// An unnameable service or operation would produce an identifier that no
	// policy can address, so the annotation step refuses instead of inventing one.
	var absent *API
	require.Error(t, absent.AnnotateOperationIDs())

	unnamedService := modelAPI()
	unnamedService.Services[0].Name = " "
	require.Error(t, unnamedService.AnnotateOperationIDs())

	unnamedOperation := modelAPI()
	unnamedOperation.Services[0].Operations[0].Name = ""
	require.Error(t, unnamedOperation.AnnotateOperationIDs())

	withoutID := modelAPI()
	withoutID.ID = ""
	require.Error(t, withoutID.AnnotateOperationIDs())
}

func TestModelAnnotateOperationIDsTrimsTheNamesItBuildsFrom(t *testing.T) {
	// A description language pads names with whatever formatting its document
	// used. The identifier has to be the same however the name was padded, or the
	// same operation gets two identifiers depending on the parser.
	described := modelAPI()
	described.Services[0].Name = " pets "
	described.Services[0].Operations[0].Name = " getPet "
	require.NoError(t, described.AnnotateOperationIDs())

	assert.Equal(t, "shop", described.ID)
	assert.Equal(t, "shop/pets", described.Services[0].ID)
	assert.Equal(t, "shop/pets/getPet", described.Services[0].Operations[0].ID)
	assert.Equal(t, "pets", described.Services[0].Operations[0].Service)
}

func TestModelNormalizeIdentifierMakesOpenNamesComparable(t *testing.T) {
	// Format and transport identifiers come from whoever contributed them, so
	// "OpenAPI" and " openapi " are the same format. Comparing them verbatim would
	// make a catalog claim it cannot parse a document it just parsed.
	assert.Equal(t, "openapi", NormalizeIdentifier("  OpenAPI "))
	assert.Equal(t, "grpc", NormalizeIdentifier("GRPC"))
	assert.Empty(t, NormalizeIdentifier("   "))
	assert.Equal(t, NormalizeIdentifier(" Mcp "), NormalizeIdentifier("mcp"))
}

func TestModelSummarizeDoesNotShareTheServerList(t *testing.T) {
	// A summary is handed to a listing caller while the catalog keeps serving the
	// API. Mutating the summary must not reach back into the description.
	described := modelAPI()
	summary := described.Summarize()
	require.Len(t, summary.ServerIDs, 1)
	summary.ServerIDs[0] = "staging"

	assert.Equal(t, "prod", described.ServerIDs[0])
	assert.Equal(t, 1, summary.Services)
	assert.Equal(t, 1, summary.Operations)
	assert.Equal(t, "shop", summary.Name)
}

func TestModelSchemaCloneCopiesBoundsAndAlternatives(t *testing.T) {
	// The bounds are pointers, so a shallow copy would let a caller widen an
	// original schema's limits while editing a derived one.
	original := &Schema{
		Type:                 TypeObject,
		Ref:                  "#/components/schemas/Pet",
		Enum:                 []string{"cat"},
		Required:             []string{"name"},
		Properties:           []Property{{Name: "name", Schema: StringSchema(), Required: true}},
		Items:                StringSchema(),
		AdditionalProperties: StringSchema(),
		OneOf:                []*Schema{StringSchema()},
		AnyOf:                []*Schema{{Type: TypeNull}},
		Minimum:              Float64(1),
		Maximum:              Float64(9),
		MinLength:            Int64(1),
		MaxLength:            Int64(9),
	}

	clone := original.Clone()
	clone.Enum[0] = "dog"
	clone.Required[0] = "other"
	clone.Properties[0].Schema.Description = "changed"
	clone.Items.Description = "changed"
	clone.AdditionalProperties.Description = "changed"
	clone.OneOf[0].Description = "changed"
	clone.AnyOf[0].Description = "changed"
	*clone.Minimum = 0
	*clone.Maximum = 100
	*clone.MinLength = 0
	*clone.MaxLength = 100

	assert.Equal(t, []string{"cat"}, original.Enum)
	assert.Equal(t, []string{"name"}, original.Required)
	assert.Empty(t, original.Properties[0].Schema.Description)
	assert.Empty(t, original.Items.Description)
	assert.Empty(t, original.AdditionalProperties.Description)
	assert.Empty(t, original.OneOf[0].Description)
	assert.Empty(t, original.AnyOf[0].Description)
	assert.Equal(t, 1.0, *original.Minimum)
	assert.Equal(t, 9.0, *original.Maximum)
	assert.Equal(t, int64(1), *original.MinLength)
	assert.Equal(t, int64(9), *original.MaxLength)
}

func TestModelCloningAnAbsentSchemaKeepsItAbsent(t *testing.T) {
	// A nil shape means "this value is not described", which is different from an
	// empty shape. A clone that materialised one would turn an undeclared request
	// body into an empty object.
	var absent *Schema
	assert.Nil(t, absent.Clone())
	assert.Nil(t, (&Schema{}).Clone().Items)
	assert.Nil(t, (&Schema{OneOf: nil, AnyOf: nil}).Clone().OneOf)
}

func TestModelSchemaLooksUpPropertiesAndRequiredness(t *testing.T) {
	schema := ObjectSchema(
		Property{Name: "name", Schema: StringSchema(), Required: true},
		Property{Name: "kind", Schema: StringSchema()},
	)

	property, ok := schema.Property("name")
	require.True(t, ok)
	assert.Equal(t, TypeString, property.Schema.Type)

	_, ok = schema.Property("missing")
	assert.False(t, ok)

	assert.True(t, schema.IsRequired("name"))
	assert.False(t, schema.IsRequired("kind"))

	var absent *Schema
	_, ok = absent.Property("name")
	assert.False(t, ok, "a schema nobody declared has no members")
	assert.False(t, absent.IsRequired("name"), "a property of an absent schema is not required")
}

func TestModelObjectSchemaSortsOnlyTheRequiredNames(t *testing.T) {
	// The rendered JSON Schema is compared between reads, so the required list is
	// sorted while the properties keep the description's order.
	schema := ObjectSchema(
		Property{Name: "zeta", Schema: StringSchema(), Required: true},
		Property{Name: "alpha", Schema: StringSchema()},
		Property{Name: "beta", Schema: StringSchema(), Required: true},
	)
	assert.Equal(t, []string{"beta", "zeta"}, schema.Required)
	assert.Equal(t, []string{"zeta", "alpha", "beta"}, []string{
		schema.Properties[0].Name, schema.Properties[1].Name, schema.Properties[2].Name,
	})
}

func TestModelSchemaFromJSONSchemaReadsEveryNumberSpelling(t *testing.T) {
	// A document reaches this reader through an encoding/json decode or a YAML
	// decode, and the two disagree about which Go type a number is. A spelling the
	// reader does not know becomes an absent bound, which silently widens a
	// constraint instead of failing.
	minimums := []struct {
		name  string
		value any
		want  *float64
	}{
		{"float64", float64(1.5), Float64(1.5)},
		{"float32", float32(2.5), Float64(2.5)},
		{"int", 3, Float64(3)},
		{"int64", int64(4), Float64(4)},
		{"json number", json.Number("5.5"), Float64(5.5)},
		{"malformed json number", json.Number("not a number"), nil},
		{"string", "6", nil},
	}
	for _, test := range minimums {
		t.Run(test.name, func(t *testing.T) {
			read := SchemaFromJSONSchema(map[string]any{"type": "number", "minimum": test.value})
			require.NotNil(t, read)
			if test.want == nil {
				assert.Nil(t, read.Minimum, "a value the reader cannot read is not invented")
				return
			}
			require.NotNil(t, read.Minimum)
			assert.Equal(t, *test.want, *read.Minimum)
		})
	}

	lengths := []struct {
		name  string
		value any
		want  *int64
	}{
		{"float64", float64(1), Int64(1)},
		{"int", 2, Int64(2)},
		{"int64", int64(3), Int64(3)},
		{"json number", json.Number("4"), Int64(4)},
		{"fractional json number", json.Number("4.5"), nil},
		{"bool", true, nil},
	}
	for _, test := range lengths {
		t.Run("minLength/"+test.name, func(t *testing.T) {
			read := SchemaFromJSONSchema(map[string]any{"type": "string", "minLength": test.value})
			require.NotNil(t, read)
			if test.want == nil {
				assert.Nil(t, read.MinLength)
				return
			}
			require.NotNil(t, read.MinLength)
			assert.Equal(t, *test.want, *read.MinLength)
		})
	}
}

func TestModelSchemaFromJSONSchemaKeepsOnlyTheListEntriesItCanRead(t *testing.T) {
	// A malformed entry in a list must not discard the ones around it: a document
	// describing ["cat", 7, "dog"] still describes two legal values, and dropping
	// them would turn a constrained field into an unconstrained one.
	read := SchemaFromJSONSchema(map[string]any{
		"type": "string",
		"enum": []any{"cat", 7, "dog"},
	})
	require.NotNil(t, read)
	assert.Equal(t, []string{"cat", "dog"}, read.Enum)

	assert.Nil(t, SchemaFromJSONSchema(map[string]any{"enum": []any{7, 8}}).Enum,
		"a list with nothing readable is absent, not empty")
	assert.Nil(t, SchemaFromJSONSchema(map[string]any{"enum": "cat"}).Enum,
		"a keyword that is not a list describes nothing")
	assert.Nil(t, SchemaFromJSONSchema(map[string]any{"enum": []any{}}).Enum)

	assert.Nil(t, SchemaFromJSONSchema(map[string]any{"required": []any{1}}).Required)
}

func TestModelSchemaFromJSONSchemaIgnoresAlternativesItCannotRead(t *testing.T) {
	// An alternative that is not an object cannot be a schema. Reporting the
	// document as unreadable would lose the alternatives that are readable.
	assert.Nil(t, SchemaFromJSONSchema(map[string]any{"oneOf": "string"}).OneOf)
	assert.Nil(t, SchemaFromJSONSchema(map[string]any{"oneOf": []any{}}).OneOf)
	assert.Nil(t, SchemaFromJSONSchema(map[string]any{"anyOf": []any{}}).AnyOf)

	read := SchemaFromJSONSchema(map[string]any{
		"oneOf": []any{"not a schema", map[string]any{"type": "string"}},
	})
	require.NotNil(t, read)
	require.Len(t, read.OneOf, 2)
	assert.Nil(t, read.OneOf[0], "an alternative that is not an object describes no shape")
	assert.Equal(t, TypeString, read.OneOf[1].Type)
}

func TestModelSchemaFromJSONSchemaReadsOnlyTheFirstUsableType(t *testing.T) {
	// The model carries one type. A list whose first entry is not a name must not
	// leave the schema untyped when a later entry names one.
	assert.Equal(t, TypeString, SchemaFromJSONSchema(map[string]any{"type": []any{7, "string", "null"}}).Type)
	assert.Equal(t, TypeUnspecified, SchemaFromJSONSchema(map[string]any{"type": []any{}}).Type)
	assert.Equal(t, TypeUnspecified, SchemaFromJSONSchema(map[string]any{"type": 7}).Type)
	assert.Equal(t, TypeUnspecified, SchemaFromJSONSchema(map[string]any{"type": []any{1, 2}}).Type)
}

func TestModelSchemaFromJSONSchemaTrimsAReference(t *testing.T) {
	// A document written by hand pads its reference. The identifier is looked up
	// verbatim later, so the padding cannot be carried into the model.
	assert.Equal(t, "#/components/schemas/Pet",
		SchemaFromJSONSchema(map[string]any{"$ref": "  #/components/schemas/Pet  "}).Ref)
}

func TestModelSchemaValidateRejectsAMalformedShape(t *testing.T) {
	// Each row is a document the model can hold but cannot honour. Accepting one
	// would produce a tool definition that promises a contract no request can
	// satisfy.
	readable := func(document map[string]any) *Schema { return SchemaFromJSONSchema(document) }

	tests := []struct {
		name   string
		schema *Schema
		reason string
	}{
		{
			name:   "unnamed property",
			schema: &Schema{Type: TypeObject, Properties: []Property{{Name: " ", Schema: StringSchema()}}},
			reason: "property name is required",
		},
		{
			name: "duplicate property",
			schema: &Schema{Type: TypeObject, Properties: []Property{
				{Name: "name", Schema: StringSchema()},
				{Name: "name", Schema: StringSchema()},
			}},
			reason: "declared more than once",
		},
		{
			name: "malformed property shape",
			schema: &Schema{Type: TypeObject, Properties: []Property{
				{Name: "tags", Schema: &Schema{Type: TypeArray}},
			}},
			reason: "items schema",
		},
		{
			name:   "required property not declared",
			schema: &Schema{Type: TypeObject, Required: []string{"missing"}},
			reason: "is not declared",
		},
		{
			name:   "array without an element shape",
			schema: &Schema{Type: TypeArray},
			reason: "items schema",
		},
		{
			name:   "inverted bounds",
			schema: &Schema{Type: TypeNumber, Minimum: Float64(10), Maximum: Float64(1)},
			reason: "greater than maximum",
		},
		{
			name:   "malformed element shape",
			schema: &Schema{Type: TypeObject, Items: &Schema{Type: TypeArray}},
			reason: "items schema",
		},
		{
			name:   "malformed additional property shape",
			schema: &Schema{Type: TypeObject, AdditionalProperties: &Schema{Type: TypeArray}},
			reason: "items schema",
		},
		{
			name:   "malformed alternative",
			schema: &Schema{OneOf: []*Schema{{Type: TypeArray}}},
			reason: "oneOf[0]",
		},
		{
			name:   "malformed alternative of the other kind",
			schema: &Schema{AnyOf: []*Schema{{Type: TypeArray}}},
			reason: "anyOf[0]",
		},
		{
			name:   "document declaring an array without items",
			schema: readable(map[string]any{"type": "array"}),
			reason: "items schema",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.schema.Validate("schema")
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalid)
			assert.Contains(t, err.Error(), test.reason)
		})
	}

	assert.NoError(t, (&Schema{Type: TypeObject}).Validate("schema"))
}

func TestModelParameterValidateRejectsAnUnnamedParameter(t *testing.T) {
	// A parameter is addressed by name after the request is parsed; one without a
	// name can never be filled.
	err := Parameter{In: ParameterInQuery, Schema: StringSchema()}.Validate("parameter")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parameter name is required")

	assert.NoError(t, Parameter{Name: "q", In: ParameterInQuery, Schema: StringSchema()}.Validate("parameter"))
}

func TestModelOperationValidateRejectsWhatCannotBeInvoked(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Operation)
		want   string
	}{
		{"no name", func(o *Operation) { o.Name = "  " }, "operation name is required"},
		{"no api", func(o *Operation) { o.APIID = "" }, "has no API id"},
		{"malformed parameter", func(o *Operation) { o.Parameters[0].Name = " " }, "parameter name is required"},
		{"parameter without a location", func(o *Operation) { o.Parameters[0].In = " " }, "has no location"},
		{
			"duplicate parameter",
			func(o *Operation) {
				o.Parameters = append(o.Parameters, Parameter{Name: "petId", In: ParameterInQuery, Schema: StringSchema()})
			},
			"declared more than once",
		},
		{"malformed request", func(o *Operation) { o.Request = &Schema{Type: TypeArray} }, "items schema"},
		{"malformed response", func(o *Operation) { o.Response = &Schema{Type: TypeArray} }, "items schema"},
		{
			"malformed documented response",
			func(o *Operation) { o.Responses = []Response{{Status: "500", Schema: &Schema{Type: TypeArray}}} },
			"items schema",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operation := modelOperation()
			test.mutate(&operation)
			err := operation.Validate("operation")
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalid)
			assert.Contains(t, err.Error(), test.want)
		})
	}

	assert.NoError(t, modelOperation().Validate("operation"))
}

func TestModelServiceValidateRejectsWhatCannotBeAddressed(t *testing.T) {
	// A service name is a pattern segment, so an unnamed service or two operations
	// with one name would make a rule address something other than it appears to.
	unnamed := Service{Operations: []Operation{modelOperation()}}
	err := unnamed.Validate("service")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service name is required")

	malformed := Service{Name: "pets", Operations: []Operation{{}}}
	err = malformed.Validate("service")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "operations[0]")

	duplicate := Service{Name: "pets", Operations: []Operation{modelOperation(), modelOperation()}}
	err = duplicate.Validate("service")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "declared more than once")

	assert.NoError(t, Service{Name: "pets", Operations: []Operation{modelOperation()}}.Validate("service"))
}

func TestModelAPIValidateRejectsAnUnaddressableDescription(t *testing.T) {
	// Every row is a description whose identifiers, servers, or shape make an
	// exposure decision impossible to express. Validation exists so the catalog
	// refuses it at registration rather than at the first policy query.
	tests := []struct {
		name   string
		mutate func(*API)
		want   string
	}{
		{"no id", func(a *API) { a.ID = " " }, "api id is required"},
		{"no name", func(a *API) { a.Name = "" }, "api name is required"},
		{"no format", func(a *API) { a.Format = " " }, "has no format"},
		{"blank server id", func(a *API) { a.ServerIDs = []string{" "} }, "server id cannot be empty"},
		{"duplicate server", func(a *API) { a.ServerIDs = []string{"prod", "prod"} }, "referenced more than once"},
		{"no services", func(a *API) { a.Services = nil }, "declares no services"},
		{"unnamed service", func(a *API) { a.Services[0].Name = " " }, "service name is required"},
		{
			"malformed operation",
			func(a *API) { a.Services[0].Operations[0].Name = "" },
			"operation name is required",
		},
		{
			"duplicate service name",
			func(a *API) { a.Services = append(a.Services, a.Services[0]) },
			"is declared more than once",
		},
		{
			"service id that is not derivable",
			func(a *API) { a.Services[0].ID = "somewhere/else" },
			"expected \"shop/pets\"",
		},
		{
			"operation id that is not derivable",
			func(a *API) { a.Services[0].Operations[0].ID = "somewhere/else" },
			"expected \"shop/pets/getPet\"",
		},
		{
			"operation claiming another api",
			func(a *API) { a.Services[0].Operations[0].APIID = "other" },
			"does not belong to",
		},
		{
			"operation claiming another service",
			func(a *API) { a.Services[0].Operations[0].Service = "other" },
			"does not belong to",
		},
		{
			// A service name padded differently trims to the same identifier, so two
			// services can pass the name check and still address one operation.
			"two operations sharing one identifier",
			func(a *API) {
				a.Services = append(a.Services, Service{
					Name: " pets ",
					Operations: []Operation{{
						Name:    "getPet",
						APIID:   "shop",
						Service: " pets ",
						ID:      "shop/pets/getPet",
					}},
				})
			},
			"is used more than once",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			described := modelAPI()
			test.mutate(&described)
			err := described.Validate()
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalid)
			assert.Contains(t, err.Error(), test.want)
		})
	}

	assert.NoError(t, modelAPI().Validate())
}

func TestModelDescriptorValidateRequiresAName(t *testing.T) {
	// A descriptor with an identifier but no name is unreadable to a person, and a
	// listing of formats is exactly what a person reads.
	err := FormatDescriptor{ID: "openapi"}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no name")

	err = TransportDescriptor{ID: "http"}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no name")

	require.Error(t, FormatDescriptor{}.Validate())
	require.Error(t, TransportDescriptor{}.Validate())
}

func TestModelValidationErrorNamesTheViolation(t *testing.T) {
	// A caller matches the sentinel and still reports the text, so both have to be
	// right: a message without its path sends a person looking through a whole API.
	var absent *ValidationError
	assert.Equal(t, ErrInvalid.Error(), absent.Error(),
		"a nil error still reports the sentinel, because a caller formats what it was handed")
	assert.ErrorIs(t, absent, ErrInvalid)

	unlocated := invalid("", "nothing to describe")
	assert.Equal(t, "invalid api description: nothing to describe", unlocated.Error(),
		"a violation outside any path does not emit a dangling separator")

	located := invalid("api.services[0]", "service name is required")
	assert.Equal(t, "invalid api description: api.services[0]: service name is required", located.Error())
	assert.True(t, errors.Is(located, ErrInvalid))
}

func TestModelServerNormalizeRefusesAnAbsentServer(t *testing.T) {
	// A normalization step that silently accepted nil would store nothing and
	// report success, and the catalog would list a server it cannot describe.
	var absent *Server
	require.Error(t, absent.Normalize(time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)))
}

func TestModelAPINormalizeStoresTheTrimmedDescription(t *testing.T) {
	// The catalog keys an API by its identifier and its policies by the operation
	// identifiers built from it, so what normalization drops has to be dropped
	// everywhere at once — and a summary is never left empty when a description
	// exists to fall back on.
	described := modelAPI()
	described.Name = " Shop "
	described.Title = " The shop "
	described.Version = " 1.0 "
	described.Source.Kind = " document "
	described.Source.Location = " ./openapi.yaml "
	described.ServerIDs = []string{" prod ", "prod", " ", ""}
	described.Tags = []string{" pets ", "pets", " ", ""}
	operation := &described.Services[0].Operations[0]
	operation.Description = "  Fetch one pet.  "
	operation.Summary = "   "

	normalized, err := described.Normalize()
	require.NoError(t, err)

	assert.Equal(t, "Shop", normalized.Name)
	assert.Equal(t, "The shop", normalized.Title)
	assert.Equal(t, "1.0", normalized.Version)
	assert.Equal(t, "document", normalized.Source.Kind)
	assert.Equal(t, "./openapi.yaml", normalized.Source.Location)
	assert.Equal(t, []string{"prod"}, normalized.ServerIDs, "a repeated server is referenced once")
	assert.Equal(t, []string{"pets"}, normalized.Tags)
	assert.Equal(t, "Fetch one pet.", normalized.Services[0].Operations[0].Summary,
		"a missing summary falls back to the description, so a listing is never blank")
	assert.Equal(t, "core", normalized.Metadata["team"])

	assert.Equal(t, " Shop ", described.Name, "normalization returns a new value rather than editing the caller's")
	assert.Equal(t, []string{" pets ", "pets", " ", ""}, described.Tags)
}

func TestModelAPINormalizeDropsListsThatOnlyHoldBlanks(t *testing.T) {
	// An empty tag list and an absent one mean the same thing, and the digest of a
	// description must not depend on which the parser happened to produce.
	described := modelAPI()
	described.Tags = []string{" ", ""}
	described.ServerIDs = nil
	normalized, err := described.Normalize()
	require.NoError(t, err)
	assert.Nil(t, normalized.Tags)
	assert.Nil(t, normalized.ServerIDs)
}

func TestModelAPINormalizeRefusesAnAbsentAPI(t *testing.T) {
	// The registrar calls Normalize on whatever it was handed. A nil that reported
	// success would register an empty API under no identifier.
	var absent *API
	_, err := absent.Normalize()
	require.Error(t, err)
}

func TestModelPatternReportsHowSpecificItIs(t *testing.T) {
	// Specificity is what decides which rule a policy consults first, so the
	// measurement has to be the number of segments named exactly.
	tests := []struct {
		source  string
		literal int
		total   int
	}{
		{"knowledge/toolbox.v1.Knowledge/Search", 3, 3},
		{"knowledge/*/Search", 2, 3},
		{" knowledge/** ", 1, 2},
		{"*", 0, 1},
	}

	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			pattern, err := CompilePattern(test.source)
			require.NoError(t, err)
			assert.Equal(t, test.literal, pattern.Literal())
			assert.Equal(t, test.total, pattern.Total())
			assert.Equal(t, strings.TrimSpace(test.source), pattern.String(),
				"a compiled pattern reports itself the way a policy report quotes it")
		})
	}
}

func TestModelPatternRefusesAMeaninglessPattern(t *testing.T) {
	// Each row is a pattern whose meaning a person would guess and the compiler
	// would have to guess differently: a rule that silently matched nothing would
	// leave an operation exposed, and one that matched everything would grant it.
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"empty", "", "needs a pattern"},
		{"blank", "   ", "needs a pattern"},
		{"empty segment", "knowledge//Search", "empty segment"},
		{"rest in the middle", "knowledge/**/Search", "before its last segment"},
		{"wildcard mixed with text", "knowledge/Sear*", "mixes a wildcard"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompilePattern(test.source)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.want)
		})
	}
}

func TestModelPatternMatchNeedsTheArityItWasWrittenWith(t *testing.T) {
	// A pattern of exact segments must not reach deeper than it names, or
	// "knowledge/*/Search" would also cover "knowledge/*/Search/Delete".
	pattern, err := CompilePattern("knowledge/*/Search")
	require.NoError(t, err)
	assert.True(t, pattern.Match("knowledge/toolbox.v1.Knowledge/Search"))
	assert.False(t, pattern.Match("knowledge/toolbox.v1.Knowledge/Search/Delete"))
	assert.False(t, pattern.Match("knowledge/Search"))

	rest, err := CompilePattern("knowledge/**")
	require.NoError(t, err)
	assert.True(t, rest.Match("knowledge/toolbox.v1.Knowledge/Search/Delete"))
	assert.False(t, rest.Match("knowledge"), "a trailing rest still needs the segments before it")
	assert.False(t, rest.Match("other/toolbox.v1.Knowledge/Search"))
}

func TestModelSideEffectsReportsWhatItCouldNotClassify(t *testing.T) {
	// The vocabulary is open, so an unknown value must come back as a finding
	// rather than vanish: a silently unclassified operation is one no policy can
	// expose, and nobody would know why.
	_, annotations := shareddocs.ParseAnnotations("@toolbox.side-effects quantum_entanglement")
	effects, unknown := SideEffects(annotations)
	assert.Nil(t, effects)
	assert.Equal(t, []string{"quantum_entanglement"}, unknown,
		"an unknown value is reported, because the operation is now unclassified")

	_, annotations = shareddocs.ParseAnnotations("@toolbox.side-effects update read_only create")
	effects, unknown = SideEffects(annotations)
	assert.Equal(t, []SideEffect{SideEffectCreate, SideEffectReadOnly, SideEffectUpdate}, effects,
		"declared effects are sorted so a report about them is stable")
	assert.Empty(t, unknown)

	_, annotations = shareddocs.ParseAnnotations("@toolbox.side-effects create create create")
	effects, unknown = SideEffects(annotations)
	assert.Equal(t, []SideEffect{SideEffectCreate}, effects, "a repeated declaration is one effect")
	assert.Empty(t, unknown)

	_, annotations = shareddocs.ParseAnnotations("no annotation here")
	effects, unknown = SideEffects(annotations)
	assert.Nil(t, effects, "a method that declared nothing has no effects, which is unknown rather than safe")
	assert.Nil(t, unknown)
	assert.Empty(t, UnknownAnnotations(annotations), "a comment with no annotation has no unrecognised key")
}

func TestModelKnownSideEffectRejectsATypoAndAcceptsAnExtension(t *testing.T) {
	// These two questions are different: whether this library documents an effect
	// is not whether an effect is consequential. A typo is rejected, and an
	// unrecognised name is still preserved by normalization.
	for _, effect := range []SideEffect{
		SideEffectReadOnly, SideEffectCreate, SideEffectUpdate, SideEffectDelete,
		SideEffectExternal, SideEffectIrreversible, SideEffectFinancial,
	} {
		assert.True(t, KnownSideEffect(effect), "effect %q is documented", effect)
	}
	assert.False(t, KnownSideEffect("readonl"))
	assert.False(t, KnownSideEffect(""))
}
