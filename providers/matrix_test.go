package providers_test

import (
	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/providers"
)

// cell is one square of the matrix: a description read from one format and
// published in another.
type cell struct {
	// source and target are the format identifiers of the two ends.
	source string
	target string
	// reports are the findings the provider publishing into target has to state. A
	// loss the provider announces is the contract; a loss kept to itself is the
	// defect, and a cell that has one says so in its losses instead.
	reports []string
	// losses are the facts this target cannot carry, each with the reason.
	losses []loss
}

// loss is one fact a description states that a target cannot restate.
type loss struct {
	// name is the fact, in the words a failure and the report use.
	name string
	// because is why the target cannot carry it: prose for whoever reads a failure,
	// and the answer to "which cells are lossy, and why".
	because string
	// apply removes the fact from a description, so the two sides can be compared
	// on the facts the target does carry.
	apply func(*api.API)
}

// matrix is every square a caller can ask this library for: each format it reads,
// published as each representation it writes.
//
// The losses are the measurement this suite exists to publish. They are the answer
// to "what does publishing in this format cost?", and each of them is checked
// against the code, so the table cannot outlive the behaviour it describes.
func matrix() []cell {
	return []cell{
		// From an OpenAPI document.
		{
			source: "openapi",
			target: "openapi",
			losses: []loss{lossTagProse, lossResponses},
		},
		{
			source: "openapi",
			target: "grpc",
			reports: []string{
				"a protobuf method is named rather than addressed",
				"the transport method declared by 2 operations was dropped",
				"the parameters of 1 operation were folded into its request message",
				"operations documented responses by status",
				"declares a version or servers for the API",
				"declares tags for itself or its operations",
				"protobuf gives every message field presence",
			},
			losses: []loss{lossAPIName, lossAPIDeclaration, lossTags, lossWire, lossResponses, lossRequiredness, lossServiceTitle},
		},
		{
			source: "openapi",
			target: "connect",
			// The same artifact as the gRPC cell, so the same findings: a caller that
			// announced its contract as Connect is handed the contract a gRPC caller is
			// handed, and hears what a gRPC caller hears.
			reports: []string{
				"a protobuf method is named rather than addressed",
				"the transport method declared by 2 operations was dropped",
				"the parameters of 1 operation were folded into its request message",
				"operations documented responses by status",
				"declares a version or servers for the API",
				"declares tags for itself or its operations",
				"protobuf gives every message field presence",
			},
			losses: []loss{lossAPIName, lossAPIDeclaration, lossTags, lossWire, lossResponses, lossRequiredness, lossServiceTitle},
		},
		{
			source: "openapi",
			target: "mcp",
			losses: []loss{lossTitleAsDescription, lossManifestVersion, lossTags, lossServiceIdentity, lossOperationProse, lossWire, lossResponses, lossCreate},
		},

		// From a protobuf contract.
		{
			source: "grpc",
			target: "openapi",
			losses: []loss{lossWireDerived, lossVersionRequired, lossTags, lossServiceProse, lossResponses, lossCreate},
		},
		{
			source: "grpc",
			target: "grpc",
		},
		{
			source: "grpc",
			target: "connect",
		},
		{
			source: "grpc",
			target: "mcp",
			losses: []loss{lossTitleAsDescription, lossServiceIdentity, lossOperationProse, lossWire},
		},

		// From a tool manifest.
		{
			source: "mcp",
			target: "openapi",
			losses: []loss{lossWireDerived, lossVersionRequired, lossResponses, lossCreate, lossTags, lossOpenShape},
		},
		{
			source: "mcp",
			target: "grpc",
			reports: []string{
				"a protobuf method's name is the whole of what its transport needs",
				"protobuf gives every message field presence",
				"a repeated or map field has no presence",
			},
			losses: []loss{lossAPIName, lossServiceTitle, lossOperationDescription, lossMemberProse, lossSchemaTitles, lossOpenShape, lossWire, lossRequiredness},
		},
		{
			source: "mcp",
			target: "connect",
			reports: []string{
				"a protobuf method's name is the whole of what its transport needs",
				"protobuf gives every message field presence",
				"a repeated or map field has no presence",
			},
			losses: []loss{lossAPIName, lossServiceTitle, lossOperationDescription, lossMemberProse, lossSchemaTitles, lossOpenShape, lossWire, lossRequiredness},
		},
		{
			source: "mcp",
			target: "mcp",
		},
	}
}

// The losses, in the words the report and a failure use.
//
// Each one names a fact and why it does not survive. The reason is the part that
// matters: a format with no spelling for a fact is a limit of the format, and a
// writer that states something else in its place is a finding. Two entries here are
// the second kind and say so — the OpenAPI writer's one success response, and the
// create consequence an MCP tool has no annotation for. They are recorded rather
// than asserted silent, because a loss a coder can read about is worth more than
// one they have to discover.
var (
	lossTagProse = loss{
		name:    "a tag's prose",
		because: "an OpenAPI document groups operations by tag, and this writer states no tag block, so a document's own description of a tag is not restated",
		apply:   dropServiceProse,
	}
	lossResponses = loss{
		name:    "every documented response but the success",
		because: "a protobuf method has one response value and no status, an MCP tool states the shape of its result and no status, and this OpenAPI writer publishes one success response described by the operation's summary — so the other statuses and their prose are not carried",
		apply:   dropResponses,
	}
	lossAPIName = loss{
		name:    "the API's title and description",
		because: "a protobuf contract has no field for either: the writer puts them in a file comment its own reader does not read back, and the reader derives a title from the service name instead",
		apply:   func(described *api.API) { described.Title = ""; described.Description = "" },
	}
	lossTitleAsDescription = loss{
		name:    "the API's title",
		because: "a tool manifest states a server name and one block of prose: when the API has no description of its own, its title is written as that prose and comes back as the description",
		apply: func(described *api.API) {
			if described.Description == "" {
				described.Description = described.Title
			}
			described.Title = ""
		},
	}
	lossManifestVersion = loss{
		name:    "the API's version",
		because: "a tool manifest this library writes states no server version, so a version read from somewhere else has no field to come back in",
		apply:   dropVersion,
	}
	lossAPIDeclaration = loss{
		name:    "the API's version and declared servers",
		because: "a protobuf contract has no field for either: the version and the locations go into the file comment, and the reader does not read comments back",
		apply:   func(described *api.API) { described.Version = ""; described.DeclaredServers = nil },
	}
	lossVersionRequired = loss{
		name:    "the API's version",
		because: "OpenAPI requires a version and a protobuf contract has none, so a description that states no version comes back as the one this writer chose for it",
		apply:   dropVersion,
	}
	lossTags = loss{
		name:    "the API's tags and each operation's tags, and the servers it declares",
		because: "a protobuf contract groups operations by service and a tool manifest has a name and a description, so neither has a field for a tag or for a declared location",
		apply: func(described *api.API) {
			described.Tags = nil
			described.DeclaredServers = nil
			forEachOperation(described, func(operation *api.Operation) { operation.Tags = nil })
		},
	}
	lossOperationProse = loss{
		name:    "the difference between an operation's summary and its description",
		because: "a tool states one block of prose, which the writer takes from the operation's summary, so a description the operation keeps apart from that summary is not stated",
		apply: func(described *api.API) {
			forEachOperation(described, func(operation *api.Operation) {
				text := firstLine(firstNonEmpty(operation.Summary, operation.Description))
				operation.Summary = text
				operation.Description = text
			})
		},
	}
	lossOperationDescription = loss{
		name:    "an operation's own description",
		because: "a protobuf method has one comment, which this reader states as the operation's summary, so a description the operation keeps apart from its summary is not restated",
		apply: func(described *api.API) {
			forEachOperation(described, func(operation *api.Operation) { operation.Description = "" })
		},
	}
	lossOpenShape = loss{
		name:    "a value's openness to members it does not name",
		because: "an OpenAPI schema states additional properties only when it has their shape, and a protobuf message has no open form at all, so a value that accepts any member comes back closed",
		apply: func(described *api.API) {
			forEachOperation(described, func(operation *api.Operation) {
				for _, schema := range operationSchemas(operation) {
					clearAdditionalPropertiesAllowed(schema)
				}
			})
		},
	}
	lossServiceTitle = loss{
		name:    "a service's own title",
		because: "the protobuf reader derives a service title from the service name",
		apply:   dropServiceTitle,
	}
	lossSchemaTitles = loss{
		name:    "a value's title",
		because: "a protobuf message has no title of its own, and this writer has no spelling for one",
		apply: func(described *api.API) {
			forEachOperation(described, func(operation *api.Operation) {
				for _, schema := range operationSchemas(operation) {
					clearSchemaTitles(schema)
				}
			})
		},
	}
	lossMemberProse = loss{
		name:    "a member's own prose",
		because: "a protobuf field's comment is not part of a descriptor set, so what a member documents about itself is not read back",
		apply: func(described *api.API) {
			forEachOperation(described, func(operation *api.Operation) {
				for _, schema := range operationSchemas(operation) {
					clearSchemaDescriptions(schema)
				}
			})
		},
	}
	lossServiceProse = loss{
		name:    "a service's own title and description",
		because: "an OpenAPI document groups operations by tag and this writer states no tag block, while a protobuf method's service is named and not described",
		apply:   dropServiceProse,
	}
	lossServiceIdentity = loss{
		name:    "the service grouping",
		because: "a tool has no group: an MCP description puts every tool under the server's own name",
		apply:   dropServiceIdentity,
	}
	lossWire = loss{
		name:    "each operation's method and path",
		because: "a protobuf method is named rather than addressed, and a tool is called by the protocol's one method and has no address",
		apply:   dropWire,
	}
	lossWireDerived = loss{
		name:    "each operation's method and path",
		because: "a protobuf method is named rather than addressed, so the writer derives the conventional mapping of a procedure onto HTTP — one POST under a path built from the service and method names",
		apply:   dropWire,
	}
	lossRequiredness = loss{
		name:    "a member's requiredness",
		because: "protobuf gives an object-valued field presence and takes it from a repeated or map field, so requiredness changes in both directions",
		apply:   dropRequiredness,
	}
	lossCreate = loss{
		name:    "a create consequence",
		because: "the MCP protocol's annotations state read-only and destructive and this writer states nothing for a create, while the OpenAPI writer derives create from a POST — so the consequence is dropped on one side and invented on the other",
		apply:   dropEffect(api.SideEffectCreate),
	}
)

// The changes a loss applies. They live here rather than beside the table because
// they are the mechanics of comparing, not the claims being compared.

func dropTitle(described *api.API)   { described.Title = "" }
func dropVersion(described *api.API) { described.Version = "" }

func dropWire(described *api.API) {
	forEachOperation(described, func(operation *api.Operation) {
		operation.Method = ""
		operation.Path = ""
	})
}

func dropResponses(described *api.API) {
	forEachOperation(described, func(operation *api.Operation) { operation.Responses = nil })
}

func dropServiceTitle(described *api.API) {
	for index := range described.Services {
		described.Services[index].Title = ""
	}
}

func dropServiceProse(described *api.API) {
	for index := range described.Services {
		described.Services[index].Title = ""
		described.Services[index].Description = ""
	}
}

// dropServiceIdentity removes what names a service, leaving the operations it
// groups: a format that has no groups can still carry every operation.
func dropServiceIdentity(described *api.API) {
	forEachOperation(described, func(operation *api.Operation) {
		operation.ID = ""
		operation.Service = ""
	})
	if len(described.Services) == 0 {
		return
	}
	// A format with no groups puts every operation in one place, so the operations
	// of several services are gathered into the first.
	merged := api.Service{Operations: make([]api.Operation, 0, len(described.Operations()))}
	for _, service := range described.Services {
		merged.Operations = append(merged.Operations, service.Operations...)
	}
	described.Services = []api.Service{merged}
}

func dropRequiredness(described *api.API) {
	forEachOperation(described, func(operation *api.Operation) {
		clearRequiredness(operation.Request)
		clearRequiredness(operation.Response)
		for _, response := range operation.Responses {
			clearRequiredness(response.Schema)
		}
	})
}

func clearSchemaTitles(schema *api.Schema) {
	if schema == nil {
		return
	}
	schema.Title = ""
	for index := range schema.Properties {
		clearSchemaTitles(schema.Properties[index].Schema)
	}
	clearSchemaTitles(schema.Items)
	clearSchemaTitles(schema.AdditionalProperties)
	for _, alternative := range schema.OneOf {
		clearSchemaTitles(alternative)
	}
	for _, alternative := range schema.AnyOf {
		clearSchemaTitles(alternative)
	}
}

func clearSchemaDescriptions(schema *api.Schema) {
	if schema == nil {
		return
	}
	schema.Description = ""
	for index := range schema.Properties {
		clearSchemaDescriptions(schema.Properties[index].Schema)
	}
	clearSchemaDescriptions(schema.Items)
	clearSchemaDescriptions(schema.AdditionalProperties)
	for _, alternative := range schema.OneOf {
		clearSchemaDescriptions(alternative)
	}
	for _, alternative := range schema.AnyOf {
		clearSchemaDescriptions(alternative)
	}
}

func clearAdditionalPropertiesAllowed(schema *api.Schema) {
	if schema == nil {
		return
	}
	schema.AdditionalPropertiesAllowed = false
	for index := range schema.Properties {
		clearAdditionalPropertiesAllowed(schema.Properties[index].Schema)
	}
	clearAdditionalPropertiesAllowed(schema.Items)
	clearAdditionalPropertiesAllowed(schema.AdditionalProperties)
	for _, alternative := range schema.OneOf {
		clearAdditionalPropertiesAllowed(alternative)
	}
	for _, alternative := range schema.AnyOf {
		clearAdditionalPropertiesAllowed(alternative)
	}
}

func clearRequiredness(schema *api.Schema) {
	if schema == nil {
		return
	}
	schema.Required = nil
	for index := range schema.Properties {
		schema.Properties[index].Required = false
		clearRequiredness(schema.Properties[index].Schema)
	}
	clearRequiredness(schema.Items)
	clearRequiredness(schema.AdditionalProperties)
	for _, alternative := range schema.OneOf {
		clearRequiredness(alternative)
	}
	for _, alternative := range schema.AnyOf {
		clearRequiredness(alternative)
	}
}

// dropEffect removes one consequence from every operation that declares it, for a
// target whose vocabulary has no word for that consequence.
func dropEffect(effect api.SideEffect) func(*api.API) {
	return func(described *api.API) {
		forEachOperation(described, func(operation *api.Operation) {
			kept := make([]api.SideEffect, 0, len(operation.SideEffects))
			for _, declared := range operation.SideEffects {
				if declared != effect {
					kept = append(kept, declared)
				}
			}
			operation.SideEffects = kept
		})
	}
}

// providersAll is the registry's answer, asked once per helper so a test cannot
// pass by asking a different question than the one the suite is about.
func providersAll() []api.Provider { return providers.Providers() }
