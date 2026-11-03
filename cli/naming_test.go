package cli_test

import (
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A command name has to identify one command, and a name two services claim is
// reached by whichever cobra added first — the other is not an error, it is just
// gone. So the qualification ladder is what keeps a contract that declares two
// versions of one service reachable at all.

// services builds a description whose services are named as given, each with one
// operation, which is all naming looks at.
func services(names ...string) api.API {
	described := api.API{ID: "contract", Name: "contract", Format: "test"}
	for _, name := range names {
		described.Services = append(described.Services, api.Service{
			Name:       name,
			Operations: []api.Operation{{Name: "Do", Method: "POST"}},
		})
	}
	return described
}

// A name one service claims is not lengthened: a caller who learned it must keep
// being able to type it.
func TestAnUnambiguousNameIsNotLengthened(t *testing.T) {
	root := generate(t, services("pets", "users"), &recorder{}, cli.Options{CommandName: "shop"})

	assert.Contains(t, commandNames(root), "pets")
	assert.Contains(t, commandNames(root), "users")
}

// Two services whose last segments match are told apart by the package segment that
// differs, which is the part that says which version is which.
func TestTwoVersionsOfOneServiceAreQualified(t *testing.T) {
	root := generate(t, services("v1.Pets", "v2.Pets"), &recorder{}, cli.Options{CommandName: "shop"})

	names := commandNames(root)
	assert.Contains(t, names, "v1.pets")
	assert.Contains(t, names, "v2.pets")
	assert.NotContains(t, names, "pets", "and neither keeps a name the other also claims")

	// Both are reachable, which is the whole point: before this, cobra resolved the
	// bare name to one of them and the other existed only in the tree.
	for _, name := range []string{"v1.pets", "v2.pets"} {
		assert.NotNil(t, findChild(find(t, root, name), "do"), "%s holds its operations", name)
	}
}

// A document's services have no package segment, so the API's own name is what tells
// two of them apart.
func TestTwoServicesWithTheSameWordAreQualifiedByTheAPI(t *testing.T) {
	root := generate(t, services("Pets", "pets"), &recorder{}, cli.Options{CommandName: "shop"})

	names := commandNames(root)
	assert.Len(t, names, 2, "both are commands, not one found twice")
	assert.NotContains(t, names, "pets", "the bare name is claimed by both, so neither keeps it")
	assert.NotContains(t, names, "contract.pets", "the qualified name is claimed by both too")
	// A case-only difference defeats every rung but the last, and the numbered
	// suffix keeps the second reachable rather than dropping it.
	for _, name := range names {
		assert.NotNil(t, findChild(find(t, root, name), "do"))
	}
}

// The names a description gets do not depend on the order it listed its services in.
// A caller who typed a name yesterday must be able to type it today.
func TestNamesDoNotDependOnDescriptionOrder(t *testing.T) {
	forward := generate(t, services("v1.Pets", "v2.Pets", "pets"), &recorder{}, cli.Options{CommandName: "shop"})
	backward := generate(t, services("pets", "v2.Pets", "v1.Pets"), &recorder{}, cli.Options{CommandName: "shop"})

	assert.ElementsMatch(t, commandNames(forward), commandNames(backward),
		"the same services get the same commands whatever order they arrive in")
}

// A service with a single-segment name and nothing to disambiguate against keeps a
// usable name rather than producing an empty or dotted one.
func TestANameWithNoPackageIsNotMangled(t *testing.T) {
	root := generate(t, services("Pets"), &recorder{}, cli.Options{CommandName: "shop"})

	for _, name := range commandNames(root) {
		assert.NotContains(t, name, "..", "no empty segment is produced")
		assert.NotEmpty(t, name)
	}
}

// A service whose name repeats the command's own gets no level of its own, because a
// level that only repeats the word above it costs the caller a word per invocation.
func TestAServiceNamedAfterItsCommandIsFlattened(t *testing.T) {
	root := generate(t, services("Pets"), &recorder{}, cli.Options{CommandName: "pets"})

	assert.Nil(t, findChild(root, "pets"), "the service level is not a second name")
	assert.NotNil(t, findChild(root, "do"), "the operations are on the command")
}

// A service that has an operation of its own name adds no level either: cobra would
// resolve the bare word to the parent, and the operation would disappear from help
// while still working if typed in full.
func TestAServiceNamedAfterItsOperationIsFlattened(t *testing.T) {
	described := api.API{
		ID: "test", Name: "test", Format: "test",
		Services: []api.Service{{
			Name:       "echo",
			Operations: []api.Operation{{Name: "echo", Method: "POST"}},
		}},
	}
	root := generate(t, described, &recorder{}, cli.Options{CommandName: "shop"})

	children := root.Commands()
	require.Len(t, children, 1, "the operation is on the root, without a service level above it")
	assert.Equal(t, "echo", children[0].Name())
	assert.False(t, children[0].HasSubCommands(), "what remains is the operation, not a namespace")
}

// Two services that are both flattened name their operations in one namespace, so an
// operation of one must not take a name the other's command has.
func TestFlattenedServicesShareOneNamespace(t *testing.T) {
	described := api.API{
		ID: "test", Name: "test", Format: "test",
		Services: []api.Service{
			{Name: "echo", Operations: []api.Operation{{Name: "echo", Method: "POST"}}},
			{Name: "reply", Operations: []api.Operation{
				{Name: "echo", Method: "POST"},
				{Name: "reply", Method: "POST"},
			}},
		},
	}
	root := generate(t, described, &recorder{}, cli.Options{CommandName: "shop"})

	// "echo" is flattened because it names its own operation; "reply" for the same
	// reason. Both contribute an operation called "do"-like "echo", so one of them
	// has to give way, and every command stays reachable.
	assert.Len(t, commandNames(root), 3, "three operations in one namespace, none dropped")
	for _, name := range commandNames(root) {
		assert.NotNil(t, findChild(root, name))
	}
}

// An API with no services still produces a tree a caller can ask for help from,
// rather than a failure the caller cannot act on.
func TestAnAPIWithNoServicesIsStillACommand(t *testing.T) {
	described := api.API{ID: "empty", Name: "empty", Format: "test"}
	root, err := cli.Generate(described, &recorder{}, cli.Options{})
	require.NoError(t, err)
	assert.Equal(t, "empty", root.Name())
	assert.Empty(t, commandNames(root))
}
