package mcp

// This file exposes the one reader the external tests cannot reach through the
// package's own surface.
//
// The protocol leaves a tool's schema as "any value that marshals to valid JSON
// Schema", so schemaDocument reads four shapes. A schema read from a live server,
// however, always arrives decoded into a map: the SDK's client decodes the tool
// list from JSON, which is why the raw-message, byte-slice, and struct arms can
// only be exercised from inside the package. Exposing the function keeps the new
// tests external, which is what a caller of this package is.
//
// It is compiled only for tests and changes no behaviour.

// CovSchemaDocument reads a tool's schema the way describeTool does.
var CovSchemaDocument = schemaDocument
