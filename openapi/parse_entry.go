package openapi

import (
	"crypto/sha256"
	"encoding/hex"

	api "github.com/Manu343726/hammer"
)

// ParseOptions configures reading a document.
type ParseOptions struct {
	// APIID overrides the identifier assigned to the parsed API. Empty derives one
	// from the document's title.
	APIID string
	// Source records where the document came from. Its digest is filled in from
	// the document's bytes, so a consumer can tell that a registered description
	// changed under it.
	Source api.Source
	// BaseURL is the endpoint the document describes. When the document declares
	// no servers, this becomes the declared one so the caller can bind the API to
	// the endpoint it actually reaches.
	BaseURL string
}

// Parse reads an OpenAPI 3.x document, in JSON or YAML, into a standard
// description.
//
// The result is a normalized description: identifiers are derived, parameters
// and bodies are resolved, and every declaration the document carries is carried
// through. What the document does not declare stays empty — the consequences of
// invoking an operation above all — so a policy can refuse an operation until
// the caller says what it authorizes, rather than inferring safety from the
// presence of a function.
func Parse(document []byte, options ParseOptions) (api.API, []string, error) {
	digest := sha256.Sum256(document)
	source := options.Source
	if source.Digest == "" {
		source.Digest = hex.EncodeToString(digest[:])
	}
	described, warnings, err := parseDocument(document, parseRequest{
		APID:   options.APIID,
		Source: source,
	})
	if err != nil {
		return api.API{}, nil, api.WrapError(api.KindInvalid, err, "read the OpenAPI document")
	}
	// A document may declare its own servers; when the caller supplied a base URL
	// and the document declared none, the caller's location becomes the declared
	// one so the API can be bound to the endpoint that was actually read.
	if len(described.DeclaredServers) == 0 && options.BaseURL != "" {
		described.DeclaredServers = []api.DeclaredServer{{URL: options.BaseURL}}
	}
	return described, warnings, nil
}
