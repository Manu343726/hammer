package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// build is a tree over the petstore description whose invoker records what it was
// asked. A fresh tree is built per test because a pflag remembers the flags it was
// given, so two runs of one command are only independent if they are two trees.
func build(t *testing.T) (*cobra.Command, *recorder) {
	t.Helper()
	invoker := &recorder{result: api.Result{Body: json.RawMessage(`{"ok":true}`)}}
	return generate(t, petstore(), invoker, cli.Options{CommandName: "petstore"}), invoker
}

// Every operation of every service is a command. A description that gains an
// operation without gaining a command is an operation a terminal cannot reach, and
// the only way that is caught is by counting commands against operations.
func TestEveryOperationBecomesACommand(t *testing.T) {
	root, _ := build(t)

	for _, path := range [][]string{
		{"pets", "get-pet"},
		{"pets", "create-pet"},
		{"pets", "list-pets"},
		{"pets", "watch-pets"},
		{"users", "get-user"},
	} {
		assert.NotNil(t, find(t, root, path...), "%v is not reachable", path)
	}
}

// A command is named from the service and operation names it came from, in the
// spelling a caller types.
func TestCommandnamesComeFromTheDescription(t *testing.T) {
	root, _ := build(t)

	assert.Contains(t, commandNames(root), "pets")
	assert.Contains(t, commandNames(root), "users")
	assert.Contains(t, commandNames(find(t, root, "pets")), "get-pet")
	assert.Contains(t, commandNames(find(t, root, "pets")), "create-pet")
}

// Running a command assembles one call from the description and the flags, and
// hands it to the invoker. The call is what every later assertion is about, so this
// is the test the rest of them assume.
func TestRunningAnOperationInvokesTheOperation(t *testing.T) {
	call, _, err := run(t, "pets", "get-pet", "--petId", "pet-7")
	require.NoError(t, err)

	assert.Equal(t, "petstore/pets/getPet", call.Operation.ID)
	assert.Equal(t, "petstore", call.API.ID)
	assert.Equal(t, "https://pets.example.com", call.Server.BaseURL)
	assert.Equal(t, map[string]any{"petId": "pet-7"}, arguments(t, call))
}

// A flag the caller did not set sends nothing. A default of "" or 0 that reached
// the call would overwrite a value the server derives, and a command line cannot
// tell "unset" from "empty" once it has filled one in.
func TestAFlagThatWasNotGivenSendsNothing(t *testing.T) {
	call, _, err := run(t, "pets", "create-pet", "--name", "Rex")
	require.NoError(t, err)

	body, ok := arguments(t, call)["body"].(map[string]any)
	require.True(t, ok, "the request value is carried under its own key: %s", call.Arguments)
	assert.Equal(t, map[string]any{"name": "Rex"}, body)
}

// The request value's members are flags, and each one lands where its schema says:
// a scalar as a scalar, a list as a list, a nested object as an object.
func TestTheBodyIsComposedFromItsMembers(t *testing.T) {
	call, _, err := run(t, "pets", "create-pet",
		"--name", "Rex",
		"--tag", "cat", "--tag", "dog",
		"--sizes", "1", "--sizes", "2",
		"--count", "3",
		"--status", "available",
		"--prompt.id", "p1",
		"--prompt.template", "Hello",
		"--friends", `[{"id":"f1"},{"id":"f2"}]`,
	)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"body": {
			"name": "Rex",
			"tag": ["cat", "dog"],
			"sizes": [1, 2],
			"count": 3,
			"status": "available",
			"prompt": {"id": "p1", "template": "Hello"},
			"friends": [{"id": "f1"}, {"id": "f2"}]
		}
	}`, string(call.Arguments))
}

// A whole request value can be given as one document, because a caller who already
// has the document should not have to spell out every member.
func TestTheWholeBodyCanBeOneDocument(t *testing.T) {
	call, _, err := run(t, "pets", "create-pet", "--body", `{"name":"Rex","tag":["cat"]}`)
	require.NoError(t, err)
	assert.JSONEq(t, `{"body":{"name":"Rex","tag":["cat"]}}`, string(call.Arguments))
}

// A member flag wins over the document for the member it names, and the rest of the
// document survives. That is the precedence the flag help promises, and it is why
// the document is applied first.
func TestAMemberFlagWinsOverTheDocumentItBelongsTo(t *testing.T) {
	call, _, err := run(t, "pets", "create-pet",
		"--body", `{"name":"Rex","count":9}`,
		"--name", "Fido",
	)
	require.NoError(t, err)
	assert.JSONEq(t, `{"body":{"name":"Fido","count":9}}`, string(call.Arguments))
}

// The same precedence holds one level down: a document supplies a nested object and
// a dotted flag overrides one member of it.
func TestADottedFlagWinsOverTheJSONFormOfItsObject(t *testing.T) {
	call, _, err := run(t, "pets", "create-pet",
		"--name", "Rex",
		"--prompt", `{"id":"from-json","template":"kept"}`,
		"--prompt.id", "from-flag",
	)
	require.NoError(t, err)
	assert.JSONEq(t, `{"body":{"name":"Rex","prompt":{"id":"from-flag","template":"kept"}}}`, string(call.Arguments))
}

// A number inside a document is sent as the number the caller wrote, not as the
// float a decoder might have rounded it to. An identifier written as a number is
// the case where that matters and the one a JSON round trip through `any` breaks.
func TestANumberInADocumentKeepsItsOwnSpelling(t *testing.T) {
	call, _, err := run(t, "pets", "create-pet", "--body", `{"name":"Rex","big":123456789012345678}`)
	require.NoError(t, err)
	assert.Contains(t, string(call.Arguments), "123456789012345678")
}

// A required input the caller omitted is refused before the call is made: the
// server's refusal would name a wire member, and the caller typed a flag.
func TestAMissingRequiredParameterIsRefused(t *testing.T) {
	root, invoker := build(t)

	_, err := execute(t, root, "pets", "get-pet")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid), "a missing input is the caller's mistake")
	assert.Contains(t, err.Error(), "--petId")
	assert.Empty(t, invoker.calls, "a request the description says cannot succeed is not sent")
}

func TestAMissingRequiredMemberIsRefused(t *testing.T) {
	root, invoker := build(t)

	_, err := execute(t, root, "pets", "create-pet", "--count", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--name")
	assert.Empty(t, invoker.calls)
}

// A member of a nested object is required only when the object it belongs to was
// supplied. The description says the object is optional, so demanding its members
// on their own would refuse a call the description allows.
func TestAMemberOfAnAbsentObjectIsNotRequired(t *testing.T) {
	call, _, err := run(t, "pets", "create-pet", "--name", "Rex")
	require.NoError(t, err)

	body, ok := arguments(t, call)["body"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, body, "prompt")
}

func TestAMemberOfAPresentObjectIsRequired(t *testing.T) {
	root, invoker := build(t)

	_, err := execute(t, root, "pets", "create-pet", "--name", "Rex", "--prompt.template", "Hi")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--prompt.id")
	assert.Empty(t, invoker.calls)
}

// An enumerated value is refused locally, with the values the description allows in
// the same breath, rather than after a round trip in the transport's own words.
func TestAValueOutsideTheEnumIsRefused(t *testing.T) {
	root, invoker := build(t)

	_, err := execute(t, root, "pets", "create-pet", "--name", "Rex", "--status", "lost")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"lost" is not one of available, sold`)
	assert.Empty(t, invoker.calls)
}

// The response body is printed as indented JSON, so a person can read it and a pipe
// can still parse it.
func TestTheResponseIsIndentedJSON(t *testing.T) {
	root, invoker := build(t)
	invoker.result = api.Result{Body: json.RawMessage(`{"id":"7","name":"Rex"}`)}

	out, err := execute(t, root, "pets", "get-pet", "--petId", "7")
	require.NoError(t, err)

	assert.JSONEq(t, `{"id":"7","name":"Rex"}`, out)
	assert.Contains(t, out, "\n  \"id\":", "the body is indented, because a person reads it too")
}

// A body that is not JSON is written as it arrived. A transport that serves text is
// still a transport this adapter describes, and mangling it would be worse than
// passing it through.
func TestABodyThatIsNotJSONIsPassedThrough(t *testing.T) {
	root, invoker := build(t)
	invoker.result = api.Result{Body: json.RawMessage(`pet-7 is fine`)}

	out, err := execute(t, root, "pets", "get-pet", "--petId", "7")
	require.NoError(t, err)
	assert.Equal(t, "pet-7 is fine\n", out)
}

// A failure from the invoker reaches the caller as it arrived, with the operation
// named and its classification intact. Silencing either half would leave a caller
// unable to tell a rejected request from a broken tool.
func TestAFailureKeepsItsClassification(t *testing.T) {
	root, invoker := build(t)
	invoker.err = api.Errorf(api.KindNotFound, "no pet %q", "7")

	_, err := execute(t, root, "pets", "get-pet", "--petId", "7")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindNotFound))
	assert.Contains(t, err.Error(), `call "petstore/pets/getPet"`)
}

// Execute is the sibling that turns a classified failure into the exit code a shell
// reads, so a script can tell "fix your arguments" from "try again later".
func TestExecuteReportsTheOutcomeAsAnExitCode(t *testing.T) {
	invoker := &recorder{err: api.Errorf(api.KindUnavailable, "the store is closed")}
	root := generate(t, petstore(), invoker, cli.Options{CommandName: "petstore"})
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)

	root.SetArgs([]string{"pets", "get-pet", "--petId", "7"})
	assert.Equal(t, cli.ExitFailure, cli.Execute(root))
	assert.Contains(t, out.String(), "unavailable")
	assert.Contains(t, out.String(), "the store is closed")

	refused := build2(t, []string{"pets", "get-pet"})
	assert.Equal(t, cli.ExitInvalid, cli.Execute(refused), "a missing input is a usage failure")

	accepted := build2(t, []string{"pets", "list-pets"})
	assert.Equal(t, cli.ExitOK, cli.Execute(accepted))
}

// build2 is a tree with the given arguments already set, for the Execute tests.
func build2(t *testing.T, args []string) *cobra.Command {
	t.Helper()
	invoker := &recorder{result: api.Result{Body: json.RawMessage(`{"ok":true}`)}}
	root := generate(t, petstore(), invoker, cli.Options{CommandName: "petstore"})
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(args)
	return root
}

// A streaming operation is a command and takes its flags, so its help documents
// what it takes; calling it says which operation cannot be invoked instead of
// pretending it worked.
func TestAStreamingOperationIsRefusedByName(t *testing.T) {
	root, invoker := build(t)
	command := find(t, root, "pets", "watch-pets")

	assert.Contains(t, command.Short, "(streaming)")
	assert.Contains(t, command.Long, "streams responses")

	_, err := execute(t, root, "pets", "watch-pets")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindUnsupported))
	assert.Contains(t, err.Error(), "petstore/pets/watchPets")
	assert.Empty(t, invoker.calls)
}

// A service whose name is the root command's own gets no level of its own: the root
// is already that name, and a level would cost a word on every invocation to say
// nothing. Another service keeps its level, so the rule is not blanket.
func TestAServiceNamedAfterTheCommandAddsNoLevel(t *testing.T) {
	invoker := &recorder{}
	root := generate(t, petstore(), invoker, cli.Options{CommandName: "pets"})

	assert.Nil(t, findChild(root, "pets"))
	assert.NotNil(t, findChild(root, "get-pet"), "the operations are on the root")
	assert.NotNil(t, findChild(root, "users"), "and a service with another name keeps its level")
}

// A command's help names the operation it was generated from. That is how a reader
// who found a command by guessing finds the contract that defines it.
func TestTheHelpNamesTheOperation(t *testing.T) {
	root, _ := build(t)
	command := find(t, root, "pets", "create-pet")

	assert.Contains(t, command.Long, "petstore/pets/createPet")
	assert.Contains(t, command.Long, "POST /pets")
	assert.Contains(t, command.Long, "Creates a pet in the store.")
	assert.Contains(t, command.Long, "Declared side effects: create.")
}

// A summary is a sentence, not the first line of the source comment. A sentence the
// author wrapped became a summary ending mid-clause, which reads as if the
// description ran out of words.
func TestASummaryIsAWholeSentence(t *testing.T) {
	root, _ := build(t)
	command := find(t, root, "pets", "get-pet")

	assert.Equal(t, "Reads one pet.", command.Short)
	assert.Contains(t, command.Long, "It is addressed by its identifier, so a caller needs nothing else.")
	assert.NotContains(t, command.Long, "so a caller\nneeds", "the source's line break is the author's, not the thought's")
}

// A flag's help is the description's own comment for that value, plus the facts a
// caller needs to type it. It is one line: a flag's help sits in one column, and a
// paragraph break in the middle of it reads as three separate flags.
func TestAFlagHelpIsTheDescriptionsOwnComment(t *testing.T) {
	root, _ := build(t)
	command := find(t, root, "pets", "get-pet")

	usage := flagUsage(command, "petId")
	assert.Contains(t, usage, "Identifier of the pet to read.")
	assert.Contains(t, usage, "required")
	assert.Contains(t, usage, "path")
	assert.NotContains(t, usage, "\n")
}

// The values an enumerated member allows are in its help, because a caller who has
// to read the document to learn them is doing the generator's job by hand.
func TestAnEnumeratedFlagListsItsValues(t *testing.T) {
	root, _ := build(t)
	command := find(t, root, "pets", "create-pet")

	assert.Contains(t, flagUsage(command, "status"), "one of: available, sold")
	assert.Contains(t, flagUsage(command, "tag"), "repeatable")
	assert.Contains(t, flagUsage(command, "prompt"), "JSON object")
}

// A member the description says is returned but not accepted gets no flag: offering
// one would promise an input the operation ignores.
func TestAReadOnlyMemberGetsNoFlag(t *testing.T) {
	root, _ := build(t)
	command := find(t, root, "pets", "create-pet")

	assert.Nil(t, command.Flags().Lookup("createdAt"))
}

// A flag the description does not declare is refused, naming it. A typo that
// silently did nothing is how a caller concludes the tool cannot do what they
// asked.
func TestAnUnknownFlagIsRefused(t *testing.T) {
	root, _ := build(t)

	_, err := execute(t, root, "pets", "get-pet", "--nope", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
}

// A description that declares no location is still callable, but a command has to
// be told where to call: guessing would send a request somewhere nobody named.
func TestADescriptionWithNoServerNeedsOne(t *testing.T) {
	described := petstore()
	described.DeclaredServers = nil
	invoker := &recorder{result: api.Result{Body: json.RawMessage(`{}`)}}
	root := generate(t, described, invoker, cli.Options{CommandName: "petstore"})

	_, err := execute(t, root, "pets", "list-pets")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "--server")
	assert.Empty(t, invoker.calls)

	root = generate(t, described, invoker, cli.Options{CommandName: "petstore"})
	_, err = execute(t, root, "pets", "list-pets", "--server", "http://localhost:9999")
	require.NoError(t, err)
	require.Len(t, invoker.calls, 1)
	assert.Equal(t, "http://localhost:9999", invoker.calls[0].Server.BaseURL)
}

// A tree without an invoker would be a set of commands that cannot call anything, so
// it is refused rather than built.
func TestGenerateRefusesToBuildWithoutAnInvoker(t *testing.T) {
	_, err := cli.Generate(petstore(), nil, cli.Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invoker")
}

// A description with no identifiers cannot be named: an operation with no identifier
// would print an empty help line and a bare command would be called "api". The
// refusal is the caller's to fix.
func TestGenerateRefusesADescriptionWithoutIdentifiers(t *testing.T) {
	described := petstore()
	described.ID = ""
	_, err := cli.Generate(described, &recorder{}, cli.Options{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "api id")
}

// Generating a tree does not rewrite the caller's description: the adapter annotates
// a copy, because a caller that keeps the description it parsed must not find its
// identifiers changed under it.
func TestGenerateDoesNotMutateTheDescription(t *testing.T) {
	described := petstore()
	_, err := cli.Generate(described, &recorder{}, cli.Options{})
	require.NoError(t, err)
	assert.Empty(t, described.Services[0].Operations[0].ID)
}

// The root describes where the calls go and where the description came from, which
// is what a reader who ran the bare command wants before choosing a child.
func TestTheRootHelpStatesTheServers(t *testing.T) {
	root, _ := build(t)

	assert.Contains(t, root.Long, "https://pets.example.com")
	assert.Contains(t, root.Long, "The live store.")
	assert.True(t, strings.HasPrefix(root.Short, "A small pet store."), "the root's one line is a sentence: %q", root.Short)
}
