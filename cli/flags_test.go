package cli_test

import (
	"strings"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A flag's name is the description's own word for the value, and where two inputs
// claim one word neither keeps it bare. These tests are about that mapping, because
// a flag that silently carries the wrong value is invisible until a caller reads the
// result and wonders.

// things is a description with one service, for the tests that need an operation
// the petstore fixture does not have.
func things(operations ...api.Operation) api.API {
	return api.API{
		ID:              "test",
		Name:            "test",
		Format:          "test",
		DeclaredServers: []api.DeclaredServer{{URL: "https://things.example.com"}},
		Services:        []api.Service{{Name: "things", Operations: operations}},
	}
}

// buildThings builds a tree over a description with one service.
func buildThings(t *testing.T, described api.API) (*cobra.Command, *recorder) {
	t.Helper()
	invoker := &recorder{}
	return generate(t, described, invoker, cli.Options{CommandName: "test"}), invoker
}

// A query parameter and a request member are allowed to share a name, and real
// descriptions do it constantly. Neither keeps the bare name: the parameter gains
// the location it travels in and the member gains the body it belongs to, so which
// one is short does not depend on the order the description listed them in.
func TestTwoInputsWithOneNameAreBothQualified(t *testing.T) {
	described := things(api.Operation{
		Name:   "putThing",
		Method: "PUT",
		Path:   "/things",
		Parameters: []api.Parameter{{
			Name:     "id",
			In:       api.ParameterInQuery,
			Required: true,
			Schema:   api.StringSchema(),
		}},
		Request: api.ObjectSchema(
			api.Property{Name: "id", Required: true, Schema: api.StringSchema()},
		),
	})
	root, invoker := buildThings(t, described)
	command := find(t, root, "things", "put-thing")

	require.NotNil(t, command.Flags().Lookup("query.id"), "the parameter keeps its own name, qualified by where it travels")
	require.NotNil(t, command.Flags().Lookup("body.id"), "and the member by the body it belongs to")
	assert.Nil(t, command.Flags().Lookup("id"), "neither keeps the bare name")

	_, err := execute(t, root, "things", "put-thing", "--query.id", "query-value", "--body.id", "body-value")
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"query-value","body":{"id":"body-value"}}`, string(invoker.last(t).Arguments))
}

// A name the command itself owns is not handed to a description's input: taking
// --help would cost the caller -h, and taking --server would shadow where calls go.
func TestAReservedNameIsQualified(t *testing.T) {
	described := things(api.Operation{
		Name:   "findThing",
		Method: "GET",
		Parameters: []api.Parameter{
			{Name: "help", In: api.ParameterInQuery, Schema: api.StringSchema()},
			{Name: "server", In: api.ParameterInQuery, Schema: api.StringSchema()},
		},
	})
	root, _ := buildThings(t, described)
	command := find(t, root, "things", "find-thing")

	assert.NotNil(t, command.Flags().Lookup("query.help"))
	assert.NotNil(t, command.Flags().Lookup("query.server"))
	command.InitDefaultHelpFlag()
	assert.NotNil(t, command.Flags().Lookup("help"), "cobra's own help flag is still there")
}

// An array of scalars is given once per element. It is deliberately not comma
// separated: an element is allowed to contain a comma, and a flag that silently
// split one would be lying about what it carried.
func TestAnArrayOfScalarsTakesOneValuePerOccurrence(t *testing.T) {
	described := things(api.Operation{
		Name:   "tagThing",
		Method: "POST",
		Request: api.ObjectSchema(
			api.Property{Name: "tags", Schema: api.ArraySchema(api.StringSchema())},
			api.Property{Name: "sizes", Schema: api.ArraySchema(&api.Schema{Type: api.TypeInteger})},
		),
	})
	root, invoker := buildThings(t, described)

	_, err := execute(t, root, "things", "tag-thing", "--tags", "a,b", "--tags", "c", "--sizes", "1", "--sizes", "2")
	require.NoError(t, err)
	assert.JSONEq(t, `{"body":{"tags":["a,b","c"],"sizes":[1,2]}}`, string(invoker.last(t).Arguments))
}

// An element that is not the type the description states is refused, naming the
// flag and the value, rather than arriving as a string the server has to interpret.
func TestAnArrayElementOfTheWrongTypeIsRefused(t *testing.T) {
	described := things(api.Operation{
		Name:   "tagThing",
		Method: "POST",
		Request: api.ObjectSchema(
			api.Property{Name: "sizes", Schema: api.ArraySchema(&api.Schema{Type: api.TypeInteger})},
		),
	})
	root, invoker := buildThings(t, described)

	_, err := execute(t, root, "things", "tag-thing", "--sizes", "large")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "--sizes")
	assert.Contains(t, err.Error(), "large")
	assert.Empty(t, invoker.calls)
}

// An array of objects is one JSON array, and its members are not flattened: no flag
// addresses --friends.0.id, so a dotted flag there would be a lie about which
// element it sets.
func TestAnArrayOfObjectsIsOneJSONValue(t *testing.T) {
	described := things(api.Operation{
		Name:   "addThings",
		Method: "POST",
		Request: api.ObjectSchema(
			api.Property{Name: "friends", Schema: api.ArraySchema(
				api.ObjectSchema(api.Property{Name: "id", Schema: api.StringSchema()}),
			)},
		),
	})
	root, invoker := buildThings(t, described)
	command := find(t, root, "things", "add-things")

	require.NotNil(t, command.Flags().Lookup("friends"))
	assert.Nil(t, command.Flags().Lookup("friends.id"), "an element of a list has no flag of its own")
	assert.Contains(t, flagUsage(command, "friends"), "JSON array")

	_, err := execute(t, root, "things", "add-things", "--friends", `{"id":"f1"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected a JSON array")
	assert.Empty(t, invoker.calls)

	root, invoker = buildThings(t, described)
	_, err = execute(t, root, "things", "add-things", "--friends", `[{"id":"f1"}]`)
	require.NoError(t, err)
	assert.JSONEq(t, `{"body":{"friends":[{"id":"f1"}]}}`, string(invoker.last(t).Arguments))
}

// A request value with no members to flag — a list, a scalar, an object of
// undeclared shape — is one flag, called after the key the model reserves for the
// request value.
func TestARequestValueWithNoMembersIsOneFlag(t *testing.T) {
	described := things(api.Operation{
		Name:    "replaceThings",
		Method:  "PUT",
		Request: api.ArraySchema(api.StringSchema()),
	})
	root, invoker := buildThings(t, described)
	command := find(t, root, "things", "replace-things")

	require.NotNil(t, command.Flags().Lookup("body"))
	assert.Contains(t, flagUsage(command, "body"), "repeatable")

	_, err := execute(t, root, "things", "replace-things", "--body", "one", "--body", "two")
	require.NoError(t, err)
	assert.JSONEq(t, `{"body":["one","two"]}`, string(invoker.last(t).Arguments))
}

// A parameter the description carries in the body is a member of the request value,
// not an argument of its own: the transport has one body, and the parameter is part
// of it.
func TestAParameterCarriedInTheBodyIsARequestBodyMember(t *testing.T) {
	described := things(api.Operation{
		Name:   "removeThing",
		Method: "DELETE",
		Parameters: []api.Parameter{{
			Name:     "reason",
			In:       api.ParameterInBody,
			Required: true,
			Schema:   api.StringSchema(),
		}},
	})
	root, invoker := buildThings(t, described)

	_, err := execute(t, root, "things", "remove-thing", "--reason", "duplicate")
	require.NoError(t, err)
	assert.JSONEq(t, `{"body":{"reason":"duplicate"}}`, string(invoker.last(t).Arguments))
}

// A header and a cookie parameter are flags of their own, and the argument they
// supply is the one the description names — the location is the invoker's to apply,
// not the command line's to encode into a key.
func TestAParameterInAHeaderIsAnArgumentOfItsOwn(t *testing.T) {
	described := things(api.Operation{
		Name:   "findThing",
		Method: "GET",
		Parameters: []api.Parameter{
			{Name: "X-Tenant", In: api.ParameterInHeader, Required: true, Schema: api.StringSchema()},
			{Name: "session", In: api.ParameterInCookie, Schema: api.StringSchema()},
		},
	})
	root, invoker := buildThings(t, described)
	command := find(t, root, "things", "find-thing")

	assert.Contains(t, flagUsage(command, "X-Tenant"), "header")
	assert.Contains(t, flagUsage(command, "session"), "cookie")

	_, err := execute(t, root, "things", "find-thing", "--X-Tenant", "acme", "--session", "s1")
	require.NoError(t, err)
	assert.JSONEq(t, `{"X-Tenant":"acme","session":"s1"}`, string(invoker.last(t).Arguments))
}

// A parameter the description does not name cannot become a flag, and a description
// that declares one is refused rather than silently dropping an input the operation
// needs.
func TestAParameterWithNoNameIsRefused(t *testing.T) {
	described := things(api.Operation{
		Name:       "findThing",
		Method:     "GET",
		Parameters: []api.Parameter{{In: api.ParameterInQuery, Schema: api.StringSchema()}},
	})
	_, err := cli.Generate(described, &recorder{}, cli.Options{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "no name")
}

// A deeply nested object is flattened only so far. The bound exists because a schema
// that contains itself would otherwise never stop, and a caller who needs a deeper
// value still has the JSON form of the object it lives in.
func TestANestedObjectStopsBeingFlattenedAtABound(t *testing.T) {
	deep := api.ObjectSchema(api.Property{Name: "value", Schema: api.StringSchema()})
	for range 8 {
		deep = api.ObjectSchema(api.Property{Name: "inner", Schema: deep})
	}
	described := things(api.Operation{
		Name:    "deepThing",
		Method:  "POST",
		Request: deep,
	})
	root, _ := buildThings(t, described)
	command := find(t, root, "things", "deep-thing")

	assert.NotNil(t, command.Flags().Lookup("inner.inner"), "a nested object is flattened more than one level")
	assert.NotNil(t, command.Flags().Lookup("inner"), "and its JSON form stays addressable")

	// The walk is bounded rather than exhaustive: the deepest flag is a few
	// segments long, not eight.
	segments := 0
	command.Flags().VisitAll(func(flag *pflag.Flag) {
		if count := len(strings.Split(flag.Name, ".")); count > segments {
			segments = count
		}
	})
	assert.LessOrEqual(t, segments, 5, "the walk stops at a bound instead of following the schema")
}

// A request value that is a list of documents travels as the value itself. It is
// not a member of an object the adapter invents around it, and it is not a scalar
// either, so the whole value is the flag.
func TestARequestValueThatIsAListOfDocuments(t *testing.T) {
	described := things(api.Operation{
		Name:   "replaceThings",
		Method: "PUT",
		Request: api.ArraySchema(
			api.ObjectSchema(api.Property{Name: "id", Schema: api.StringSchema()}),
		),
	})
	root, invoker := buildThings(t, described)
	command := find(t, root, "things", "replace-things")

	assert.Contains(t, flagUsage(command, "body"), "JSON array")

	_, err := execute(t, root, "things", "replace-things", "--body", `[{"id":"one"}]`)
	require.NoError(t, err)
	assert.JSONEq(t, `{"body":[{"id":"one"}]}`, string(invoker.last(t).Arguments))
}

// An empty object the caller gave explicitly is still a request value: "no body" and
// "an empty body" are different requests, and only the caller knows which they meant.
func TestAnEmptyBodyDocumentIsStillSent(t *testing.T) {
	described := things(api.Operation{
		Name:   "touchThing",
		Method: "POST",
		Request: api.ObjectSchema(
			api.Property{Name: "note", Schema: api.StringSchema()},
		),
	})
	root, invoker := buildThings(t, described)

	_, err := execute(t, root, "things", "touch-thing", "--body", `{}`)
	require.NoError(t, err)
	assert.JSONEq(t, `{"body":{}}`, string(invoker.last(t).Arguments))

	root, invoker = buildThings(t, described)
	_, err = execute(t, root, "things", "touch-thing")
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(invoker.last(t).Arguments))
}
