package openapi_test

import (
	"strings"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/openapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests hold three claims this package made and did not keep. Each one is a
// behaviour the package advertised in prose and contradicted in code, so each test
// is written to fail against the code that advertised it: the finding that never
// arrived, the document that was refused, the composition that disappeared.

// covParseDefect parses a document and returns the description, its findings, and
// the request schema of one operation, in the shape these tests compare against.
func covParseDefect(t *testing.T, document, operationID string) (api.API, []string, *api.Schema) {
	t.Helper()
	parsed, warnings, err := openapi.Parse([]byte(document), openapi.ParseOptions{APIID: "defects"})
	require.NoError(t, err, "a document that is valid OpenAPI has to be readable")
	operation, found := parsed.Operation(operationID)
	require.True(t, found, "the operation of %q is in the description", operationID)
	return parsed, warnings, operation.Request
}

// covJoin renders findings as one searchable string, so a failure prints everything
// the package said rather than only the phrase the test looked for.
func covJoin(warnings []string) string { return strings.Join(warnings, "\n") }

// TestCovDefectRenderKeepsItsFindings is the first defect: renderOpenAPI collected
// the mapping it derived for each operation and then handed the document to an
// encoder whose every return said "no warnings", so Rendered.Warnings was always
// empty.
//
// It mattered beyond a lost message. The findings are the package's only account of
// what it invented, and the conformance matrix reads them: a protobuf contract
// published as an OpenAPI document reported no findings at all while the document it
// produced carried a POST and a path that no contract declared. A caller that trusts
// an empty warning list trusts a document that pretends to be declared.
func TestCovDefectRenderKeepsItsFindings(t *testing.T) {
	// A description parsed from a protobuf contract: a method name, no HTTP verb,
	// no path. Rendering it is supported and derives both.
	described := api.API{
		ID:   "contract",
		Name: "contract",
		Services: []api.Service{{Name: "pets.v1.PetService", Operations: []api.Operation{
			{Name: "ListPets", Method: "ListPets", Service: "pets.v1.PetService"},
		}}},
	}
	rendered, err := openapi.Render(described, openapi.RenderOptions{})
	require.NoError(t, err)
	require.Len(t, rendered.Files, 1)

	// The document was written, so the derivation happened; the findings that say
	// so have to be there too, or the two halves disagree.
	assert.Contains(t, string(rendered.Files[0].Content), "/pet/ListPets")
	require.NotEmpty(t, rendered.Warnings, "a derived mapping is reported, not implied")
	joined := covJoin(rendered.Warnings)
	assert.Contains(t, joined, `declares method "ListPets", which is not an HTTP method; rendered as POST`)
	assert.Contains(t, joined, "declares no request path; rendered as /pet/ListPets")

	// An operation the writer had to omit is a finding as well: the document is
	// smaller than the description, and nothing else says so.
	mixed := api.API{
		ID:   "mixed",
		Name: "mixed",
		Services: []api.Service{{Name: "things", Operations: []api.Operation{
			{Name: "list", Method: "get", Path: "/things"},
			{Name: "broken", Method: "get", Path: "things"},
		}}},
	}
	partial, err := openapi.Render(mixed, openapi.RenderOptions{})
	require.NoError(t, err)
	assert.Contains(t, covJoin(partial.Warnings), `is omitted: path "things" must start with /`,
		"an operation that was left out of the document is reported")

	// A document read from a document declares both halves of the mapping, so it
	// derives nothing and reports nothing: the fix must not invent findings where
	// the description already said everything.
	declared := api.API{
		ID: "declared", Name: "declared",
		Services: []api.Service{{Name: "things", Operations: []api.Operation{
			{Name: "list", Method: "get", Path: "/things"},
		}}},
	}
	plain, err := openapi.Render(declared, openapi.RenderOptions{})
	require.NoError(t, err)
	assert.Empty(t, plain.Warnings, "a description that declares its mapping derives nothing")
}

// TestCovDefectAdditionalPropertiesAcceptTheBooleanForm is the second defect: the
// keyword was decoded into a schema, so the boolean spelling — valid OpenAPI, and
// the only way to say "any member" without describing one — failed to decode and
// the whole document was refused over a single keyword.
func TestCovDefectAdditionalPropertiesAcceptTheBooleanForm(t *testing.T) {
	const open = `
openapi: "3.1.0"
info: {title: Open, version: "1"}
paths:
  /bags:
    post:
      operationId: putBag
      tags: [bags]
      requestBody:
        content:
          application/json:
            schema:
              type: object
              additionalProperties: true
      responses:
        "200": {description: ok}
`
	_, _, request := covParseDefect(t, open, "defects/bags/putBag")
	require.NotNil(t, request)
	assert.True(t, request.AdditionalPropertiesAllowed, `additionalProperties: true accepts any member`)
	assert.Nil(t, request.AdditionalProperties,
		"the boolean is not a member shape: an empty shape would state a constraint the document does not")

	// The open flag is parsed, not written back: the writer states
	// additionalProperties only when it has a member shape to state, which the
	// conformance matrix records as a loss of this target. Reading it back closed is
	// that recorded loss; reading it back as an object of any shape is this defect.

	// The closed spelling is the half that a careless fix gets wrong: "the keyword
	// is present" is not "any member is accepted". A schema declared closed must
	// stay closed rather than becoming the free-form object that an empty member
	// shape reads as.
	const closed = `
openapi: "3.1.0"
info: {title: Closed, version: "1"}
paths:
  /bags:
    post:
      operationId: putBag
      tags: [bags]
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: [id]
              properties:
                id: {type: string}
              additionalProperties: false
      responses:
        "200": {description: ok}
`
	_, _, closedRequest := covParseDefect(t, closed, "defects/bags/putBag")
	require.NotNil(t, closedRequest)
	assert.False(t, closedRequest.AdditionalPropertiesAllowed, `additionalProperties: false accepts no member`)
	assert.Nil(t, closedRequest.AdditionalProperties, "a closed object has no member shape")
	// The declared members are kept: the refusal this defect caused used to replace
	// the whole schema with a free-form object, and the fix must not do the same.
	assert.Equal(t, api.TypeObject, closedRequest.Type)
	id, found := closedRequest.Property("id")
	require.True(t, found, "the members the document declares survive the keyword")
	assert.Equal(t, api.TypeString, id.Schema.Type)
	assert.Equal(t, []string{"id"}, closedRequest.Required)

	// The schema spelling still works, unchanged: this fix accepts a second form, it
	// does not replace the first.
	const shaped = `
openapi: "3.1.0"
info: {title: Shaped, version: "1"}
paths:
  /bags:
    post:
      operationId: putBag
      tags: [bags]
      requestBody:
        content:
          application/json:
            schema:
              type: object
              additionalProperties: {type: string}
      responses:
        "200": {description: ok}
`
	_, _, shapedRequest := covParseDefect(t, shaped, "defects/bags/putBag")
	require.NotNil(t, shapedRequest)
	require.NotNil(t, shapedRequest.AdditionalProperties)
	assert.Equal(t, api.TypeString, shapedRequest.AdditionalProperties.Type)
}

// TestCovDefectAllOfIsMergedRatherThanDropped is the third defect: schemaNode had
// oneOf and anyOf but no allOf, so a composition landed in the extension map and
// disappeared. What was left was a schema with no type and no members, which reads
// as "a value of any shape" — the opposite of what a composition says.
func TestCovDefectAllOfIsMergedRatherThanDropped(t *testing.T) {
	const document = `
openapi: "3.1.0"
info: {title: Composed, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      tags: [things]
      requestBody:
        content:
          application/json:
            schema:
              allOf:
                - $ref: "#/components/schemas/Identified"
                - type: object
                  properties:
                    name: {type: string}
                  required: [name]
      responses:
        "200": {description: ok}
components:
  schemas:
    Identified:
      type: object
      required: [id]
      properties:
        id: {type: string}
`
	_, warnings, request := covParseDefect(t, document, "defects/things/createThing")
	require.NotNil(t, request)

	// The members are folded in: the type of one, the members of both, the
	// requiredness of both. Dropping the composition gave none of this.
	assert.Equal(t, api.TypeObject, request.Type, "the composed type is the type the members state")
	id, found := request.Property("id")
	require.True(t, found, "the referenced member contributes its members")
	assert.Equal(t, api.TypeString, id.Schema.Type)
	name, found := request.Property("name")
	require.True(t, found, "the inline member contributes its members")
	assert.Equal(t, api.TypeString, name.Schema.Type)
	assert.Equal(t, []string{"id", "name"}, request.Required, "requiredness from every member holds")
	assert.True(t, request.IsRequired("id"))
	assert.True(t, request.IsRequired("name"))

	// A faithful composition needs no finding: every member was carried, so nothing
	// was decided on the caller's behalf.
	assert.Empty(t, warnings, "a composition that could be folded is not a finding")
}

// TestCovDefectAllOfReportsWhatItCannotCarry holds the other half of the claim: a
// composition that cannot be folded exactly is reported by name, because a member
// that disappears silently leaves the value it described undescribed.
func TestCovDefectAllOfReportsWhatItCannotCarry(t *testing.T) {
	// Two members disagree about one keyword. The later wins — a composition is
	// applied in order — and the choice is stated, with both values, so nobody has
	// to guess which member the schema followed.
	const disagreeing = `
openapi: "3.1.0"
info: {title: Disagreeing, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      tags: [things]
      requestBody:
        content:
          application/json:
            schema:
              allOf:
                - type: object
                  properties:
                    id: {type: string}
                - type: object
                  properties:
                    id: {type: integer}
      responses:
        "200": {description: ok}
`
	_, warnings, request := covParseDefect(t, disagreeing, "defects/things/createThing")
	require.NotNil(t, request)
	id, found := request.Property("id")
	require.True(t, found)
	assert.Equal(t, api.TypeInteger, id.Schema.Type, "the later member wins, as a composition applied in order does")
	assert.Contains(t, covJoin(warnings), `allOf member allOf[1] for property "id" declares type "integer", which replaces the earlier "string"`,
		"a replaced value is reported rather than chosen in silence")

	// A member that cannot be resolved at all is the case that has to be loudest:
	// its keywords are simply not there, so the only trace it can leave is the
	// finding that names it.
	const unresolvable = `
openapi: "3.1.0"
info: {title: Unresolvable, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      tags: [things]
      requestBody:
        content:
          application/json:
            schema:
              allOf:
                - $ref: "#/components/schemas/Absent"
                - type: object
                  properties:
                    name: {type: string}
      responses:
        "200": {description: ok}
`
	_, missing, request := covParseDefect(t, unresolvable, "defects/things/createThing")
	require.NotNil(t, request)
	// The member that could be folded still is: one unreadable member does not cost
	// the caller the rest of the composition.
	_, found = request.Property("name")
	assert.True(t, found)
	assert.Contains(t, covJoin(missing), `allOf member "#/components/schemas/Absent" could not be merged: it is not declared in components`,
		"a member that could not be merged is named, not dropped")
}

// TestCovDefectAllOfFollowsANamedSchemaComposition covers the shape a real document
// uses most: a component that is itself a composition, referenced from another.
func TestCovDefectAllOfFollowsANamedSchemaComposition(t *testing.T) {
	const document = `
openapi: "3.1.0"
info: {title: Named, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      tags: [things]
      requestBody:
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/Thing"
      responses:
        "200": {description: ok}
components:
  schemas:
    Base:
      type: object
      properties:
        id: {type: string}
      required: [id]
    Thing:
      allOf:
        - $ref: "#/components/schemas/Base"
        - properties:
            name: {type: string}
          required: [name]
`
	_, _, request := covParseDefect(t, document, "defects/things/createThing")
	require.NotNil(t, request)
	assert.Equal(t, api.TypeObject, request.Type)
	assert.Equal(t, []string{"id", "name"}, request.Required)
	_, hasID := request.Property("id")
	_, hasName := request.Property("name")
	assert.True(t, hasID)
	assert.True(t, hasName)
	assert.Equal(t, "#/components/schemas/Thing", request.Ref, "the document's own name for the composition is kept")
}

// TestCovDefectAllOfNamesARecursiveMember covers the member that refers back into
// the composition being merged. It has to be reported rather than followed, because
// following it is an infinite schema.
func TestCovDefectAllOfNamesARecursiveMember(t *testing.T) {
	const document = `
openapi: "3.1.0"
info: {title: Recursive, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      tags: [things]
      requestBody:
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/Loop"
      responses:
        "200": {description: ok}
components:
  schemas:
    Loop:
      allOf:
        - $ref: "#/components/schemas/Loop"
        - type: object
          properties:
            name: {type: string}
`
	_, warnings, request := covParseDefect(t, document, "defects/things/createThing")
	require.NotNil(t, request)
	assert.Contains(t, covJoin(warnings), `allOf member "#/components/schemas/Loop" could not be merged: it refers back to a schema already being merged`)
	_, found := request.Property("name")
	assert.True(t, found, "the member that could be merged still is")
}

// TestCovDefectAllOfFoldsEveryKeyword covers the merge itself rather than one
// example of it: a composition of two members that state every keyword the model
// carries. Each member states a value the other disagrees with, so the fold has to
// do two things at once — keep the later value and report that it replaced one.
//
// It is here because the alternative to folding a member is not "a smaller schema":
// it is a schema that describes nothing, and a merge that quietly picks a value is
// the same silence in a different place.
func TestCovDefectAllOfFoldsEveryKeyword(t *testing.T) {
	const document = `
openapi: "3.1.0"
info: {title: Folds, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      tags: [things]
      requestBody:
        content:
          application/json:
            schema:
              allOf:
                - type: object
                  title: Earlier
                  description: the earlier statement
                  pattern: "^a+$"
                  format: earlier-format
                  minLength: 1
                  maxLength: 9
                  minimum: 1
                  maximum: 9
                  default: first
                  deprecated: true
                  nullable: true
                  enum: [a, b]
                  required: [shared]
                  properties:
                    shared: {type: string}
                  oneOf: [{type: string}]
                  anyOf: [{type: integer}]
                  x-note: first
                - type: object
                  title: Later
                  description: the later statement
                  pattern: "^b+$"
                  format: later-format
                  minLength: 2
                  maxLength: 8
                  minimum: 2
                  maximum: 8
                  default: second
                  readOnly: true
                  enum: [c]
                  required: [extra]
                  properties:
                    shared: {type: integer}
                    extra: {type: string}
                  oneOf: [{type: boolean}]
                  anyOf: [{type: number}]
                  x-note: second
      responses:
        "200": {description: ok}
`
	_, warnings, request := covParseDefect(t, document, "defects/things/createThing")
	require.NotNil(t, request)

	// The later member's value is the one the schema carries, and the earlier one is
	// not lost: it is in the finding, which is where a reader can still see it.
	assert.Equal(t, "Later", request.Title)
	assert.Equal(t, "the later statement", request.Description)
	assert.Equal(t, "later-format", request.Format)
	assert.Equal(t, "^b+$", request.Pattern)
	assert.Equal(t, api.Float64(2), request.Minimum)
	assert.Equal(t, api.Float64(8), request.Maximum)
	assert.Equal(t, api.Int64(2), request.MinLength)
	assert.Equal(t, api.Int64(8), request.MaxLength)
	assert.EqualValues(t, "second", request.Default)
	assert.True(t, request.Deprecated, "a permission any member states holds for the composed value")
	assert.True(t, request.ReadOnly)
	assert.Equal(t, []string{"c"}, request.Enum)
	assert.Equal(t, []string{"extra", "shared"}, request.Required, "requiredness of every member holds")

	// A member two members describe is folded member by member, so a disagreement
	// about its type is reported against the property it is about.
	shared, found := request.Property("shared")
	require.True(t, found)
	assert.Equal(t, api.TypeInteger, shared.Schema.Type)
	_, found = request.Property("extra")
	assert.True(t, found)

	require.Len(t, request.OneOf, 1)
	assert.Equal(t, api.TypeBoolean, request.OneOf[0].Type)
	require.Len(t, request.AnyOf, 1)
	assert.Equal(t, api.TypeNumber, request.AnyOf[0].Type)

	joined := covJoin(warnings)
	for _, keyword := range []string{
		`declares title "Later", which replaces the earlier "Earlier"`,
		`declares description "the later statement", which replaces the earlier "the earlier statement"`,
		`declares pattern "^b+$", which replaces the earlier "^a+$"`,
		`declares format "later-format", which replaces the earlier "earlier-format"`,
		`declares default "second", which replaces the earlier "first"`,
		`declares minimum "2", which replaces the earlier "1"`,
		`declares maximum "8", which replaces the earlier "9"`,
		`declares minLength "2", which replaces the earlier "1"`,
		`declares maxLength "8", which replaces the earlier "9"`,
		`declares enum "[c]", which replaces the earlier "[a, b]"`,
		"states a oneOf of its own, so the earlier oneOf is replaced",
		"states an anyOf of its own, so the earlier anyOf is replaced",
		`for property "shared" declares type "integer", which replaces the earlier "string"`,
	} {
		assert.Contains(t, joined, keyword, "every replaced value is reported, not chosen in silence")
	}
}

// TestCovDefectAllOfFoldsTheShapesItCanAndNamesTheOnesItCannot covers what a member
// can contribute beyond its own keywords: element schemas, the shape of unlisted
// members, a composition of its own, and the two members that cannot be folded at
// all — a reference that leaves the document, and an empty entry.
func TestCovDefectAllOfFoldsTheShapesItCanAndNamesTheOnesItCannot(t *testing.T) {
	const document = `
openapi: "3.1.0"
info: {title: Shapes, version: "1"}
paths:
  /lists:
    post:
      operationId: sendList
      tags: [shapes]
      requestBody:
        content:
          application/json:
            schema:
              allOf:
                - type: array
                  items: {type: string}
                  additionalProperties: {type: string}
                - items: {type: integer}
                  additionalProperties: {type: integer}
      responses:
        "200": {description: ok}
  /chained:
    post:
      operationId: sendChained
      tags: [shapes]
      requestBody:
        content:
          application/json:
            schema:
              allOf:
                - $ref: "#/components/schemas/Composed"
                - $ref: "#/components/schemas/Alias"
                - $ref: "elsewhere.yaml#/components/schemas/Away"
                - null
                - type: object
                  properties:
                    name: {type: string}
      responses:
        "200": {description: ok}
  /switched:
    post:
      operationId: sendSwitched
      tags: [shapes]
      requestBody:
        content:
          application/json:
            schema:
              allOf:
                - type: object
                  additionalProperties: {type: string}
                - additionalProperties: true
      responses:
        "200": {description: ok}
  /sealed:
    post:
      operationId: sendSealed
      tags: [shapes]
      requestBody:
        content:
          application/json:
            schema:
              allOf:
                - type: object
                  additionalProperties: {type: string}
                - additionalProperties: false
      responses:
        "200": {description: ok}
components:
  schemas:
    Base:
      type: object
      properties:
        id: {type: string}
    Composed:
      allOf:
        - $ref: "#/components/schemas/Base"
        - properties:
            composed: {type: string}
    Alias:
      $ref: "#/components/schemas/Base"
`
	_, warnings, lists := covParseDefect(t, document, "defects/shapes/sendList")
	require.NotNil(t, lists)
	require.NotNil(t, lists.Items)
	assert.Equal(t, api.TypeInteger, lists.Items.Type, "two element schemas are folded into one")
	require.NotNil(t, lists.AdditionalProperties)
	assert.Equal(t, api.TypeInteger, lists.AdditionalProperties.Type)
	assert.Contains(t, covJoin(warnings), "for its element schema declares type \"integer\"")

	// A member that is a composition in its own right is resolved to what it states,
	// and so is a member that is nothing but a reference to another component. A
	// reference that leaves the document, and an empty entry, are named instead of
	// vanishing.
	_, chainedWarnings, chained := covParseDefect(t, document, "defects/shapes/sendChained")
	require.NotNil(t, chained)
	for _, property := range []string{"id", "composed", "name"} {
		_, found := chained.Property(property)
		assert.True(t, found, "the member %q is folded in", property)
	}
	assert.Contains(t, covJoin(chainedWarnings), `allOf member "elsewhere.yaml#/components/schemas/Away" could not be merged: it is not a local component reference`)
	assert.Contains(t, covJoin(chainedWarnings), "allOf member allOf[3] carries no schema")

	// A boolean member against a schema member is a disagreement about the same
	// keyword, so the later spelling wins and the finding names both.
	_, switchedWarnings, switched := covParseDefect(t, document, "defects/shapes/sendSwitched")
	require.NotNil(t, switched)
	assert.True(t, switched.AdditionalPropertiesAllowed)
	assert.Contains(t, covJoin(switchedWarnings),
		`declares additionalProperties "any member", which replaces the earlier "a member shape"`)

	// The closed spelling replaces the open one with the same report: a member that
	// says "no member" is not the member that came before it, and the schema that
	// results accepts fewer values than either of them alone.
	_, sealedWarnings, sealed := covParseDefect(t, document, "defects/shapes/sendSealed")
	require.NotNil(t, sealed)
	assert.False(t, sealed.AdditionalPropertiesAllowed)
	assert.Contains(t, covJoin(sealedWarnings),
		`declares additionalProperties "no member", which replaces the earlier "a member shape"`)
}

// TestCovDefectAdditionalPropertiesRefusesWhatIsNeitherForm covers the two ways a
// document can get the keyword wrong. A keyword this package cannot read has to fail
// the document with a message naming it: accepting it would describe a value the
// document did not describe, and ignoring it would silently close a shape the
// document left open.
func TestCovDefectAdditionalPropertiesRefusesWhatIsNeitherForm(t *testing.T) {
	const prefix = `
openapi: "3.1.0"
info: {title: Bad, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      tags: [things]
      requestBody:
        content:
          application/json:
            schema:
              type: object
              additionalProperties: `
	const suffix = `
      responses:
        "200": {description: ok}
`
	for name, value := range map[string]string{
		"a list":            `[1, 2]`,
		"a word":            `not-a-boolean`,
		"a malformed shape": `{type: {nested: 1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := openapi.Parse([]byte(prefix+value+suffix), openapi.ParseOptions{APIID: "bad"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "additionalProperties")
		})
	}
}

// TestCovDefectAdditionalPropertiesBooleanFormInfersAnObject covers the one shape
// where the boolean form is the whole schema: a value that accepts any member and
// states no type. Without the inference it would have no type at all, which reads
// back as a value of any shape.
func TestCovDefectAdditionalPropertiesBooleanFormInfersAnObject(t *testing.T) {
	const document = `
openapi: "3.1.0"
info: {title: Open, version: "1"}
paths:
  /bags:
    post:
      operationId: putBag
      tags: [bags]
      requestBody:
        content:
          application/json:
            schema:
              additionalProperties: true
      responses:
        "200": {description: ok}
`
	_, _, request := covParseDefect(t, document, "defects/bags/putBag")
	require.NotNil(t, request)
	assert.Equal(t, api.TypeObject, request.Type, "a value with unlisted members is an object")
	assert.True(t, request.AdditionalPropertiesAllowed)

	// A hand-written document states the keyword through a YAML anchor, which
	// arrives as an alias to the scalar. The keyword decodes the node it names, so
	// an anchor is the same statement as the literal it stands for.
	const anchored = `
openapi: "3.1.0"
info:
  title: Anchored
  version: "1"
  x-any-member: &any true
paths:
  /bags:
    post:
      operationId: putBag
      tags: [bags]
      requestBody:
        content:
          application/json:
            schema:
              type: object
              additionalProperties: *any
      responses:
        "200": {description: ok}
`
	_, _, aliased := covParseDefect(t, anchored, "defects/bags/putBag")
	require.NotNil(t, aliased)
	assert.True(t, aliased.AdditionalPropertiesAllowed, "an anchored boolean is the boolean it names")
}
