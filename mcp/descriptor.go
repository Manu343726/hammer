// Package mcp reads a Model Context Protocol server, or a published tool
// manifest, into the standard description, renders a description into MCP tool
// definitions, and calls a tool.
//
// It is a plain Go package: it depends on the standard model and on the
// protocol's own SDK, and on nothing else. A caller uses it directly — to describe
// a third-party tool surface as an API, to publish an API in MCP whatever language
// that API was described from, or to call one of its tools. A program that has to
// offer the same three jobs over a protocol of its own wraps it; this package
// never knows.
//
// The four capabilities are separate on purpose. Reading a live server and
// reading a manifest are two readers for the one format; rendering and calling
// are the two things a caller does with the result.
//
// Every rendering of a tool comes from Render, because a tool name is a promise
// to whatever already learned it. Two translations of one description that
// agreed by accident would stop agreeing the first time the naming rule or the
// argument schema changed, and the client that learned the old name would reach
// nothing.
package mcp

import (
	"encoding/json"

	api "github.com/Manu343726/hammer"
)

const (
	// Version is the reference implementation version.
	Version = "0.1.0"

	// Format is the description format an MCP server is read as. It is an
	// identifier an index records; nothing in this package branches on it.
	Format api.Format = "mcp"

	// Transport is the transport an MCP server is reached over. It belongs to the
	// description because the format knows it: a protocol that names its own
	// transport states how its operations are called, and a caller left to guess
	// between HTTP and MCP would make the wrong call at the worst moment.
	Transport api.Transport = "mcp"

	// ToolCallMethod is the protocol method a tool's operation is called by. An
	// operation names the method it is invoked with, and for this format the
	// answer is always the protocol's own.
	ToolCallMethod = "tools/call"

	// SourceKind records that a description came from a live MCP server rather
	// than from a document, which is how a caller tells a description that can be
	// refreshed from one that cannot.
	SourceKind api.SourceKind = "mcp"

	// ServerName is the service name a described server's tools are grouped under
	// when the caller does not supply one.
	ServerName = "mcp"

	// ClientName is the identity this package gives itself when it speaks the
	// protocol. It is not a server's name and never appears in a description.
	ClientName = "hammer"

	// ToolManifestFile is the file name a rendered tool manifest is published
	// under, so a caller that writes the document somewhere predictable does not
	// have to invent a name of its own.
	ToolManifestFile = "mcp-tools.json"
)

// Formats returns the format identifiers this package claims.
func Formats() []api.Format { return []api.Format{Format} }

// Targets returns the representations this package renders into.
func Targets() []string { return []string{string(Format)} }

// HandlesFormat reports whether this package reads a format.
func HandlesFormat(format api.Format) bool {
	return api.NormalizeIdentifier(string(format)) == string(Format)
}

// HandlesTarget reports whether this package renders into a target.
func HandlesTarget(target string) bool {
	return api.NormalizeIdentifier(target) == string(Format)
}

// FormatDescriptor describes the format for an index, so the index can answer
// "can this library read an MCP server?" without importing this package to find
// out.
func FormatDescriptor() api.FormatDescriptor {
	return api.FormatDescriptor{
		ID:          string(Format),
		Name:        "Model Context Protocol",
		Version:     Version,
		Description: "A Model Context Protocol server's tool list, read from a live endpoint's own tools/list response.",
		MediaTypes:  []string{"application/json"},
	}
}

// TargetDescriptor describes the representation this package renders, so an
// index can say what this library is able to publish and not only what it can
// read.
func TargetDescriptor() api.FormatDescriptor {
	return api.FormatDescriptor{
		ID:                   string(Format),
		Name:                 "Model Context Protocol tool manifest",
		Version:              Version,
		SpecificationVersion: "2025-06-18",
		Description:          "A Model Context Protocol tool manifest rendered from a standard API description, whatever format that description was parsed from.",
		MediaTypes:           []string{"application/json"},
		FileExtensions:       []string{"json"},
	}
}

// TransportDescriptors describes the transports this package reaches.
func TransportDescriptors() []api.TransportDescriptor {
	return []api.TransportDescriptor{{
		ID:          string(Transport),
		Name:        "Model Context Protocol",
		Version:     Version,
		Description: "Tool calls to a Model Context Protocol server over Streamable HTTP.",
		Schemes:     []string{"http", "https"},
	}}
}

// Manifest renders a description as the tool manifest document a client is
// offered, which is the file a publisher writes.
//
// It is Render written out, not a second translation: a server serving these
// tools and a manifest describing them have to agree, because a client that
// learned a name from one of them must find the same tool on the other.
func Manifest(described api.API, options RenderOptions) ([]byte, error) {
	rendered, err := Render(described, options)
	if err != nil {
		return nil, err
	}
	tools := make([]map[string]any, 0, len(rendered.Tools))
	for _, tool := range rendered.Tools {
		entry := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
		}
		if tool.OutputSchema != nil {
			entry["outputSchema"] = tool.OutputSchema
		}
		entry["x-toolbox-operation"] = tool.OperationID
		// A description's declared consequences are written as the protocol's own
		// annotations, so a manifest read back says the same thing the description
		// did. Dropping them here would lose exactly what a policy filters on, and
		// the tool would come back unclassified — which reads as "unknown", not as
		// "safe", but is still less than what the description knew.
		if annotations := toolAnnotations(tool); len(annotations) > 0 {
			entry["annotations"] = annotations
		}
		tools = append(tools, entry)
	}
	document := map[string]any{
		"protocolVersion": "2025-06-18",
		"server": map[string]any{
			"name":        firstNonEmpty(described.Name, described.ID),
			"description": rendered.Instructions,
		},
		"tools": tools,
	}
	if len(rendered.Warnings) > 0 {
		// Findings travel with the document, so a reader sees what was left out
		// instead of inferring it from what is missing.
		document["x-toolbox-warnings"] = rendered.Warnings
	}
	return indentJSON(document)
}

// toolAnnotations renders a tool's declared consequences as the protocol's
// annotations.
func toolAnnotations(tool ToolDefinition) map[string]any {
	annotations := map[string]any{}
	if tool.ReadOnly {
		annotations["readOnlyHint"] = true
	}
	if tool.Destructive {
		annotations["destructiveHint"] = true
	}
	if tool.Idempotent {
		annotations["idempotentHint"] = true
	}
	if len(annotations) == 0 {
		return nil
	}
	return annotations
}

func indentJSON(document map[string]any) ([]byte, error) {
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, api.WrapError(api.KindInternal, err, "encode the tool manifest")
	}
	return encoded, nil
}
