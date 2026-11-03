package providers_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/Manu343726/hammer/mcp"
	"github.com/Manu343726/hammer/openapi"
	"github.com/Manu343726/hammer/protocontract"
)

// The harness the conformance suite is built from: publishing a description in a
// target format, reading it back, and comparing the two.

// publish renders a description into a target and reads the artifact back, failing
// when either half does not work.
func publish(t *testing.T, described api.API, target string) api.API {
	t.Helper()
	back, _ := publishWithFindings(t, described, target)
	return back
}

// publishWithFindings is publish, and also the findings the provider reported.
func publishWithFindings(t *testing.T, described api.API, target string) (api.API, []string) {
	t.Helper()
	switch target {
	case "openapi":
		rendered, err := openapi.Render(described, openapi.RenderOptions{})
		if err != nil {
			t.Fatalf("publish %q as OpenAPI: %v", described.ID, err)
		}
		if len(rendered.Files) != 1 {
			t.Fatalf("a rendered OpenAPI document is one file, got %d", len(rendered.Files))
		}
		back, _, err := openapi.Parse(rendered.Files[0].Content, openapi.ParseOptions{APIID: described.ID})
		if err != nil {
			t.Fatalf("read back the OpenAPI document of %q: %v", described.ID, err)
		}
		return back, rendered.Warnings

	case "grpc", "connect":
		rendered, err := protocontract.Render(described, protocontract.RenderOptions{})
		if err != nil {
			t.Fatalf("publish %q as a protobuf contract: %v", described.ID, err)
		}
		// One contract, two protocols: gRPC and Connect are the same artifact, which
		// is why there is no second branch here. A caller that announced its contract
		// as Connect is handed what a gRPC caller is handed, and reading it back is
		// the same read.
		compiled := descriptorSetOf(t, rendered)
		back, _, err := protocontract.NewDescriptor(protocontract.Descriptor{APIID: described.ID}).FromDescriptorSet(compiled)
		if err != nil {
			t.Fatalf("read back the contract of %q: %v", described.ID, err)
		}
		return back, rendered.Warnings

	case "mcp":
		document, err := mcp.Manifest(described, mcp.RenderOptions{})
		if err != nil {
			t.Fatalf("publish %q as a tool manifest: %v", described.ID, err)
		}
		back, warnings, err := mcp.DescribeDocument(document, mcp.DescribeOptions{APIID: described.ID})
		if err != nil {
			t.Fatalf("read back the tool manifest of %q: %v", described.ID, err)
		}
		return back, warnings
	}
	t.Fatalf("the matrix names target %q, which this harness cannot render", target)
	return api.API{}, nil
}

// descriptorSetOf picks the compiled descriptor set out of a rendered contract. It
// is the artifact a reader is handed, so it is what the round trip reads.
func descriptorSetOf(t *testing.T, rendered protocontract.Rendered) []byte {
	t.Helper()
	for _, file := range rendered.Files {
		if file.MediaType == protocontract.MediaTypeDescriptorSet {
			return file.Content
		}
	}
	t.Fatalf("the rendered contract carries no descriptor set: %d files", len(rendered.Files))
	return nil
}

// readAs reads a description in one format, choosing the input that format needs.
// It is what "can this library read X?" is answered with.
func readAs(t *testing.T, format api.Format) api.API {
	t.Helper()
	switch format {
	case "openapi":
		return readOpenAPI(t, openAPIDocument)
	case "grpc", "connect":
		return readContract(t)
	case "mcp":
		return readManifest(t, manifestDocument)
	case "cli":
		root, err := cli.Generate(readOpenAPI(t, openAPIDocument), discardInvoker(), cli.Options{CommandName: "petstore"})
		if err != nil {
			t.Fatalf("build the command tree a command line is read from: %v", err)
		}
		text, err := cli.HelpText(root)
		if err != nil {
			t.Fatalf("render the help a command line is read from: %v", err)
		}
		described, err := cli.DescribeHelp(text, cli.DescribeOptions{})
		if err != nil {
			t.Fatalf("read the command line help: %v", err)
		}
		return described
	}
	t.Fatalf("the registry advertises format %q and this harness cannot feed it", format)
	return api.API{}
}

// renderTarget renders a description into a target and answers with how many
// documents it produced, failing when the target produces none.
//
// It is what "can this library write Y?" is answered with: a provider that claims a
// target has to have something to show for it.
func renderTarget(t *testing.T, described api.API, target string) int {
	t.Helper()
	switch target {
	case "openapi":
		rendered, err := openapi.Render(described, openapi.RenderOptions{})
		if err != nil {
			t.Fatalf("publish as OpenAPI: %v", err)
		}
		return len(rendered.Files)
	case "grpc", "connect":
		rendered, err := protocontract.Render(described, protocontract.RenderOptions{})
		if err != nil {
			t.Fatalf("publish as a protobuf contract: %v", err)
		}
		return len(rendered.Files)
	case "mcp":
		document, err := mcp.Manifest(described, mcp.RenderOptions{})
		if err != nil {
			t.Fatalf("publish as a tool manifest: %v", err)
		}
		if len(document) == 0 {
			t.Fatalf("publish as a tool manifest: the document is empty")
		}
		return 1
	case "cli":
		root, err := cli.Generate(described, discardInvoker(), cli.Options{CommandName: "petstore"})
		if err != nil {
			t.Fatalf("publish as a command tree: %v", err)
		}
		return len(root.Commands())
	}
	t.Fatalf("the registry advertises target %q and this harness cannot render it", target)
	return 0
}

// discardInvoker is an invoker that does nothing, for building a command tree that
// is described rather than run.
func discardInvoker() api.Invoker {
	return api.InvokerFunc(func(context.Context, api.Call) (api.Result, error) { return api.Result{}, nil })
}

// claimedFormat reports whether a provider claims to read a format.
func claimedFormat(t *testing.T, format string) bool {
	t.Helper()
	for _, provider := range providersAll() {
		if provider.Role == api.ProviderParser && provider.HandlesFormat(format) {
			return true
		}
	}
	return false
}

// claimedTarget reports whether a provider claims to render a target.
func claimedTarget(t *testing.T, target string) bool {
	t.Helper()
	for _, provider := range providersAll() {
		if provider.Role == api.ProviderAdapter && provider.HandlesTarget(target) {
			return true
		}
	}
	return false
}

// claimedTransport reports whether a provider claims a transport.
func claimedTransport(t *testing.T, transport string) bool {
	t.Helper()
	for _, provider := range providersAll() {
		if provider.Role == api.ProviderInvoker && provider.HandlesTransport(transport) {
			return true
		}
	}
	return false
}

// resolveRequiredness states each member's requiredness the one way a document
// states it.
//
// The standard model states requiredness twice — on the member and as a list on the
// value that holds it — because the formats disagree about which one to carry. A
// format that states the list is read back into the member's own flag, so a
// description written and read again can differ in the flag alone while saying the
// same thing. Resolving both sides the same way is what makes that a comparison of
// facts rather than of spellings.
func resolveRequiredness(described *api.API) {
	forEachOperation(described, func(operation *api.Operation) {
		for _, schema := range operationSchemas(operation) {
			resolveSchemaRequiredness(schema)
		}
	})
}

func resolveSchemaRequiredness(schema *api.Schema) {
	if schema == nil {
		return
	}
	switch {
	case schema.Required != nil:
		required := make(map[string]bool, len(schema.Required))
		for _, name := range schema.Required {
			required[name] = true
		}
		for index := range schema.Properties {
			schema.Properties[index].Required = required[schema.Properties[index].Name]
		}
	default:
		// A value whose members are required states so, and a format that carries
		// only the list is read back into both. Stating the list is therefore the
		// canonical form, and a value folded from parameters gets one here.
		for index := range schema.Properties {
			if schema.Properties[index].Required {
				schema.Required = append(schema.Required, schema.Properties[index].Name)
			}
		}
	}
	for index := range schema.Properties {
		resolveSchemaRequiredness(schema.Properties[index].Schema)
	}
	resolveSchemaRequiredness(schema.Items)
	resolveSchemaRequiredness(schema.AdditionalProperties)
	for _, alternative := range schema.OneOf {
		resolveSchemaRequiredness(alternative)
	}
	for _, alternative := range schema.AnyOf {
		resolveSchemaRequiredness(alternative)
	}
}

// stripReferences removes the pointers a description keeps into one document's
// component table. The value a pointer names travels with it, so dropping the
// pointer leaves every fact a consumer acts on in place.
func stripReferences(described *api.API) {
	forEachOperation(described, func(operation *api.Operation) {
		for _, schema := range operationSchemas(operation) {
			stripReference(schema)
		}
	})
}

func stripReference(schema *api.Schema) {
	if schema == nil {
		return
	}
	schema.Ref = ""
	for index := range schema.Properties {
		stripReference(schema.Properties[index].Schema)
	}
	stripReference(schema.Items)
	stripReference(schema.AdditionalProperties)
	for _, alternative := range schema.OneOf {
		stripReference(alternative)
	}
	for _, alternative := range schema.AnyOf {
		stripReference(alternative)
	}
}

// operationSchemas is every value an operation declares, so a walk over a
// description does not have to know where schemas live.
func operationSchemas(operation *api.Operation) []*api.Schema {
	result := make([]*api.Schema, 0, len(operation.Parameters)+2)
	for _, parameter := range operation.Parameters {
		result = append(result, parameter.Schema)
	}
	result = append(result, operation.Request, operation.Response)
	for _, response := range operation.Responses {
		result = append(result, response.Schema)
	}
	return result
}

// foldParameters moves an operation's parameters into its request value, which is
// what a protobuf method and an MCP tool have to do: each takes one request value
// and has nowhere to say where an argument travels.
func foldParameters(described *api.API) {
	forEachOperation(described, func(operation *api.Operation) {
		if len(operation.Parameters) == 0 {
			return
		}
		if operation.Request == nil {
			operation.Request = &api.Schema{Type: api.TypeObject}
		}
		for _, parameter := range operation.Parameters {
			operation.Request.Properties = append(operation.Request.Properties, api.Property{
				Name:     parameter.Name,
				Schema:   parameter.Schema.Clone(),
				Required: parameter.Required,
			})
		}
		operation.Parameters = nil
	})
}

// forEachOperation applies a change to every operation of an API.
func forEachOperation(described *api.API, apply func(*api.Operation)) {
	for serviceIndex := range described.Services {
		service := &described.Services[serviceIndex]
		for operationIndex := range service.Operations {
			apply(&service.Operations[operationIndex])
		}
	}
}

// sorted puts a description in a canonical order, so a comparison does not depend
// on the order a format listed members in.
func sorted(described api.API) api.API {
	clone := described.Clone()
	for serviceIndex := range clone.Services {
		service := &clone.Services[serviceIndex]
		sort.Slice(service.Operations, func(i, j int) bool {
			return service.Operations[i].Name < service.Operations[j].Name
		})
		sort.Strings(service.SideEffects)
		for operationIndex := range service.Operations {
			sortOperation(&service.Operations[operationIndex])
		}
	}
	sort.Slice(clone.Services, func(i, j int) bool { return clone.Services[i].Name < clone.Services[j].Name })
	sort.Strings(clone.Tags)
	return clone
}

func sortOperation(operation *api.Operation) {
	sort.Strings(operation.Tags)
	sort.Strings(operation.SideEffects)
	for _, schema := range operationSchemas(operation) {
		sortSchema(schema)
	}
}

func sortSchema(schema *api.Schema) {
	if schema == nil {
		return
	}
	sort.Slice(schema.Properties, func(i, j int) bool { return schema.Properties[i].Name < schema.Properties[j].Name })
	sort.Strings(schema.Required)
	for index := range schema.Properties {
		sortSchema(schema.Properties[index].Schema)
	}
	sortSchema(schema.Items)
	sortSchema(schema.AdditionalProperties)
	for _, alternative := range schema.OneOf {
		sortSchema(alternative)
	}
	for _, alternative := range schema.AnyOf {
		sortSchema(alternative)
	}
}

// describeDocument is the standard model's own JSON form, used to print a
// difference a reader can act on.
func describeDocument(described api.API) string {
	encoded, err := json.MarshalIndent(described, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(encoded)
}

// firstDifference is the first line two descriptions disagree on, so a failure
// names the fact instead of dumping a document.
func firstDifference(want, got api.API) string {
	left := strings.Split(describeDocument(want), "\n")
	right := strings.Split(describeDocument(got), "\n")
	for index := 0; index < len(left) || index < len(right); index++ {
		line, other := "", ""
		if index < len(left) {
			line = left[index]
		}
		if index < len(right) {
			other = right[index]
		}
		if line == other {
			continue
		}
		context := index - 3
		if context < 0 {
			context = 0
		}
		return "line " + strconv.Itoa(index+1) + " of the description:\n  want: " + line + "\n  got:  " + other +
			"\nnear:\n  " + strings.Join(left[context:min(index+1, len(left))], "\n  ")
	}
	return ""
}

// contains reports whether a finding states a phrase. A phrase rather than a whole
// finding, because a finding names the operations it is about.
func contains(finding, phrase string) bool {
	return strings.Contains(finding, phrase)
}

// equal reports whether two descriptions say the same thing.
func equal(want, got api.API) bool {
	return reflect.DeepEqual(want, got)
}

// firstLine is the first line of a block of prose, which is what a format that
// keeps one sentence apart from the rest states.
func firstLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return strings.TrimSpace(text[:index])
	}
	return strings.TrimSpace(text)
}

// firstNonEmpty is the first value that states something.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
