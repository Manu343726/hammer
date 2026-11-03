package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The generator has a branch per value shape, per flag carrier, and per declared
// default, and every one of those is invisible until a description exercises it:
// a carrier that reads a value back as the wrong type, or a declared default that
// is dropped because it arrived as a json.Number rather than an int, only shows up
// when that exact description is generated. The fixture below declares one of each
// so each test can name the shape it is about instead of asserting on a whole
// command tree.

// covFailWriter fails the Nth write, so a test can fail the body write and the
// trailing newline separately: printResult has a distinct error path for each, and
// a result that is half written must still be reported rather than swallowed.
type covFailWriter struct {
	writes int
	failAt int
}

func (w *covFailWriter) Write(p []byte) (int, error) {
	if w.writes >= w.failAt {
		w.writes++
		return 0, errors.New("the reader went away")
	}
	w.writes++
	return len(p), nil
}

// covRichAPI is a description that declares every value shape the generator
// distinguishes, on a tree that has a service and an operation to hang them on.
func covRichAPI() api.API {
	return api.API{
		ID:          "rich",
		Name:        "rich",
		Title:       "Rich",
		Format:      "openapi",
		Transport:   "http",
		Description: "",
		DeclaredServers: []api.DeclaredServer{{
			URL: "https://rich.example.com",
		}},
		Services: []api.Service{{
			Name:        "widgets",
			Description: "Operations on widgets.",
			SideEffects: []api.SideEffect{"create", "update"},
			Operations: []api.Operation{
				{
					Name:    "listWidgets",
					Method:  "GET",
					Path:    "/widgets",
					Summary: "Lists widgets.",
				},
				{
					Name:       "retireWidget",
					Summary:    "Retires a widget.",
					Deprecated: true,
					Streaming:  api.Streaming{Client: true},
				},
				{
					Name:    "countWidgets",
					Summary: "Counts widgets.",
					Parameters: []api.Parameter{
						{
							Name:        "flag",
							In:          api.ParameterInQuery,
							Description: "A boolean.",
							Schema:      &api.Schema{Type: api.TypeBoolean},
						},
						{
							Name:        "ratio",
							In:          api.ParameterInQuery,
							Description: "A number.",
							Schema:      &api.Schema{Type: api.TypeNumber},
						},
						{
							Name: "nothing",
							In:   api.ParameterInQuery,
						},
						{
							Name:        "absent",
							In:          api.ParameterInHeader,
							Description: "A value of no stated type.",
							Schema:      &api.Schema{Type: api.TypeNull},
						},
						{
							Name:        "old",
							In:          api.ParameterInCookie,
							Description: "An input that should not be used.",
							Deprecated:  true,
							Required:    true,
							Schema:      api.StringSchema(),
						},
					},
				},
				{
					Name:    "shapeWidget",
					Summary: "Shapes a widget.",
					Request: api.ObjectSchema(
						api.Property{Name: "bells", Schema: &api.Schema{
							Type:        api.TypeBoolean,
							Description: "Whether to ring the bell.",
							Default:     true,
						}},
						api.Property{Name: "anInt", Schema: &api.Schema{
							Type: api.TypeInteger, Default: 7, Description: "An int default.",
						}},
						api.Property{Name: "anInt64", Schema: &api.Schema{
							Type: api.TypeInteger, Default: int64(8),
						}},
						api.Property{Name: "aWholeFloat", Schema: &api.Schema{
							Type: api.TypeInteger, Default: float64(9),
						}},
						api.Property{Name: "aFractionalFloat", Schema: &api.Schema{
							Type: api.TypeInteger, Default: 9.5,
						}},
						api.Property{Name: "aJSONNumber", Schema: &api.Schema{
							Type: api.TypeInteger, Default: json.Number("10"),
						}},
						api.Property{Name: "aBadJSONNumber", Schema: &api.Schema{
							Type: api.TypeInteger, Default: json.Number("ten"),
						}},
						api.Property{Name: "aString", Schema: &api.Schema{
							Type: api.TypeInteger, Default: " 11 ",
						}},
						api.Property{Name: "aBadString", Schema: &api.Schema{
							Type: api.TypeInteger, Default: "eleven",
						}},
						api.Property{Name: "aDecimal", Schema: &api.Schema{
							Type: api.TypeNumber, Default: 1.5,
						}},
						api.Property{Name: "aDecimalInt", Schema: &api.Schema{
							Type: api.TypeNumber, Default: 2,
						}},
						api.Property{Name: "aDecimalInt64", Schema: &api.Schema{
							Type: api.TypeNumber, Default: int64(3),
						}},
						api.Property{Name: "aDecimalJSON", Schema: &api.Schema{
							Type: api.TypeNumber, Default: json.Number("4.5"),
						}},
						api.Property{Name: "aDecimalString", Schema: &api.Schema{
							Type: api.TypeNumber, Default: " 5.5 ",
						}},
						api.Property{Name: "aBadDecimal", Schema: &api.Schema{
							Type: api.TypeNumber, Default: struct{}{},
						}},
						api.Property{Name: "texts", Schema: &api.Schema{
							Type:        api.TypeArray,
							Items:       api.StringSchema(),
							Description: "Strings.",
							Default:     []string{"a", "b"},
						}},
						api.Property{Name: "mixed", Schema: &api.Schema{
							Type:    api.TypeArray,
							Items:   api.StringSchema(),
							Default: []any{"a", true, 1.5, 2, int64(3), json.Number("4"), struct{}{}},
						}},
						api.Property{Name: "flags", Schema: &api.Schema{
							Type:  api.TypeArray,
							Items: &api.Schema{Type: api.TypeBoolean},
						}},
						api.Property{Name: "ratios", Schema: &api.Schema{
							Type:  api.TypeArray,
							Items: &api.Schema{Type: api.TypeNumber},
						}},
						api.Property{Name: "untypedElements", Schema: &api.Schema{
							Type:  api.TypeArray,
							Items: &api.Schema{},
						}},
						api.Property{Name: "states", Schema: &api.Schema{
							Type:  api.TypeArray,
							Items: &api.Schema{Type: api.TypeString, Enum: []string{"on", "off"}},
						}},
						api.Property{Name: "untypedArray", Schema: &api.Schema{
							Items: api.StringSchema(),
						}},
						api.Property{Name: "untaggedObject", Schema: &api.Schema{
							Properties: []api.Property{
								{Name: "leaf", Schema: api.StringSchema(), Required: true},
							},
						}},
						api.Property{Name: "either", Schema: &api.Schema{
							OneOf: []*api.Schema{api.StringSchema(), &api.Schema{Type: api.TypeInteger}},
						}},
						api.Property{Name: "anull", Schema: &api.Schema{Type: api.TypeNull}},
						api.Property{Name: "with space", Schema: api.StringSchema()},
						api.Property{Name: "shapeless"},
						api.Property{Name: "body", Schema: api.StringSchema()},
						api.Property{Name: "prompt", Schema: api.ObjectSchema(
							api.Property{Name: "id", Schema: api.StringSchema(), Required: true},
						)},
						api.Property{Name: "sealed", Schema: api.ObjectSchema(
							api.Property{Name: "hidden", Schema: &api.Schema{
								Type: api.TypeString, ReadOnly: true,
							}},
							api.Property{Name: "shown", Schema: api.StringSchema()},
						)},
					),
				},
			},
		}},
	}
}

// covTree builds the rich tree and returns it with the invoker behind it.
func covTree(t *testing.T) (*cobra.Command, *recorder) {
	t.Helper()
	invoker := &recorder{result: api.Result{Body: json.RawMessage(`{"ok":true}`)}}
	return generate(t, covRichAPI(), invoker, cli.Options{}), invoker
}

// An API whose only service declares no operation still has to be a tree a caller
// can type into: the service command exists, and running it prints its own help
// rather than failing as an unknown command. A service with no operations is what
// a description of an empty contract produces, and it is the shape that would
// otherwise be dropped from help silently.
func TestAServiceWithNoOperationsIsStillACommand(t *testing.T) {
	described := api.API{
		ID:   "hollow",
		Name: "hollow",
		Services: []api.Service{{
			Name:        "things",
			Description: "Operations on things.",
		}},
	}
	root := generate(t, described, &recorder{}, cli.Options{})

	require.NotNil(t, find(t, root, "things"))
	out, err := execute(t, root, "things")
	require.NoError(t, err, "running the group command prints its help rather than failing")
	assert.Contains(t, out, "Usage:")
	assert.Contains(t, out, "things")
}

// A description that states no prose of its own still needs a summary and a long
// help, because the root is the page a caller reads first. Falling back to the ID
// rather than to an empty string is what keeps the page from reading as an
// undocumented command.
func TestARootWithNoProseFallsBackToItsOwnWords(t *testing.T) {
	described := api.API{
		ID:    "bare",
		Title: "A title with no description.",
	}
	root := generate(t, described, &recorder{}, cli.Options{})

	assert.Equal(t, "A title with no description.", root.Short,
		"with no description and no name, the title is the one line the page can state")
	assert.Equal(t, "A title with no description.", root.Long,
		"and the title is also the long help when there is no description to print")

	// A description with nothing at all still gets the sentence this package has
	// always used, because a help page with a blank lead reads as a broken one.
	silent := generate(t, api.API{ID: "silent"}, &recorder{}, cli.Options{})
	assert.Equal(t, "Commands generated from an API description", silent.Short)
}

// The root's own run prints its help: a caller who types the program's name is
// asking what it can do, and cobra would otherwise print nothing at all for a
// command with children and no run.
func TestTheRootPrintsItsOwnHelp(t *testing.T) {
	root, invoker := covTree(t)

	out, err := execute(t, root)
	require.NoError(t, err)
	assert.Contains(t, out, "Usage:")
	assert.Contains(t, out, "Available Commands:")
	assert.Empty(t, invoker.calls, "printing help is not a call")
}

// An override replaces the API's own description in the root help, which is what a
// host composing this tree needs: the description it read belongs to the document,
// and the sentence a caller reads belongs to the host.
func TestTheRootDescriptionCanBeOverridden(t *testing.T) {
	root := generate(t, covRichAPI(), &recorder{}, cli.Options{Description: "  Chosen by the host.  "})

	assert.Contains(t, root.Long, "Chosen by the host.")
	assert.NotContains(t, root.Long, covRichAPI().Title,
		"the host's sentence replaces the description, and the rest of the page stays")
}

// Execute turns a tree into an exit code, and a nil tree is the one input it cannot
// call: returning a code rather than panicking keeps a host's error path intact.
func TestExecuteWithoutATreeFails(t *testing.T) {
	assert.Equal(t, cli.ExitFailure, cli.Execute(nil))
}

// A failure with no classification is printed without one. "internal: boom" over a
// transport error would read as if the caller had done something wrong, which is
// the opposite of what an unclassified failure says.
func TestAnUnclassifiedFailureIsPrintedWithoutAKind(t *testing.T) {
	invoker := &recorder{err: errors.New("boom")}
	root := generate(t, covRichAPI(), invoker, cli.Options{})
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"widgets", "list-widgets"})

	require.Equal(t, cli.ExitFailure, cli.Execute(root))
	assert.Equal(t, "rich: call \"rich/widgets/listWidgets\": boom\n", out.String(),
		"a failure with no classification prints the command and the message, and no kind")
}

// The response body is the command's whole output, so a failure to write it has to
// be reported as a failure of the call. Reporting success after a truncated write
// would let a pipeline consume half a document and believe it.
func TestAResultThatCannotBeWrittenIsAnError(t *testing.T) {
	for _, failAt := range []int{0, 1} {
		invoker := &recorder{result: api.Result{Body: json.RawMessage(`{"ok":true}`)}}
		root := generate(t, covRichAPI(), invoker, cli.Options{})
		root.SetOut(&covFailWriter{failAt: failAt})
		root.SetArgs([]string{"widgets", "list-widgets"})

		err := root.Execute()
		require.Error(t, err, "a write that failed must not be reported as a successful call (failure at %d)", failAt)
		assert.True(t, api.IsKind(err, api.KindUnavailable), "a broken output stream is an availability failure: %v", err)
		assert.Contains(t, err.Error(), "write the response")
	}
}

// A parameter states where it travels, and each of the five documented locations is
// a fact the caller needs before typing: the flag's help states it, because the
// same name in two locations is two different values.
func TestEveryParameterLocationIsStatedInItsHelp(t *testing.T) {
	root, _ := covTree(t)
	command := find(t, root, "widgets", "count-widgets")

	for _, name := range []string{"flag", "ratio", "nothing", "absent", "old"} {
		require.NotEmpty(t, flagUsage(command, name), "flag --%s must document itself", name)
	}
	assert.Contains(t, flagUsage(command, "flag"), "(query)")
	assert.Contains(t, flagUsage(command, "absent"), "(JSON value; header)")
	assert.Contains(t, flagUsage(command, "old"), "(required; deprecated; cookie)",
		"a required, deprecated cookie parameter states all three facts")
	assert.Equal(t, "string (query)", flagUsage(command, "nothing"),
		"an undocumented value falls back to its type rather than to invented prose")
}

// A value whose shape the description does not classify travels as JSON, and the
// help says which JSON shape is accepted: an object flag refuses an array, and a
// caller needs to know that before typing one.
func TestAValueOfNoStatedTypeIsCarriedAsJSON(t *testing.T) {
	root, invoker := covTree(t)
	command := find(t, root, "widgets", "shape-widget")

	assert.Contains(t, flagUsage(command, "either"), "JSON value")
	assert.Contains(t, flagUsage(command, "anull"), "JSON value")

	_, err := execute(t, root, "widgets", "shape-widget", "--either", `{"a":1}`, "--anull", "null")
	require.NoError(t, err)
	body, ok := arguments(t, invoker.last(t))["body"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"a": float64(1)}, body["either"])
	assert.Nil(t, body["anull"], "a JSON null is a value the caller stated, and it is sent")
}

// A nested object is offered as one JSON document and as dotted flags, and the
// object's own flag keeps the schema's comment. A description of an object whose
// members are all read-only has nothing to flatten, and the document flag is then
// the only way to send it.
func TestANestedObjectIsADocumentAndItsMembersAreFlags(t *testing.T) {
	root, invoker := covTree(t)
	command := find(t, root, "widgets", "shape-widget")

	assert.Contains(t, flagUsage(command, "prompt"), "JSON object")
	assert.Contains(t, flagUsage(command, "prompt.id"), "required",
		"a member's required-ness survives the flattening")

	_, err := execute(t, root, "widgets", "shape-widget", "--prompt.id", "intro", "--sealed", `{"shown":"x"}`)
	require.NoError(t, err)
	body, ok := arguments(t, invoker.last(t))["body"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"id": "intro"}, body["prompt"])
	assert.Equal(t, map[string]any{"shown": "x"}, body["sealed"])
}

// A read-only member is not an input, so it gets no flag — and neither does one
// nested inside an object that is otherwise flattened, which is the branch a
// top-level-only check would miss.
func TestAReadOnlyMemberOfANestedObjectGetsNoFlag(t *testing.T) {
	root, _ := covTree(t)
	command := find(t, root, "widgets", "shape-widget")

	assert.NotNil(t, command.Flags().Lookup("sealed.shown"))
	assert.Nil(t, command.Flags().Lookup("sealed.hidden"), "a read-only member is never an input")
}

// An array of scalars takes one value per occurrence, and its element type is
// stated in words because pflag prints every repeated value as a string array.
// The words are what a caller reads to know whether --flags takes true or 1.
func TestAnArrayStatesItsElementTypeInWords(t *testing.T) {
	root, invoker := covTree(t)
	command := find(t, root, "widgets", "shape-widget")

	assert.Contains(t, flagUsage(command, "flags"), "list of booleans")
	assert.Contains(t, flagUsage(command, "ratios"), "list of numbers")
	assert.Contains(t, flagUsage(command, "untypedElements"), "list of strings",
		"an element schema with no type is text")
	assert.Contains(t, flagUsage(command, "states"), "one of: on, off",
		"an enumeration belongs to the element of a repeated value")

	_, err := execute(t, root, "widgets", "shape-widget",
		"--flags", "true", "--flags", "false",
		"--ratios", "1.5", "--ratios", "2")
	require.NoError(t, err)
	body, ok := arguments(t, invoker.last(t))["body"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{true, false}, body["flags"])
	assert.Equal(t, []any{float64(1.5), float64(2)}, body["ratios"])
}

// An element the flag cannot parse is refused by name rather than sent as text: a
// boolean array that carried "yes" would be a call the server rejects in its own
// words, after a round trip the caller did not need.
func TestAnArrayElementOfTheWrongTypeIsRefusedLocally(t *testing.T) {
	for _, test := range []struct {
		flag  string
		value string
		want  string
	}{
		{"flags", "yes", `"yes" is not a boolean`},
		{"ratios", "many", `"many" is not a number`},
		{"states", "maybe", `"maybe" is not one of on, off`},
	} {
		root, invoker := covTree(t)
		_, err := execute(t, root, "widgets", "shape-widget", "--"+test.flag, test.value)
		require.Error(t, err, "--%s %q must be refused", test.flag, test.value)
		assert.True(t, api.IsKind(err, api.KindInvalid))
		assert.Contains(t, err.Error(), test.want)
		assert.Empty(t, invoker.calls, "a value the flag refuses never reaches the invoker")
	}
}

// A declared default is what the help shows and what the configuration layer
// resolves against, and a description parsed from a document states its numbers as
// floats and its lists as untyped elements. Each spelling has to reach the flag as
// the type the flag parses, or the help prints a default nobody declared.
func TestADeclaredDefaultIsCarriedHoweverTheDocumentSpeltIt(t *testing.T) {
	root, _ := covTree(t)
	command := find(t, root, "widgets", "shape-widget")

	for name, want := range map[string]string{
		"anInt":            "7",
		"anInt64":          "8",
		"aWholeFloat":      "9",
		"aFractionalFloat": "0",
		"aJSONNumber":      "10",
		"aBadJSONNumber":   "0",
		"aString":          "11",
		"aBadString":       "0",
		"aDecimal":         "1.5",
		"aDecimalInt":      "2",
		"aDecimalInt64":    "3",
		"aDecimalJSON":     "4.5",
		"aDecimalString":   "5.5",
		"aBadDecimal":      "0",
	} {
		flag := command.Flags().Lookup(name)
		require.NotNil(t, flag, "--%s must exist", name)
		assert.Equal(t, want, flag.DefValue, "--%s carries the declared default", name)
	}

	assert.Equal(t, "true", command.Flags().Lookup("bells").DefValue,
		"a boolean default is the boolean the document declared")
	assert.Equal(t, "[a,b]", command.Flags().Lookup("texts").DefValue,
		"a list default is the list the document declared, one element per occurrence")
	assert.Equal(t, "[a,true,1.5,2,3,4,{}]",
		command.Flags().Lookup("mixed").DefValue,
		"each declared element is rendered as the word the flag parses")
}

// A value whose schema is an array without a declared type is still an array on the
// command line: the shape is read from the element schema, and a list of objects is
// one JSON document because no dotted flag can address --list.0.id.
func TestAnArrayWhoseTypeIsOnlyInItsElementsIsStillAnArray(t *testing.T) {
	root, _ := covTree(t)
	command := find(t, root, "widgets", "shape-widget")

	assert.Equal(t, "stringArray", command.Flags().Lookup("untypedArray").Value.Type())
	assert.Equal(t, "stringArray", command.Flags().Lookup("untypedElements").Value.Type())
	assert.Equal(t, "string", command.Flags().Lookup("untaggedObject").Value.Type(),
		"an object that states its members is still an object, and its document is a string flag")
}

// A member whose name carries a space is typed with a hyphen, because a shell
// splits a flag name at whitespace: the description's own spelling is kept
// everywhere the space is readable, and only the word a caller types is changed.
func TestAMemberWhoseNameCarriesASpaceIsTypedWithAHyphen(t *testing.T) {
	root, invoker := covTree(t)
	command := find(t, root, "widgets", "shape-widget")

	require.NotNil(t, command.Flags().Lookup("with-space"), "a space cannot appear in a flag name")
	_, err := execute(t, root, "widgets", "shape-widget", "--with-space", "x")
	require.NoError(t, err)
	body, _ := arguments(t, invoker.last(t))["body"].(map[string]any)
	assert.Equal(t, map[string]any{"with space": "x"}, body,
		"the value is sent under the description's own spelling")
}

// A request member named "body" collides with the flag for the whole request, which
// is the one name the generator adds rather than takes from the description.
// Neither may shadow the other: the member keeps its own name qualified by the body
// it belongs to, and the document keeps the bare word.
func TestAMemberNamedBodyDoesNotShadowTheWholeRequest(t *testing.T) {
	root, invoker := covTree(t)
	command := find(t, root, "widgets", "shape-widget")

	require.NotNil(t, command.Flags().Lookup("body"))
	require.NotNil(t, command.Flags().Lookup("body.body"))

	_, err := execute(t, root, "widgets", "shape-widget", "--body.body", "flat")
	require.NoError(t, err)
	body, ok := arguments(t, invoker.last(t))["body"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"body": "flat"}, body)
}

// Two inputs cannot share one flag name: the second registration would be refused
// by pflag, or would silently replace the first. A parameter without a location is
// still placeable, and a name claimed twice is qualified for both claimants so
// neither is the short one by accident of declaration order.
func TestTwoParametersWithOneNameAreBothReachable(t *testing.T) {
	described := api.API{
		ID:   "twins",
		Name: "twins",
		Services: []api.Service{{
			Name: "things",
			Operations: []api.Operation{{
				Name: "read",
				Parameters: []api.Parameter{
					{Name: "id", Schema: api.StringSchema()},
					{Name: "id", In: api.ParameterInQuery, Schema: api.StringSchema()},
				},
			}},
		}},
	}
	root := generate(t, described, &recorder{}, cli.Options{})
	command := find(t, root, "things", "read")

	assert.Nil(t, command.Flags().Lookup("id"), "neither claimant keeps the bare name")
	assert.NotNil(t, command.Flags().Lookup("parameter.id"),
		"a parameter that states no location is still qualified by one")
	assert.NotNil(t, command.Flags().Lookup("query.id"))
}

// Two parameters in one location with one name qualify to one word, and the second
// still has to be reachable: registration order breaks the tie with a suffix, so
// neither input disappears behind pflag's refusal to register a duplicate.
func TestTwoParametersInOneLocationKeepDistinctFlags(t *testing.T) {
	described := api.API{
		ID:   "twins",
		Name: "twins",
		Services: []api.Service{{
			Name: "things",
			Operations: []api.Operation{{
				Name: "read",
				Parameters: []api.Parameter{
					{Name: "id", In: api.ParameterInQuery, Schema: api.StringSchema()},
					{Name: "id", In: api.ParameterInQuery, Schema: api.StringSchema()},
				},
			}},
		}},
	}
	root := generate(t, described, &recorder{}, cli.Options{})
	command := find(t, root, "things", "read")

	require.NotNil(t, command.Flags().Lookup("query.id"))
	assert.NotNil(t, command.Flags().Lookup("query.id-2"),
		"the two qualified names collide too, and the second stays reachable under a suffix")
}

// An operation that streams is still a command — its help documents the inputs it
// takes — and running it says which operation cannot be called rather than
// pretending it worked. A client-streaming operation is the direction a
// server-streaming-only check would miss.
func TestAClientStreamingOperationIsRefusedByName(t *testing.T) {
	root, invoker := covTree(t)

	_, err := execute(t, root, "widgets", "retire-widget")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindUnsupported))
	assert.Contains(t, err.Error(), "rich/widgets/retireWidget")
	assert.Contains(t, err.Error(), "unary")
	assert.Empty(t, invoker.calls)
}

// A deprecated operation says so in the one-line summary and in its long help, so a
// caller who found the command by completion reads the warning before running it.
// The tree knows the deprecation; a help page does not, which is why it is stated
// here rather than left to the description.
func TestADeprecatedOperationSaysSoInItsHelp(t *testing.T) {
	root, _ := covTree(t)
	command := find(t, root, "widgets", "retire-widget")

	assert.Contains(t, command.Short, "(deprecated)")
	assert.Contains(t, command.Short, "(streaming)")
	assert.Contains(t, command.Long, "This operation is deprecated and should not be used for new work.")
	assert.Contains(t, command.Long, "This operation streams requests, and a command line invokes unary operations only.")
}

// A service's declared side effects are stated on the group's own page, because a
// caller who is about to pick one of its operations needs to know that the whole
// group writes before reading any single operation's page.
func TestAServicesSideEffectsAreStatedOnItsPage(t *testing.T) {
	root, _ := covTree(t)
	assert.Contains(t, find(t, root, "widgets").Long, "Declared side effects: create, update.")
}

// A number flag can parse NaN and Inf, which are values JSON cannot carry. The
// failure has to be reported as the operation's arguments being unencodable rather
// than passed to an invoker that would fail in its own words.
func TestANumberJSONCannotCarryIsRefusedBeforeTheCall(t *testing.T) {
	root, invoker := covTree(t)

	_, err := execute(t, root, "widgets", "count-widgets", "--ratio", "NaN", "--old", "x")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInternal))
	assert.Contains(t, err.Error(), "encode the arguments of")
	assert.Empty(t, invoker.calls)

	// The flag does parse the value, so the refusal is the encoder's and not the
	// parser's: this is the branch a "parse everything as text" design would not have.
	parsed, flagErr := find(t, root, "widgets", "count-widgets").Flags().GetFloat64("ratio")
	require.NoError(t, flagErr)
	assert.True(t, math.IsNaN(parsed))
}

// A dotted flag writes into the object the caller supplied as JSON, and an object
// that is not an object cannot be written into. Reporting the member that is in the
// way is what tells the caller which of two flags disagreed.
func TestADottedFlagCannotWriteInsideAValueThatIsNotAnObject(t *testing.T) {
	root, invoker := covTree(t)

	_, err := execute(t, root, "widgets", "shape-widget", "--body", `{"prompt":5}`, "--prompt.id", "intro")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "prompt is not a JSON object")
	assert.Empty(t, invoker.calls)
}

// A required member of an optional object is only required when the object is
// present: the description says the object may be left out, so demanding a member
// of it on its own would refuse a call the description allows. When the object is
// present without the member, the refusal names the flag.
func TestARequiredMemberOfAPresentObjectIsRefused(t *testing.T) {
	root, invoker := covTree(t)

	_, err := execute(t, root, "widgets", "shape-widget", "--prompt", `{}`)
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "--prompt.id is required")
	assert.Empty(t, invoker.calls)

	call, _, err := run(t, "pets", "create-pet", "--name", "Rex")
	require.NoError(t, err, "leaving the object out entirely is still allowed")
	assert.NotNil(t, call)
}

// A body parameter — one the description carries inside the request value rather
// than beside it — composes into the body, and a member of it that states no name
// is refused while the tree is being built, because a flag with no name is not
// something a caller can type.
func TestABodyParameterWithAnUnnamedMemberIsRefused(t *testing.T) {
	described := api.API{
		ID:   "broken",
		Name: "broken",
		Services: []api.Service{{
			Name: "things",
			Operations: []api.Operation{{
				Name: "write",
				Parameters: []api.Parameter{{
					Name: "payload",
					In:   api.ParameterInBody,
					Schema: &api.Schema{Type: api.TypeObject, Properties: []api.Property{
						{Name: "", Schema: api.StringSchema()},
					}},
				}},
			}},
		}},
	}
	_, err := cli.Generate(described, &recorder{}, cli.Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request member has no name")
}

// A request value with a member that states no name is refused for the same reason:
// the flag it would become cannot be typed, and dropping it would silently lose an
// input the description declared.
func TestARequestMemberWithNoNameIsRefused(t *testing.T) {
	described := api.API{
		ID:   "broken",
		Name: "broken",
		Services: []api.Service{{
			Name: "things",
			Operations: []api.Operation{{
				Name: "write",
				Request: &api.Schema{Type: api.TypeObject, Properties: []api.Property{
					{Name: "  ", Schema: api.StringSchema()},
				}},
			}},
		}},
	}
	_, err := cli.Generate(described, &recorder{}, cli.Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "declares a request member with no name")
}

// A nested object whose member states no name is refused while the object is being
// flattened, and the refusal travels out of the recursion: a nameless member two
// levels down is exactly as unusable as one at the top, and a walk that swallowed
// the error would produce a tree with a flag named "prompt.".
func TestANestedMemberWithNoNameIsRefused(t *testing.T) {
	described := api.API{
		ID:   "broken",
		Name: "broken",
		Services: []api.Service{{
			Name: "things",
			Operations: []api.Operation{{
				Name: "write",
				Request: &api.Schema{Type: api.TypeObject, Properties: []api.Property{{
					Name: "prompt",
					Schema: &api.Schema{Type: api.TypeObject, Properties: []api.Property{{
						Name: "inner",
						Schema: &api.Schema{Type: api.TypeObject, Properties: []api.Property{
							{Name: "", Schema: api.StringSchema()},
						}},
					}}},
				}}},
			}},
		}},
	}
	_, err := cli.Generate(described, &recorder{}, cli.Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a request member has no name")
}

// A member whose schema states nothing at all is a real state — a description may
// name a member and say nothing about its value — and the flag it becomes is text
// with the type as its only documentation, rather than a flag with no help at all.
func TestAMemberWithNoSchemaIsAStringFlag(t *testing.T) {
	root, invoker := covTree(t)
	command := find(t, root, "widgets", "shape-widget")

	require.NotNil(t, command.Flags().Lookup("shapeless"))
	assert.Equal(t, "string", flagUsage(command, "shapeless"),
		"the type stands in for the comment the description never wrote")

	_, err := execute(t, root, "widgets", "shape-widget", "--shapeless", "x")
	require.NoError(t, err)
	body, _ := arguments(t, invoker.last(t))["body"].(map[string]any)
	assert.Equal(t, map[string]any{"shapeless": "x"}, body)
}

// A required member of an object the caller stated as a non-object is refused
// before the call: the member is not there, and the object it belongs to is not an
// object, so no spelling of a dotted flag could have set it.
func TestARequiredMemberInsideANonObjectIsRefused(t *testing.T) {
	root, invoker := covTree(t)

	_, err := execute(t, root, "widgets", "shape-widget", "--body", `{"prompt":5}`)
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "--prompt.id is required")
	assert.Empty(t, invoker.calls)
}

// A whole request value that is one JSON document refuses anything that is not an
// object, and names the shape the caller actually gave: "expected a JSON object,
// got an array" is the sentence that tells them which flag was wrong.
func TestARequestDocumentOfTheWrongShapeIsRefused(t *testing.T) {
	root, invoker := covTree(t)

	for value, want := range map[string]string{
		`[]`:     "an array",
		`null`:   "null",
		`"text"`: "a string",
		`true`:   "a boolean",
		`4`:      "a number",
	} {
		_, err := execute(t, root, "widgets", "shape-widget", "--body", value)
		require.Error(t, err, "--body %s is not an object", value)
		assert.Contains(t, err.Error(), "expected a JSON object, got "+want)
	}

	assert.Empty(t, invoker.calls)
}

// An array flag refuses an object the same way, and an empty or malformed document
// is refused before the decoder is asked to read it: "is not valid JSON" names the
// problem, where a decoder's own error would name a byte offset.
func TestAnArrayDocumentOfTheWrongShapeIsRefused(t *testing.T) {
	root, _ := covTree(t)

	_, err := execute(t, root, "widgets", "shape-widget", "--either", `{}`)
	require.NoError(t, err, "a JSON value flag accepts an object")

	described := api.API{
		ID:   "docs",
		Name: "docs",
		Services: []api.Service{{
			Name: "things",
			Operations: []api.Operation{{
				Name: "write",
				Request: api.ObjectSchema(
					api.Property{Name: "rows", Schema: &api.Schema{
						Type:  api.TypeArray,
						Items: api.ObjectSchema(api.Property{Name: "id", Schema: api.StringSchema()}),
					}},
				),
			}},
		}},
	}
	root = generate(t, described, &recorder{}, cli.Options{})
	for value, want := range map[string]string{
		`{}`:  "expected a JSON array, got an object",
		``:    "expected a JSON value, got an empty flag",
		`[{`:  "is not valid JSON",
		`[{]`: "is not valid JSON",
	} {
		_, err := execute(t, root, "things", "write", "--rows", value)
		require.Error(t, err, "--rows %q must be refused", value)
		assert.Contains(t, err.Error(), want)
	}
}

// A parameter the description does not document still says what it carries, and a
// boolean parameter is a boolean flag: registering it as a string would make
// --flag=true a value the server reads as the word "true".
func TestAScalarParameterKeepsItsType(t *testing.T) {
	root, invoker := covTree(t)
	command := find(t, root, "widgets", "count-widgets")

	assert.Equal(t, "bool", command.Flags().Lookup("flag").Value.Type())
	assert.Equal(t, "float64", command.Flags().Lookup("ratio").Value.Type())

	_, err := execute(t, root, "widgets", "count-widgets",
		"--flag", "--ratio", "1.5", "--old", "x", "--absent", `{"a":1}`)
	require.NoError(t, err)
	arguments := arguments(t, invoker.last(t))
	assert.Equal(t, true, arguments["flag"])
	assert.Equal(t, 1.5, arguments["ratio"])
	assert.Equal(t, "x", arguments["old"])
	assert.Equal(t, map[string]any{"a": float64(1)}, arguments["absent"],
		"a parameter of no stated type travels as JSON")
}
