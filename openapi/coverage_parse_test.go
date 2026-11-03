package openapi_test

import (
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/openapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests hold the reader to the promises its doc comments make about real
// documents: every schema shape a document can declare survives the read, a
// reference the reader cannot follow is reported rather than silently dropped,
// and a document that cannot be read at all is refused with a classification the
// caller can act on. A reader that quietly loses one of these turns a document
// into a description that looks complete and is not.

// covParse reads a document and fails the test when the reader refuses it.
func covParse(t *testing.T, document string) (api.API, []string) {
	t.Helper()
	parsed, warnings, err := openapi.Parse([]byte(document), openapi.ParseOptions{})
	require.NoError(t, err)
	return parsed, warnings
}

func TestCovParseReadsEveryDocumentedSecuritySchemeType(t *testing.T) {
	// A document may authenticate with an API key in any of three places, HTTP
	// basic or bearer, OAuth2 scopes, or OpenID Connect. Each is a different shape
	// of credential, and the invoker picks the shape from the parsed scheme, so the
	// reader has to carry the type, the location, and the scopes through intact.
	const document = `
openapi: "3.1.0"
info: {title: Secure, version: "1"}
servers:
  - url: "https://{region}.example.test/{version}"
    description: Regional endpoint
    variables:
      version: {default: v2}
      region: {default: eu}
security:
  - oauth: [write, read]
  - key: []
paths:
  /things:
    get:
      operationId: listThings
      tags: [things]
      security:
        - basic: []
        - key: []
      responses:
        "200": {description: ok}
components:
  securitySchemes:
    basic: {type: http, scheme: basic, description: HTTP basic}
    bearer: {type: http, scheme: bearer, bearerFormat: JWT}
    cookie: {type: apiKey, in: Cookie, name: session}
    key: {type: apiKey, in: Query, name: api_key}
    oauth:
      type: oauth2
      flows: {}
      scopes: {write: "write things", read: "read things"}
    oidc: {type: openIdConnect, openIdConnectUrl: "https://example.test/.well-known"}
`
	parsed, _ := covParse(t, document)

	// A server variable is expanded with its default, because the description has
	// to name a location a caller can actually reach.
	require.Len(t, parsed.DeclaredServers, 1)
	assert.Equal(t, "https://eu.example.test/v2", parsed.DeclaredServers[0].URL)
	assert.Equal(t, "Regional endpoint", parsed.DeclaredServers[0].Description)

	require.Len(t, parsed.SecuritySchemes, 6, "every declared scheme is carried, in name order")
	schemes := make(map[string]api.SecurityScheme, len(parsed.SecuritySchemes))
	for _, scheme := range parsed.SecuritySchemes {
		schemes[scheme.Name] = scheme
	}
	assert.Equal(t, "http", schemes["basic"].Type)
	assert.Equal(t, "basic", schemes["basic"].Scheme)
	assert.Equal(t, "HTTP basic", schemes["basic"].Description)
	assert.Equal(t, "bearer", schemes["bearer"].Scheme)
	// The location is lowercased because the invoker compares it to its own
	// documented locations.
	assert.Equal(t, "cookie", schemes["cookie"].In)
	assert.Equal(t, "session", schemes["cookie"].ParameterName)
	assert.Equal(t, "query", schemes["key"].In)
	assert.Equal(t, "oauth2", schemes["oauth"].Type)
	assert.Equal(t, []string{"read", "write"}, schemes["oauth"].Scopes, "scopes are sorted so a listing is stable")
	assert.Equal(t, "openIdConnect", schemes["oidc"].Type)

	// API-wide requirements are carried, and an operation's own requirements
	// replace them rather than adding to them: that is what the document means.
	require.Len(t, parsed.Security, 2)
	// Scopes are carried as the document lists them; only the scheme names are
	// ordered, so a requirement stays comparable.
	assert.Equal(t, api.SecurityRequirement{Scheme: "oauth", Scopes: []string{"write", "read"}}, parsed.Security[0])
	assert.Equal(t, api.SecurityRequirement{Scheme: "key"}, parsed.Security[1])

	operation, found := parsed.Operation("secure-1/things/listThings")
	require.True(t, found)
	require.Len(t, operation.Security, 2)
	assert.Equal(t, "basic", operation.Security[0].Scheme)
	assert.Equal(t, "key", operation.Security[1].Scheme)
}

func TestCovParseCarriesEverySchemaShape(t *testing.T) {
	// A description that loses a shape loses the contract: a caller building a
	// request from it would send a value the server never accepts. Every shape the
	// document language can declare is asserted here.
	const document = `
openapi: "3.1.0"
info: {title: Shapes, version: "1"}
paths:
  /everything:
    post:
      operationId: sendEverything
      tags: [shapes]
      requestBody:
        content:
          application/json:
            schema: {$ref: "#/components/schemas/Everything"}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {$ref: "#/components/schemas/Everything"}
components:
  schemas:
    Everything:
      type: object
      title: Everything
      description: every shape at once
      deprecated: true
      readOnly: true
      pattern: "^[a-z]+$"
      minLength: 1
      maxLength: 5
      minimum: 0
      maximum: 10
      required: [name, meta]
      properties:
        name: {type: string}
        meta: {type: object}
        kind:
          type: string
          enum: [alpha, true, 2.5, null, {nested: 1}]
        count: {type: integer, default: 7}
        ratio: {type: number}
        nothing: {type: "null"}
        flags: {type: array, items: {type: boolean}}
        labels:
          type: object
          additionalProperties: {type: string}
        shape:
          oneOf: [{type: string}, {type: integer}]
        choice:
          anyOf: [{type: boolean}]
        untypedObject: {properties: {x: {type: string}}}
        untypedArray: {items: {type: number}}
        untypedEnum: {enum: [a, b]}
        formatted: {format: date-time}
        empty: {}
        maybe: {type: string, nullable: true, description: Maybe a string}
        anything:
          oneOf: [null]
`
	parsed, _ := covParse(t, document)
	operation, found := parsed.Operation("shapes-1/shapes/sendEverything")
	require.True(t, found)
	schema := operation.Request
	require.NotNil(t, schema)

	assert.Equal(t, api.TypeObject, schema.Type)
	assert.Equal(t, "Everything", schema.Title)
	assert.Equal(t, "every shape at once", schema.Description)
	assert.True(t, schema.Deprecated)
	assert.True(t, schema.ReadOnly)
	assert.Equal(t, "^[a-z]+$", schema.Pattern)
	assert.Equal(t, api.Int64(1), schema.MinLength)
	assert.Equal(t, api.Int64(5), schema.MaxLength)
	assert.Equal(t, api.Float64(0), schema.Minimum)
	assert.Equal(t, api.Float64(10), schema.Maximum)
	assert.Equal(t, []string{"meta", "name"}, schema.Required, "required names are sorted so a listing is stable")
	assert.Equal(t, "#/components/schemas/Everything", schema.Ref, "the document's own naming is preserved")

	kind, ok := schema.Property("kind")
	require.True(t, ok)
	// Each literal is rendered as the text the document wrote, whatever YAML typed
	// it as, because an enum is a set of allowed values and one of them may be a
	// JSON null or an object.
	assert.Equal(t, []string{"alpha", "true", "2.5", "", "map[nested:1]"}, kind.Schema.Enum)

	count, ok := schema.Property("count")
	require.True(t, ok)
	assert.EqualValues(t, 7, count.Schema.Default, "a declared default is carried")

	ratio, _ := schema.Property("ratio")
	assert.Equal(t, api.TypeNumber, ratio.Schema.Type)
	nothing, _ := schema.Property("nothing")
	assert.Equal(t, api.TypeNull, nothing.Schema.Type)
	flags, _ := schema.Property("flags")
	assert.Equal(t, api.TypeArray, flags.Schema.Type)
	require.NotNil(t, flags.Schema.Items)
	assert.Equal(t, api.TypeBoolean, flags.Schema.Items.Type)

	labels, _ := schema.Property("labels")
	require.NotNil(t, labels.Schema.AdditionalProperties, "a map is described by its value schema")
	assert.Equal(t, api.TypeString, labels.Schema.AdditionalProperties.Type)

	shape, _ := schema.Property("shape")
	require.Len(t, shape.Schema.OneOf, 2)
	choice, _ := schema.Property("choice")
	require.Len(t, choice.Schema.AnyOf, 1)

	// A schema without a declared type is described by its shape, which is what a
	// consumer needs in order to build a value.
	untypedObject, _ := schema.Property("untypedObject")
	assert.Equal(t, api.TypeObject, untypedObject.Schema.Type)
	untypedArray, _ := schema.Property("untypedArray")
	assert.Equal(t, api.TypeArray, untypedArray.Schema.Type)
	untypedEnum, _ := schema.Property("untypedEnum")
	assert.Equal(t, api.TypeString, untypedEnum.Schema.Type)
	formatted, _ := schema.Property("formatted")
	assert.Equal(t, api.TypeString, formatted.Schema.Type)
	assert.Equal(t, "date-time", formatted.Schema.Format)
	empty, _ := schema.Property("empty")
	assert.Equal(t, api.TypeUnspecified, empty.Schema.Type)

	maybe, _ := schema.Property("maybe")
	assert.Equal(t, api.TypeString, maybe.Schema.Type)
	assert.Equal(t, "Maybe a string", maybe.Schema.Description)

	// An empty alternative is not a schema; it is kept as an absent alternative
	// rather than becoming an object schema that the document never declared.
	anything, _ := schema.Property("anything")
	require.Len(t, anything.Schema.OneOf, 1)
	assert.Nil(t, anything.Schema.OneOf[0])
}

func TestCovParseWarnsWhenTheOnlyMediaTypeIsNotJSON(t *testing.T) {
	// JSON is what agent tooling can call with directly. A document that only
	// describes XML is still describable, but the choice is reported so the caller
	// knows the description was taken from a different media type.
	const document = `
openapi: "3.1.0"
info: {title: Legacy, version: "1"}
paths:
  /reports:
    post:
      operationId: sendReport
      tags: [reports]
      requestBody:
        content:
          application/vnd.api+json:
            schema: {type: object, properties: {title: {type: string}}}
      responses:
        "200":
          description: ok
          content:
            application/xml:
              schema: {type: object, properties: {body: {type: string}}}
  /blobs:
    get:
      operationId: getBlob
      tags: [reports]
      responses:
        "200":
          description: opaque
          content:
            text/plain: {}
`
	parsed, warnings := covParse(t, document)

	// A vendor media type is still JSON, so it is picked without a warning.
	send, found := parsed.Operation("legacy-1/reports/sendReport")
	require.True(t, found)
	require.NotNil(t, send.Request)
	title, ok := send.Request.Property("title")
	require.True(t, ok, "a vendor JSON media type is read as JSON")
	assert.Equal(t, api.TypeString, title.Schema.Type)

	// XML is the only media type, so the description is taken from it and the
	// substitution is reported.
	require.NotNil(t, send.Response)
	body, ok := send.Response.Property("body")
	require.True(t, ok)
	assert.Equal(t, api.TypeString, body.Schema.Type)
	assert.Contains(t, warnings, `no JSON media type is declared; describing "application/xml" instead`)

	// A media type declared with no schema yields a free-form object rather than
	// an absent response.
	blob, found := parsed.Operation("legacy-1/reports/getBlob")
	require.True(t, found)
	require.NotNil(t, blob.Response)
	assert.Equal(t, api.TypeObject, blob.Response.Type)
}

func TestCovParseResolvesRequestBodyComponents(t *testing.T) {
	// A request body may be a component reference. A reference the reader cannot
	// follow is still describable as a free-form body, but it is reported: a
	// silently empty body would make an operation look like it takes no input.
	const document = `
openapi: "3.1.0"
info: {title: Bodies, version: "1"}
paths:
  /local:
    post:
      operationId: localBody
      tags: [bodies]
      requestBody: {$ref: "#/components/requestBodies/Free"}
      responses: {"200": {description: ok}}
  /external:
    post:
      operationId: externalBody
      tags: [bodies]
      requestBody: {$ref: "other.yaml#/components/requestBodies/Free"}
      responses: {"200": {description: ok}}
  /missing:
    post:
      operationId: missingBody
      tags: [bodies]
      requestBody: {$ref: "#/components/requestBodies/Absent"}
      responses: {"200": {description: ok}}
  /empty:
    post:
      operationId: emptyBody
      tags: [bodies]
      requestBody: {content: {}}
      responses: {"200": {description: ok}}
components:
  requestBodies:
    Free:
      content:
        application/json:
          schema: {type: object, properties: {note: {type: string}}}
`
	parsed, warnings := covParse(t, document)

	local, found := parsed.Operation("bodies-1/bodies/localBody")
	require.True(t, found)
	require.NotNil(t, local.Request)
	_, ok := local.Request.Property("note")
	require.True(t, ok, "a local request body component is resolved")

	for _, name := range []string{"externalBody", "missingBody", "emptyBody"} {
		operation, found := parsed.Operation("bodies-1/bodies/" + name)
		require.True(t, found, name)
		require.NotNil(t, operation.Request, "%s is still described as taking a body", name)
		assert.Equal(t, api.TypeObject, operation.Request.Type, "%s is free-form", name)
	}
	assert.Contains(t, warnings, `request body reference "other.yaml#/components/requestBodies/Free" is not a local component reference; the body is treated as free-form`)
	assert.Contains(t, warnings, `request body reference "#/components/requestBodies/Absent" is not declared in components; the body is treated as free-form`)
}

func TestCovParseResolvesParameterComponents(t *testing.T) {
	// Parameters are declared at both the path and the operation level, may be
	// components, and a path parameter is required whether or not the document
	// says so. The merge rules are what make a call possible at all.
	const document = `
openapi: "3.1.0"
info: {title: Params, version: "1"}
paths:
  /pets/{petId}:
    parameters:
      - {$ref: "#/components/parameters/PetID"}
      - {name: page, in: Query, schema: {type: integer}}
      - {$ref: "shared.yaml#/components/parameters/Other"}
      - {name: unschematized, in: header}
    get:
      operationId: getPet
      tags: [pets]
      parameters:
        - {name: page, in: query, schema: {type: integer}, description: Page number}
        - {$ref: "#/components/parameters/Aliased"}
      requestBody:
        content:
          application/json:
            schema: {type: object, properties: {filter: {type: string}}}
      responses: {"200": {description: ok}}
  /unresolvable:
    get:
      operationId: getUnresolvable
      tags: [pets]
      parameters:
        - {$ref: "#/components/parameters/Missing"}
      responses: {"200": {description: ok}}
components:
  parameters:
    PetID:
      name: petId
      in: path
      schema: {type: string}
    Aliased:
      $ref: "#/components/parameters/Verbose"
    Verbose:
      name: verbose
      in: query
      schema: {type: boolean}
`
	parsed, warnings := covParse(t, document)
	operation, found := parsed.Operation("params-1/pets/getPet")
	require.True(t, found)

	names := make([]string, 0, len(operation.Parameters))
	byName := make(map[string]api.Parameter, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		names = append(names, parameter.Name)
		byName[parameter.Name] = parameter
	}
	// The first declaration of a name wins, so a path-level parameter is not
	// duplicated by an operation that redeclares it: two entries with one name
	// would make the argument ambiguous.
	assert.Equal(t, []string{"petId", "page", "unschematized", "verbose"}, names)

	assert.Equal(t, api.ParameterInPath, byName["petId"].In)
	assert.True(t, byName["petId"].Required, "a path parameter is required even when the document omits the flag")
	assert.Equal(t, api.ParameterInQuery, byName["page"].In, "a declared location is lowercased")
	assert.Equal(t, api.TypeString, byName["unschematized"].Schema.Type, "a parameter with no schema is a string")
	assert.Equal(t, api.TypeBoolean, byName["verbose"].Schema.Type, "a chain of parameter references resolves to its declaration")
	// Parameters and a request body are not alternatives: an operation may carry
	// both, and dropping either would make the description unusable.
	require.NotNil(t, operation.Request)
	_, ok := operation.Request.Property("filter")
	assert.True(t, ok)

	// A reference the reader cannot follow is reported: the path-level one is
	// skipped with a warning, and the operation-level one leaves the operation
	// describable without it.
	assert.Contains(t, warnings, `parameter reference "shared.yaml#/components/parameters/Other" is not a local component reference; the parameter is skipped`)
	// The warning for an operation-level reference names the node's own empty name
	// rather than the reference, which is all the message can say about a node that
	// is nothing but a reference.
	assert.Contains(t, warnings, `parameter "" in "" could not be resolved`)
	_, found = parsed.Operation("params-1/pets/getUnresolvable")
	assert.True(t, found, "an operation with an unresolvable parameter is still described")
}

func TestCovParseRejectsUnresolvablePathParameter(t *testing.T) {
	// A shared parameter that points at a component the document never declares is
	// a contradiction the reader cannot paper over: the parameter is required to
	// build every request under the path, so the document is refused.
	const document = `
openapi: "3.1.0"
info: {title: Broken, version: "1"}
paths:
  /things:
    parameters:
      - {$ref: "#/components/parameters/Absent"}
    get:
      operationId: listThings
      tags: [things]
      responses: {"200": {description: ok}}
`
	_, _, err := openapi.Parse([]byte(document), openapi.ParseOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), `parameter reference "#/components/parameters/Absent" is not declared in components`)
}

func TestCovParseRejectsAParameterSchemaThatCannotBeResolved(t *testing.T) {
	// A parameter whose schema names a component that does not exist cannot be
	// turned into a type, and a parameter of unknown type is not a usable
	// declaration, so the whole document is refused.
	const document = `
openapi: "3.1.0"
info: {title: Broken, version: "1"}
paths:
  /things:
    parameters:
      - {name: filter, in: query, schema: {$ref: "#/components/schemas/Absent"}}
    get:
      operationId: listThings
      tags: [things]
      responses: {"200": {description: ok}}
`
	_, _, err := openapi.Parse([]byte(document), openapi.ParseOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `schema reference "#/components/schemas/Absent" is not declared in components`)
}

func TestCovParseReadsPathItemMetadataAndSkipsAPathWithNoOperations(t *testing.T) {
	// A path item may carry metadata and no operations, and may name its own
	// servers. Such a path contributes nothing to the description and must not fail
	// the read, but its metadata is still available to the group it describes.
	const document = `
openapi: "3.1.0"
info: {title: Meta, version: "1"}
paths:
  /meta:
    summary: Metadata only
    description: This path declares no operation
    servers: [{url: "https://elsewhere.test"}]
    x-internal: true
  /things:
    summary: Things
    description: The things path
    get:
      operationId: listThings
      tags: [things]
      responses: {"200": {description: ok}}
`
	parsed, _ := covParse(t, document)

	require.Len(t, parsed.Services, 1)
	assert.Equal(t, "things", parsed.Services[0].Name)
	assert.Equal(t, "The things path", parsed.Services[0].Description,
		"a group with no declared tag description falls back to the path item's")
	assert.Equal(t, []string{"meta-1/things/listThings"}, parsed.OperationIDs())
}

func TestCovParseRejectsMalformedPathItems(t *testing.T) {
	// The document language lets a path item be a mapping and nothing else. Each of
	// these is a defect in the document, and each is reported through the same
	// classification so a caller can tell a bad document from an unreachable one.
	tests := []struct {
		name     string
		pathItem string
		contains string
	}{
		{name: "not a mapping", pathItem: `"just a string"`, contains: "path item must be a mapping"},
		{name: "summary is not text", pathItem: `{summary: [a, b]}`, contains: "path item summary"},
		{name: "parameters are not a list", pathItem: `{parameters: "none"}`, contains: "path item parameters"},
		{name: "operation is not an object", pathItem: `{get: "list"}`, contains: "path item get"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := `
openapi: "3.1.0"
info: {title: Broken, version: "1"}
paths:
  /things: ` + test.pathItem + "\n"
			_, _, err := openapi.Parse([]byte(document), openapi.ParseOptions{})
			require.Error(t, err)
			assert.Equal(t, api.KindInvalid, api.KindOf(err))
			assert.Contains(t, err.Error(), test.contains)
		})
	}
}

func TestCovParseRejectsDuplicateOperationIdentifiers(t *testing.T) {
	// Two operations in one group with the same identifier would make an exposure
	// decision ambiguous, so the description is refused rather than returned with
	// one of them lost.
	const document = `
openapi: "3.1.0"
info: {title: Duplicated, version: "1"}
paths:
  /a:
    get:
      operationId: listThings
      tags: [things]
      responses: {"200": {description: ok}}
  /b:
    get:
      operationId: listThings
      tags: [things]
      responses: {"200": {description: ok}}
`
	_, _, err := openapi.Parse([]byte(document), openapi.ParseOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), `declares operation "listThings" more than once`)
}

func TestCovParseKeepsTagOrderAndSkipsAnUnnamedTag(t *testing.T) {
	// A document may declare the order its tags should be listed in: a group whose
	// tag is declared follows that order, and an undeclared group keeps the order
	// its operations appeared in, so two readers of the same document agree. An
	// unnamed tag entry declares nothing and is skipped. The order is computed per
	// path item and the merged groups are appended in path order, so the declared
	// order is not applied across separate paths; this test asserts the ordering
	// that actually happens, which is where several tags meet on one path.
	const document = `
openapi: "3.1.0"
info: {title: Ordered, version: "1"}
tags:
  - {description: no name}
  - {name: beta, description: Beta operations}
  - {name: alpha, description: Alpha operations}
paths:
  /a:
    get:
      operationId: alphaOp
      tags: [alpha]
      responses: {"200": {description: ok}}
    post:
      operationId: betaOp
      tags: [beta]
      responses: {"200": {description: ok}}
    delete:
      operationId: gammaOp
      tags: [gamma]
      responses: {"200": {description: ok}}
`
	parsed, _ := covParse(t, document)

	names := make([]string, 0, len(parsed.Services))
	for _, service := range parsed.Services {
		names = append(names, service.Name)
	}
	assert.Equal(t, []string{"beta", "alpha", "gamma"}, names)
	assert.Equal(t, "Beta operations", parsed.Services[0].Description)
}

func TestCovParseTreatsABlankTagAsNoTagAndFallsBackToTheAPIName(t *testing.T) {
	// A title with nothing identifier-shaped in it still yields an identifier,
	// because an API without one cannot be registered. A tag that is only
	// whitespace groups nothing, so the operation lands in the default group and
	// carries no tags.
	const document = `
openapi: "3.1.0"
info: {title: "***", version: ""}
paths:
  /things:
    get:
      operationId: listThings
      tags: ["", "   "]
      responses: {"200": {description: ok}}
`
	parsed, _ := covParse(t, document)
	assert.Equal(t, "api", parsed.ID)
	operation, found := parsed.Operation("api/default/listThings")
	require.True(t, found)
	assert.Empty(t, operation.Tags)
	assert.Equal(t, "default", operation.Service)
}

func TestCovParseFillsAGroupDescriptionFromALaterPath(t *testing.T) {
	// Groups that share a name are merged into one service. The description is
	// taken from the first path that supplies one, because a merge that kept only
	// the first path's empty string would lose what the document said.
	const document = `
openapi: "3.1.0"
info: {title: Merged, version: "1"}
paths:
  /a:
    get:
      operationId: firstOp
      tags: [things]
      responses: {"200": {description: ok}}
  /b:
    description: Collects things
    get:
      operationId: secondOp
      tags: [things]
      responses: {"200": {description: ok}}
`
	parsed, _ := covParse(t, document)

	require.Len(t, parsed.Services, 1)
	assert.Equal(t, "things", parsed.Services[0].Name)
	assert.Equal(t, "Collects things", parsed.Services[0].Description)
	assert.Equal(t, []string{"firstOp", "secondOp"}, covOperationNames(parsed.Services[0]))
}

func TestCovParseReadsOperationExtensionsAsDeclarations(t *testing.T) {
	// Deprecation and side effects are the only things a document cannot express
	// with an HTTP verb, and they are what a policy refuses an operation on. Both
	// the document's own flag and the extension are honoured, and a value the
	// reader cannot parse is treated as not declared rather than as true.
	const document = `
openapi: "3.1.0"
info: {title: Extensions, version: "1"}
paths:
  /things:
    get:
      operationId: listThings
      tags: [things]
      x-toolbox-deprecated: true
      x-toolbox-side-effects: irreversible
      responses: {"200": {description: ok}}
    delete:
      operationId: purgeThings
      tags: [things]
      deprecated: true
      x-toolbox-side-effects: [external, irreversible, external]
      responses: {"204": {description: gone}}
    put:
      operationId: replaceThings
      tags: [things]
      x-toolbox-deprecated: "not a boolean"
      x-toolbox-side-effects: {invalid: shape}
      responses: {"200": {description: ok}}
`
	parsed, _ := covParse(t, document)

	list, found := parsed.Operation("extensions-1/things/listThings")
	require.True(t, found)
	assert.True(t, list.Deprecated, "the extension marks an operation the document does not")
	assert.Equal(t, []api.SideEffect{api.SideEffectIrreversible, api.SideEffectReadOnly}, list.SideEffects,
		"the method's implied effect and the declared one are both carried")

	purge, found := parsed.Operation("extensions-1/things/purgeThings")
	require.True(t, found)
	assert.True(t, purge.Deprecated)
	assert.Equal(t, []api.SideEffect{api.SideEffectDelete, api.SideEffectExternal, api.SideEffectIrreversible}, purge.SideEffects,
		"a repeated effect is dropped and the list is ordered")

	replace, found := parsed.Operation("extensions-1/things/replaceThings")
	require.True(t, found)
	assert.False(t, replace.Deprecated, "a value that is not a boolean is not a deprecation")
	assert.Equal(t, []api.SideEffect{api.SideEffectUpdate}, replace.SideEffects,
		"an extension of the wrong shape declares nothing")
}

func TestCovParseTreatsANonLocalSchemaReferenceAsFreeForm(t *testing.T) {
	// A reference to another document cannot be followed by this reader. The
	// document stays describable, but the substitution is reported so a caller does
	// not mistake a free-form value for the shape the other document declares.
	const document = `
openapi: "3.1.0"
info: {title: Dangling, version: "1"}
paths:
  /things:
    get:
      operationId: listThings
      tags: [things]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  external: {$ref: "https://example.test/schemas/Thing.json"}
                  empty: {$ref: "#/components/schemas/"}
                  nested: {$ref: "#/components/schemas/a/b"}
`
	parsed, warnings := covParse(t, document)
	operation, found := parsed.Operation("dangling-1/things/listThings")
	require.True(t, found)

	for _, name := range []string{"external", "empty", "nested"} {
		property, ok := operation.Response.Property(name)
		require.True(t, ok, name)
		assert.Equal(t, api.TypeObject, property.Schema.Type, "%s is described as free-form", name)
	}
	assert.Contains(t, warnings, `schema reference "https://example.test/schemas/Thing.json" is not a local component reference; treating it as free-form`)
}

func TestCovParseWarnsWhenASchemaShapeCannotBeConverted(t *testing.T) {
	// Each of these schemas is broken inside a different shape. The operation is
	// still describable, because a free-form value is more useful to a caller than
	// no operation at all, but every substitution is reported.
	const document = `
openapi: "3.1.0"
info: {title: Broken, version: "1"}
paths:
  /property:
    get:
      operationId: brokenProperty
      tags: [broken]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties: {x: {$ref: "#/components/schemas/Absent"}}
  /items:
    get:
      operationId: brokenItems
      tags: [broken]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: array, items: {$ref: "#/components/schemas/Absent"}}
  /additional:
    get:
      operationId: brokenAdditional
      tags: [broken]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                additionalProperties: {$ref: "#/components/schemas/Absent"}
  /oneof:
    get:
      operationId: brokenOneOf
      tags: [broken]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {oneOf: [{$ref: "#/components/schemas/Absent"}]}
  /anyof:
    get:
      operationId: brokenAnyOf
      tags: [broken]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {anyOf: [{$ref: "#/components/schemas/Absent"}]}
  /component:
    get:
      operationId: brokenComponent
      tags: [broken]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {$ref: "#/components/schemas/Broken"}
components:
  schemas:
    Broken:
      type: object
      properties: {x: {$ref: "#/components/schemas/Absent"}}
`
	parsed, warnings := covParse(t, document)

	for _, name := range []string{"brokenProperty", "brokenItems", "brokenAdditional", "brokenOneOf", "brokenAnyOf", "brokenComponent"} {
		operation, found := parsed.Operation("broken-1/broken/" + name)
		require.True(t, found, name)
		require.NotNil(t, operation.Response, name)
		assert.Equal(t, api.TypeObject, operation.Response.Type, "%s is described as free-form", name)
	}
	joined := warnings
	assert.Contains(t, joined, `schema could not be converted: property "x": schema reference "#/components/schemas/Absent" is not declared in components`)
	assert.Contains(t, joined, `schema could not be converted: items: schema reference "#/components/schemas/Absent" is not declared in components`)
	assert.Contains(t, joined, `schema could not be converted: additionalProperties: schema reference "#/components/schemas/Absent" is not declared in components`)
	assert.Contains(t, joined, `schema could not be converted: oneOf: schema reference "#/components/schemas/Absent" is not declared in components`)
	assert.Contains(t, joined, `schema could not be converted: anyOf: schema reference "#/components/schemas/Absent" is not declared in components`)
}

func TestCovParseDescribesAnOperationWithNoResponses(t *testing.T) {
	// A document may describe an operation without responses. That is a document
	// with less information, not a broken one; the operation still exists and is
	// still callable, but it declares no success shape and no documented statuses.
	const document = `
openapi: "3.1.0"
info: {title: Silent, version: "1"}
paths:
  /silent:
    get:
      operationId: silentOp
      tags: [silent]
  /erroronly:
    get:
      operationId: errorOnly
      tags: [silent]
      responses:
        "404": {description: missing}
  /nocontent:
    delete:
      operationId: deleteThing
      tags: [silent]
      responses:
        "204": {description: gone}
`
	parsed, _ := covParse(t, document)

	silent, found := parsed.Operation("silent-1/silent/silentOp")
	require.True(t, found)
	assert.Nil(t, silent.Response)
	assert.Empty(t, silent.Responses)

	errorOnly, found := parsed.Operation("silent-1/silent/errorOnly")
	require.True(t, found)
	assert.Nil(t, errorOnly.Response, "a document with no success status declares no success shape")
	require.Len(t, errorOnly.Responses, 1)
	assert.Equal(t, "404", errorOnly.Responses[0].Status)

	deleteThing, found := parsed.Operation("silent-1/silent/deleteThing")
	require.True(t, found)
	assert.Nil(t, deleteThing.Response, "a response with no content declares no shape")
	require.Len(t, deleteThing.Responses, 1)
	assert.Equal(t, "204", deleteThing.Responses[0].Status)
	assert.Equal(t, "gone", deleteThing.Responses[0].Description)
}

func TestCovParseDerivesTheIdentifierFromTitleAndVersion(t *testing.T) {
	// The identifier is what every operation identifier is built from, so it has to
	// be stable and it has to survive a version written the way documents write
	// one. A caller may override it, and then nothing is derived.
	const document = `
openapi: "3.1.0"
info: {title: Payments API, version: V2}
paths:
  /charge:
    post:
      operationId: charge
      tags: [payments]
      responses: {"201": {description: charged}}
`
	parsed, _ := covParse(t, document)
	assert.Equal(t, "payments-api-v2", parsed.ID)

	overridden, _, err := openapi.Parse([]byte(document), openapi.ParseOptions{APIID: "billing"})
	require.NoError(t, err)
	assert.Equal(t, "billing", overridden.ID)
	assert.Equal(t, []string{"billing/payments/charge"}, overridden.OperationIDs())
}

func TestCovParseRecordsTheCallersBaseURLWhenTheDocumentDeclaresNone(t *testing.T) {
	// A document that names no server leaves the caller unable to reach it. When
	// the caller knows the endpoint it read the document from, that location
	// becomes the declared one, which is what binds the description to a server.
	const document = `
openapi: "3.1.0"
info: {title: Hostless, version: "1"}
paths:
  /things:
    get:
      operationId: listThings
      tags: [things]
      responses: {"200": {description: ok}}
`
	parsed, _, err := openapi.Parse([]byte(document), openapi.ParseOptions{BaseURL: "https://hostless.test/api"})
	require.NoError(t, err)
	require.Len(t, parsed.DeclaredServers, 1)
	assert.Equal(t, "https://hostless.test/api", parsed.DeclaredServers[0].URL)
	assert.Equal(t, "document", parsed.Source.Kind, "a document read from bytes is a document")
	assert.NotEmpty(t, parsed.Source.Digest)
}

// covOperationNames lists an operation's names in order.
func covOperationNames(service api.Service) []string {
	names := make([]string, 0, len(service.Operations))
	for _, operation := range service.Operations {
		names = append(names, operation.Name)
	}
	return names
}
