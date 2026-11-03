package openapi

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// These tests are in the package rather than beside it because each one holds a
// guard the public surface cannot reach: the HTTP mux redirects a request to the
// surface root before the handler sees it, the encoders are only ever handed
// documents this package built, and the scalar helpers are only ever handed nodes
// a decoder produced. They exist so the guards that make those paths safe are
// proven rather than assumed, and each says which guard it holds.

func TestCovPickMediaHasNoCandidateForAnEmptyContentMap(t *testing.T) {
	// pickMedia reports that it found nothing, which is what lets a caller tell an
	// absent media type from a chosen one. Reached here because the only caller
	// returns early on an empty map, so the guard is otherwise unproven.
	builder := &builder{warnings: make([]string, 0)}
	_, ok := builder.pickMedia(map[string]mediaNode{})
	assert.False(t, ok)
}

func TestCovAdditionalPropertiesFlagIsReadFromTheExtension(t *testing.T) {
	// The schema shape records whether unlisted members are accepted, which is the
	// difference between a closed object and a map. The flag arrives as a document
	// extension rather than a typed field, so it is read as text; a value that is
	// not a boolean accepts nothing rather than everything.
	builder := &builder{warnings: make([]string, 0)}
	allowed := schemaNode{Extensions: map[string]yaml.Node{
		"additionalProperties": {Value: "true"},
	}}
	assert.True(t, allowed.AdditionalPropertiesAllowed())
	converted, err := builder.convertComponentSchema(&allowed, make(map[string]bool))
	require.NoError(t, err)
	assert.True(t, converted.AdditionalPropertiesAllowed)

	denied := schemaNode{Extensions: map[string]yaml.Node{
		"additionalProperties": {Value: "sometimes"},
	}}
	assert.False(t, denied.AdditionalPropertiesAllowed(), "an unreadable value is not a yes")
}

func TestCovScalarHelpersSurviveANodeTheyCannotDecode(t *testing.T) {
	// A node whose tag its text cannot satisfy is what a decoder hands back for a
	// malformed literal. Neither helper may panic: one falls back to the node's own
	// text, the other declares no value rather than an invented one.
	undecodable := yaml.Node{Kind: yaml.ScalarNode, Tag: "!!timestamp", Value: "not-a-time"}
	assert.Equal(t, "not-a-time", scalarString(undecodable))
	assert.Nil(t, scalarValue(undecodable))
}

func TestCovRenderSchemaWithNothingToDescribe(t *testing.T) {
	// A caller may hand the renderer no schema at all, and the document then carries
	// an empty schema object rather than a null, which is what the document
	// language requires and what a reader can parse.
	warnings := make([]string, 0)
	target := map[string]any{"schemas": map[string]any{}}
	assert.Equal(t, map[string]any{}, renderSchema(nil, map[string]string{}, target, "body", "owner", &warnings))
	assert.Empty(t, warnings)
}

// covUnencodableYAML is a value the YAML encoder refuses, which is what a
// document carrying something this package cannot write looks like to the
// encoder.
type covUnencodableYAML struct{}

func (covUnencodableYAML) MarshalYAML() (any, error) {
	return nil, fmt.Errorf("this value cannot be written")
}

func TestCovMarshalDocumentReportsAValueItCannotEncode(t *testing.T) {
	// A document this package builds always encodes, so the encoder's error path is
	// proven here. A failure must be reported as a failed render, never returned as
	// a document: half a document is not a document.
	_, err := marshalDocument(map[string]any{"value": math.NaN()}, renderOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encode JSON document")

	_, err = marshalDocument(map[string]any{"value": covUnencodableYAML{}}, renderOptions{mediaType: "application/yaml"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encode YAML document")
}

func TestCovPrimaryDocumentWithoutAPrimaryFile(t *testing.T) {
	// The primary file is what a one-document client starts from. When no file is
	// marked, the first one is still an answer rather than an error, because a
	// renderer that produced a single file has already said which one it means.
	content, err := primaryDocument([]File{{Name: "openapi.json", Content: []byte(`{}`)}})
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(content))

	// A renderer that produced nothing at all is an internal failure: the surface
	// cannot serve a schema it does not have.
	_, err = primaryDocument(nil)
	require.Error(t, err)
	assert.Equal(t, api.KindInternal, api.KindOf(err))
}

func TestCovSchemaRenderFailureIsReported(t *testing.T) {
	// A description with no operation cannot become a document. The surface renders
	// its schema before it listens, so the failure is reported as a failed mount
	// rather than as a surface that answers nothing.
	surfaces := NewSurfaces(SurfaceOptions{})
	_, err := surfaces.schema(api.API{ID: "empty", Name: "empty"}, "application/json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no operation that OpenAPI can represent")
}

func TestCovDocumentationPageSurvivesADocumentItCannotRead(t *testing.T) {
	// The page is generated from the schema, and the schema is this package's own
	// output. If it were ever not, the page must still render with the generic title
	// rather than fail, because the endpoint is the only documentation a deployment
	// without a supplied UI has.
	surfaces := NewSurfaces(SurfaceOptions{})
	page := string(surfaces.documentationPage("instance", []byte("{not json"), "/api"))
	assert.Contains(t, page, "Adapted API")
	assert.Contains(t, page, "instance")
}

func TestCovItoaRendersZero(t *testing.T) {
	// The path count on the documentation page may be zero for a schema that
	// declares none, and the page must say "0" rather than nothing.
	assert.Equal(t, "0", itoa(0))
	assert.Equal(t, "42", itoa(42))
}

func TestCovTunnelRejectsARequestItCannotBuild(t *testing.T) {
	// The tunnelled request is built from the matched route. A route whose method is
	// not an HTTP token cannot become a request, and the caller is told the gateway
	// failed rather than being handed an empty answer.
	surfaces := NewSurfaces(SurfaceOptions{})
	original, err := url.Parse("http://original.test")
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/things", nil)

	surfaces.tunnel(recorder, request, original, route{method: "ge t", path: "/things"}, nil, nil)

	assert.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "cannot build the tunnelled request")
}

func TestCovMatchSegmentsRejectsAnEmptyParameter(t *testing.T) {
	// A parameter matches a whole path segment, and an empty segment is not a value.
	// Treating it as one would forward a request for a resource with no identifier.
	_, matched := matchSegments([]patternSegment{{parameter: "thingId"}}, "/")
	assert.False(t, matched)
}

func TestCovParameterTextWithoutAValue(t *testing.T) {
	// A parameter that was supplied with no value at all contributes no text, which
	// is what makes an optional parameter absent rather than empty.
	text, err := parameterText(api.Parameter{Name: "filter"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "", text)
}

func TestCovFieldsArgumentReportsAFieldItCannotEncode(t *testing.T) {
	// The body built from the caller's fields is only sent when it is JSON. A field
	// that cannot be encoded means no body, so the call is not sent with a partial
	// one.
	body, ok := fieldsArgument(
		map[string]json.RawMessage{"name": json.RawMessage("{not json")},
		nil,
		map[string]bool{"name": true},
	)
	assert.False(t, ok)
	assert.Nil(t, body)
}
