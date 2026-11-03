package cli_test

import (
	"sort"
	"strings"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A description and the command line built from it are one contract seen twice,
// so the round trip is the property these tests hold the reader to: describing a
// generated tree, and describing the help that tree prints, has to give back the
// operations and the inputs the description declared.
//
// Two comparisons, because they fail for different reasons:
//
//   - a tree read and a text read of the same tree must agree exactly, which is
//     what proves the text grammar is not losing something the tree states;
//   - a described tree must agree with the description it was generated from on
//     everything a command line can carry, which is what proves the mapping is
//     the inverse of the generator's rather than merely self-consistent.

// describeTree reads a tree both ways and fails when either reading does.
func describeTree(t *testing.T, root *cobra.Command) (api.API, api.API) {
	t.Helper()
	fromTree, err := cli.Describe(root, cli.DescribeOptions{})
	require.NoError(t, err)
	text, err := cli.HelpText(root)
	require.NoError(t, err)
	fromText, err := cli.DescribeHelp(text, cli.DescribeOptions{})
	require.NoError(t, err)
	return fromTree, fromText
}

// sameReading holds the two readings of one tree to each other. The source is the
// only thing that differs by construction: a tree is read where it is, and a text
// is read from nowhere in particular.
func sameReading(t *testing.T, fromTree, fromText api.API) {
	t.Helper()
	fromText.Source = fromTree.Source
	assert.Equal(t, fromTree, fromText,
		"the help a tree prints describes the same surface as the tree it printed from")
}

// The property the whole direction exists for: every operation of the description
// is an operation of the description read back, with the same wire line, the same
// declared facts, and the same inputs.
func TestTheOperationsSurviveTheRoundTrip(t *testing.T) {
	original := petstore()
	require.NoError(t, original.AnnotateOperationIDs(), "the fixture declares no identifiers; a description has them")
	root := generate(t, original, &recorder{}, cli.Options{CommandName: "petstore"})
	described, _ := describeTree(t, root)

	want := operationsByID(t, original)
	got := operationsByID(t, described)
	require.Equal(t, sortedIDs(want), sortedIDs(got))

	for id, operation := range want {
		read, ok := got[id]
		require.True(t, ok, "%s is missing from the description read back", id)
		assert.Equal(t, operation.Name, read.Name, id)
		assert.Equal(t, operation.Method, read.Method, id)
		assert.Equal(t, operation.Path, read.Path, id)
		assertSameProse(t, id, operation, read)
		assert.Equal(t, operation.SideEffects, read.SideEffects, id)
		assert.Equal(t, operation.Deprecated, read.Deprecated, id)
		assert.Equal(t, operation.Streaming, read.Streaming, id)
		require.Len(t, read.Parameters, len(operation.Parameters), id)
		for index, parameter := range operation.Parameters {
			read := read.Parameters[index]
			path := id + " parameter " + parameter.Name
			assert.Equal(t, parameter.Name, read.Name, path)
			assert.Equal(t, parameter.In, read.In, path)
			assert.Equal(t, parameter.Description, read.Description, path)
			assert.Equal(t, parameter.Required, read.Required, path)
			assert.Equal(t, parameter.Deprecated, read.Deprecated, path)
			assertSameSchema(t, path, parameter.Schema, read.Schema)
		}
		assertSameSchema(t, id+" request", operation.Request, read.Request)
	}
}

// The same, for the shape where the generator drops a level: a service named after
// the command has no command of its own, so its operations sit on the root and the
// only statement of which service they belong to is the identifier their help
// prints.
func TestAFlattenedServiceComesBackAsItsService(t *testing.T) {
	root := generate(t, petstore(), &recorder{}, cli.Options{CommandName: "pets"})
	fromTree, fromText := describeTree(t, root)
	sameReading(t, fromTree, fromText)

	assert.Nil(t, findChild(root, "pets"), "the fixture is the flattened shape")
	assert.Equal(t, []string{"pets", "users"}, serviceNames(fromTree))
	for _, operation := range fromTree.Operations() {
		assert.Equal(t, "petstore", operation.APIID, "the identifier states the API the command name cannot")
		if strings.HasPrefix(operation.Name, "getUser") {
			assert.Equal(t, "users", operation.Service)
		} else {
			assert.Equal(t, "pets", operation.Service,
				"the operations the generator put on the root belong to the service it flattened")
		}
	}
}

// A tool that is a single command is one operation: there is no subcommand to
// group, and dropping it would describe nothing at all.
func TestAToolOfOneCommandIsOneOperation(t *testing.T) {
	described_theAPI := api.API{
		ID:          "single",
		Name:        "single",
		Format:      "test",
		Description: "Does one thing.",
		Services: []api.Service{{
			Name: "single",
			Operations: []api.Operation{{
				Name:       "single",
				Summary:    "Does one thing.",
				Parameters: []api.Parameter{{Name: "file", In: api.ParameterInPath, Required: true, Schema: api.StringSchema()}},
			}},
		}},
	}
	root := generate(t, described_theAPI, &recorder{}, cli.Options{})
	fromTree, fromText := describeTree(t, root)
	sameReading(t, fromTree, fromText)

	require.Len(t, fromTree.Services, 1)
	require.Len(t, fromTree.Services[0].Operations, 1)
	assert.Equal(t, "single", fromTree.Services[0].Operations[0].Name)
	require.Len(t, fromTree.Operations()[0].Parameters, 1)
	assert.Equal(t, api.ParameterInPath, fromTree.Operations()[0].Parameters[0].In)
}

// A request value carried whole is the operation's request, and a flag whose name
// is a path is a member of it: the two shapes a request has are both read back.
func TestTheRequestValueSurvivesTheRoundTrip(t *testing.T) {
	described_theAPI := things(
		api.Operation{
			Name:   "putDocument",
			Method: "PUT",
			Path:   "/documents",
			Request: api.ObjectSchema(
				api.Property{
					Name:     "document",
					Required: true,
					Schema:   &api.Schema{Type: api.TypeString, Description: "The document, as JSON."},
				},
				api.Property{
					Name: "meta",
					Schema: api.ObjectSchema(
						api.Property{Name: "author", Schema: api.StringSchema()},
						api.Property{Name: "version", Schema: &api.Schema{Type: api.TypeInteger}},
					),
				},
			),
		},
		api.Operation{
			Name:    "replaceEverything",
			Method:  "PUT",
			Path:    "/documents/all",
			Request: &api.Schema{Type: api.TypeString, Description: "The whole store, verbatim."},
		},
	)
	root := generate(t, described_theAPI, &recorder{}, cli.Options{CommandName: "test"})
	fromTree, fromText := describeTree(t, root)
	sameReading(t, fromTree, fromText)

	require.NoError(t, described_theAPI.AnnotateOperationIDs())
	want := operationsByID(t, described_theAPI)
	got := operationsByID(t, fromTree)
	require.Equal(t, sortedIDs(want), sortedIDs(got))
	for id, operation := range want {
		assertSameSchema(t, id+" request", operation.Request, got[id].Request)
	}
}

// A default the description declares is the flag's default, so a caller reads the
// same value in the help as in the description — and the default survives the trip
// back into a description.
func TestADeclaredDefaultSurvivesTheRoundTrip(t *testing.T) {
	described_theAPI := things(api.Operation{
		Name:   "listThings",
		Method: "GET",
		Parameters: []api.Parameter{
			{Name: "pageSize", In: api.ParameterInQuery, Schema: &api.Schema{Type: api.TypeInteger, Default: 20}},
			{Name: "sort", In: api.ParameterInQuery, Schema: &api.Schema{Type: api.TypeString, Default: "name"}},
		},
	})
	root := generate(t, described_theAPI, &recorder{}, cli.Options{CommandName: "test"})

	command := find(t, root, "things", "list-things")
	pageSize := command.Flags().Lookup("pageSize")
	require.NotNil(t, pageSize)
	assert.Equal(t, "20", pageSize.DefValue, "a flag's default is the one the description declares")
	assert.Equal(t, "name", command.Flags().Lookup("sort").DefValue)

	fromTree, fromText := describeTree(t, root)
	sameReading(t, fromTree, fromText)
	parameters := fromTree.Operations()[0].Parameters
	byName := map[string]api.Parameter{}
	for _, parameter := range parameters {
		byName[parameter.Name] = parameter
	}
	assert.Equal(t, int64(20), byName["pageSize"].Schema.Default)
	assert.Equal(t, "name", byName["sort"].Schema.Default)
}

// What a help page cannot state is not invented, and the degradations are the ones
// the package documents: an array's element shape is lost, an argument is not an
// input, and the order of a request's members becomes the order a flag block is
// printed in.
func TestWhatAPageCannotStateIsNotInvented(t *testing.T) {
	described_theAPI := things(api.Operation{
		Name:   "createThing",
		Method: "POST",
		Request: api.ObjectSchema(
			api.Property{Name: "zebra", Schema: api.StringSchema()},
			api.Property{Name: "apple", Schema: api.StringSchema()},
			api.Property{
				Name: "friends",
				Schema: api.ArraySchema(api.ObjectSchema(
					api.Property{Name: "id", Required: true, Schema: api.StringSchema()},
				)),
			},
		),
	})
	root := generate(t, described_theAPI, &recorder{}, cli.Options{CommandName: "test"})
	fromTree, _ := describeTree(t, root)
	request := fromTree.Operations()[0].Request
	require.NotNil(t, request)

	friends, present := request.Property("friends")
	require.True(t, present)
	require.Equal(t, api.TypeArray, friends.Schema.Type)
	require.NotNil(t, friends.Schema.Items)
	assert.Equal(t, api.TypeUnspecified, friends.Schema.Items.Type,
		"a repeated value is printed as stringArray: nothing states what its elements are")

	assert.Equal(t, []string{"apple", "friends", "zebra"}, propertyNames(request),
		"pflag prints a flag block in one order, so the members come back sorted")
}

// A hidden command and a hidden flag are the tool's own business: it does not
// advertise them, so a description of what the tool offers does not claim them.
func TestWhatAToolHidesIsNotDescribed(t *testing.T) {
	root := generate(t, petstore(), &recorder{}, cli.Options{CommandName: "petstore"})
	secret := &cobra.Command{Use: "secret", Short: "Not advertised.", RunE: func(*cobra.Command, []string) error { return nil }}
	secret.Hidden = true
	root.AddCommand(secret)
	root.PersistentFlags().String("invisible", "", "Not advertised either.")
	require.NoError(t, root.PersistentFlags().MarkHidden("invisible"))

	fromTree, _ := describeTree(t, root)
	for _, operation := range fromTree.Operations() {
		assert.NotContains(t, operation.Name, "secret")
		assert.NotContains(t, strings.Join(parameterNames(operation), ","), "invisible")
	}
}

// A deprecated command still runs, so it is still described — and it is described
// from the tree, which knows it, although no page lists it. That is the one place
// the two readings differ, and it is stated rather than silent.
func TestADeprecatedCommandIsDescribedFromTheTree(t *testing.T) {
	root := generate(t, petstore(), &recorder{}, cli.Options{CommandName: "petstore"})
	old := &cobra.Command{Use: "old-pets", Short: "The old way.", Deprecated: "use list-pets", RunE: func(*cobra.Command, []string) error { return nil }}
	root.AddCommand(old)

	fromTree, _ := cli.Describe(root, cli.DescribeOptions{})
	describedOperation, ok := fromTree.Operation("petstore/petstore/old-pets")
	require.True(t, ok, "the tree knows the command: %v", fromTree.OperationIDs())
	assert.True(t, describedOperation.Deprecated)
	assert.Equal(t, "The old way.", describedOperation.Summary)
}

// An override renames the API and, because an identifier is built from it, every
// operation with it.
func TestTheAPIIdentifierCanBeOverridden(t *testing.T) {
	root := generate(t, petstore(), &recorder{}, cli.Options{CommandName: "petstore"})
	described, err := cli.Describe(root, cli.DescribeOptions{APIID: "store", Title: "The store", Version: "2"})
	require.NoError(t, err)
	assert.Equal(t, "store", described.ID)
	assert.Equal(t, "The store", described.Title)
	assert.Equal(t, "2", described.Version)
	assert.Contains(t, described.OperationIDs(), "store/pets/getPet")
}

// A description read out of a tree is a well-formed description: a caller may
// register it, publish it, and generate a command line from it.
func TestTheDescribedAPIIsWellFormed(t *testing.T) {
	root := generate(t, petstore(), &recorder{}, cli.Options{CommandName: "petstore"})
	described, _ := describeTree(t, root)

	assert.NoError(t, described.Validate())
	assert.Equal(t, cli.Format, described.Format)
	assert.Equal(t, cli.Transport, described.Transport)
	again, err := cli.Generate(described, &recorder{}, cli.Options{CommandName: "petstore"})
	require.NoError(t, err, "a description read back can build a tree, or it is not a description")
	assert.NotNil(t, findChild(again, "pets"))
}

// Reading a tree does not run it and does not rewrite it: the fixes below are
// built on a tree that is described and then executed, and a reader that added a
// flag or a hook of its own would show up there.
func TestDescribingATreeDoesNotChangeIt(t *testing.T) {
	root := generate(t, petstore(), &recorder{}, cli.Options{CommandName: "petstore"})
	before := len(find(t, root, "pets", "get-pet").Flags().Args())
	_, err := cli.HelpText(root)
	require.NoError(t, err)
	_, err = cli.Describe(root, cli.DescribeOptions{})
	require.NoError(t, err)

	call, out, err := run(t, "pets", "get-pet", "--petId", "7")
	require.NoError(t, err)
	assert.Equal(t, before, len(find(t, root, "pets", "get-pet").Flags().Args()))
	assert.Equal(t, map[string]any{"petId": "7"}, arguments(t, call))
	assert.JSONEq(t, `{"ok":true}`, out)
}

// Describe refuses nothing it cannot name, and names the rest.
func TestDescribeRefusesWhatItCannotDescribe(t *testing.T) {
	_, err := cli.Describe(nil, cli.DescribeOptions{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))

	_, err = cli.DescribeHelp("", cli.DescribeOptions{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
}

// assertSameProse compares what an operation is documented as, allowing the one
// distinction a page cannot make.
//
// A page prints one prose block: the description when there is one, and the
// summary when there is not. So an operation the description left undocumented
// comes back documented by the sentence the page printed in its place, and an
// operation with no summary of its own comes back summarized by its wire line,
// which is the fallback the generator itself uses.
func assertSameProse(t *testing.T, id string, want, got api.Operation) {
	t.Helper()
	if want.Description != "" {
		assert.Equal(t, collapse(want.Description), collapse(got.Description),
			"%s: the prose survives, its line wrapping does not", id)
	} else {
		// The page printed the summary in the description's place, so that is
		// exactly what comes back — no more, and nothing invented.
		assert.Contains(t, []string{"", want.Summary}, got.Description,
			"%s: the prose a page states is the description, or the summary standing in for one", id)
	}
	if want.Summary != "" {
		assert.Equal(t, want.Summary, got.Summary, id)
		return
	}
	fallbacks := []string{got.Description, strings.TrimSpace(want.Method + " " + want.Path)}
	assert.Contains(t, fallbacks, got.Summary, "%s: a page summarizes an operation by its prose or its wire line", id)
}

// operationsByID indexes a description's operations, which is how two descriptions
// are compared: a page lists its flags in one order, so matching by position would
// compare one operation against another.
func operationsByID(t *testing.T, described api.API) map[string]api.Operation {
	t.Helper()
	operations := make(map[string]api.Operation, len(described.Services))
	for _, operation := range described.Operations() {
		operations[operation.ID] = operation
	}
	return operations
}

func sortedIDs(operations map[string]api.Operation) []string {
	ids := make([]string, 0, len(operations))
	for id := range operations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func serviceNames(described api.API) []string {
	names := make([]string, 0, len(described.Services))
	for _, service := range described.Services {
		names = append(names, service.Name)
	}
	sort.Strings(names)
	return names
}

func propertyNames(schema *api.Schema) []string {
	if schema == nil {
		return nil
	}
	names := make([]string, 0, len(schema.Properties))
	for _, property := range schema.Properties {
		names = append(names, property.Name)
	}
	sort.Strings(names)
	return names
}

// writablePropertyNames is the members a command line can state: one the
// description marks read-only is returned but not accepted, so no flag offers it.
func writablePropertyNames(schema *api.Schema) []string {
	if schema == nil {
		return nil
	}
	names := make([]string, 0, len(schema.Properties))
	for _, property := range schema.Properties {
		if property.Schema != nil && property.Schema.ReadOnly {
			continue
		}
		names = append(names, property.Name)
	}
	sort.Strings(names)
	return names
}

func parameterNames(operation api.Operation) []string {
	names := make([]string, 0, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		names = append(names, parameter.Name)
	}
	return names
}

func collapse(text string) string { return strings.Join(strings.Fields(text), " ") }

// assertSameSchema compares two value shapes as far as a help page can state one.
//
// The comparison is narrower than a deep equal on purpose, and each gap is a fact
// about the format rather than a tolerance: a repeated value is printed as
// stringArray with its element type in the help text, so an array of objects comes
// back as an array of unknown elements; a member's order is the order a flag block
// is printed in, which pflag sorts.
func assertSameSchema(t *testing.T, path string, want, got *api.Schema) {
	t.Helper()
	if want == nil || got == nil {
		assert.Equal(t, want == nil, got == nil, "%s: one side states a value and the other does not", path)
		return
	}
	assert.Equal(t, want.Type, got.Type, path+": type")
	assert.Equal(t, collapse(want.Description), collapse(got.Description), path+": description")
	assert.Equal(t, want.Enum, got.Enum, path+": enum")
	assert.Equal(t, want.Deprecated, got.Deprecated, path+": deprecated")
	assert.Equal(t, sortedCopy(want.Required), sortedCopy(got.Required), path+": required")
	assert.Equal(t, writablePropertyNames(want), propertyNames(got), path+": member names")
	for _, property := range want.Properties {
		if property.Schema != nil && property.Schema.ReadOnly {
			// A member the description says is returned but not accepted gets no
			// flag, because offering one would promise an input the operation
			// ignores. Nothing on the command line states it, so it is not read
			// back — the one member a page is allowed to omit.
			_, present := got.Property(property.Name)
			assert.False(t, present, "%s: the read-only member %q must not become an input", path, property.Name)
			continue
		}
		read, present := got.Property(property.Name)
		require.True(t, present, "%s: member %q is missing", path, property.Name)
		assert.Equal(t, property.Required, read.Required, path+"."+property.Name+": required")
		assertSameSchema(t, path+"."+property.Name, property.Schema, read.Schema)
	}
	if want.Type == api.TypeArray || got.Type == api.TypeArray {
		if got.Items != nil && got.Items.Type != api.TypeUnspecified {
			assertSameSchema(t, path+"[]", want.Items, got.Items)
		}
	}
}

func sortedCopy(values []string) []string {
	copied := append([]string(nil), values...)
	sort.Strings(copied)
	return copied
}
