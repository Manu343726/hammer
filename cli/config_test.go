package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A value a caller states once should not have to be typed on every invocation,
// and the sources it can be stated in have one conventional order: what was typed
// wins over what was exported, which wins over what was written down, which wins
// over what the description declares. Each test below pins one step of that order,
// because a precedence that is only asserted in aggregate is a precedence nobody
// can tell from whichever source happened to answer first.

// writeConfig writes a configuration file and returns its path.
func writeConfig(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// Without either option the tree is the one this package has always built: no
// --config flag, and no hook that reads anything.
func TestTheConfigurationLayerIsOptIn(t *testing.T) {
	root := generate(t, petstore(), &recorder{}, cli.Options{CommandName: "petstore"})
	assert.Nil(t, root.PersistentFlags().Lookup("config"))

	enabled := generate(t, petstore(), &recorder{}, cli.Options{CommandName: "petstore", EnvPrefix: "PETSTORE"})
	assert.NotNil(t, enabled.PersistentFlags().Lookup("config"),
		"a caller who reads the environment may also want to name a file")
}

// A value in the file is the value the call carries, including one the description
// says is required: a required input that the file supplies is not missing.
func TestAValueIsReadFromTheConfigurationFile(t *testing.T) {
	path := writeConfig(t, "petstore.yaml", "petId: pet-7\n")
	options := cli.Options{CommandName: "petstore", ConfigFile: path}
	invoker := &recorder{result: api.Result{Body: []byte(`{"ok":true}`)}}
	root := generate(t, petstore(), invoker, options)

	_, err := execute(t, root, "pets", "get-pet")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"petId": "pet-7"}, arguments(t, invoker.last(t)))
}

// A nested member is stated as a nested document in the file and as a dotted flag
// on the command line, and both address the same value.
func TestANestedMemberIsReadFromTheConfigurationFile(t *testing.T) {
	path := writeConfig(t, "petstore.yaml", `
name: Rex
count: 5
prompt:
  id: intro
  template: Hello
`)
	invoker := &recorder{result: api.Result{Body: []byte(`{}`)}}
	root := generate(t, petstore(), invoker, cli.Options{CommandName: "petstore", ConfigFile: path})

	_, err := execute(t, root, "pets", "create-pet")
	require.NoError(t, err)
	assert.JSONEq(t, `{"body":{"name":"Rex","count":5,"prompt":{"id":"intro","template":"Hello"}}}`,
		string(invoker.last(t).Arguments))
}

// A value in the environment wins over a value in the file.
func TestTheEnvironmentWinsOverTheFile(t *testing.T) {
	path := writeConfig(t, "petstore.yaml", "name: FromFile\ncount: 5\n")
	t.Setenv("PETSTORE_NAME", "FromEnvironment")

	invoker := &recorder{result: api.Result{Body: []byte(`{}`)}}
	root := generate(t, petstore(), invoker, cli.Options{
		CommandName: "petstore",
		ConfigFile:  path,
		EnvPrefix:   "PETSTORE",
	})

	_, err := execute(t, root, "pets", "create-pet")
	require.NoError(t, err)
	assert.JSONEq(t, `{"body":{"name":"FromEnvironment","count":5}}`, string(invoker.last(t).Arguments),
		"the value only the file states is still read")
}

// A flag the caller typed wins over the environment, and the environment wins over
// the file, so the whole order holds at once.
func TestTheCommandLineWinsOverTheEnvironment(t *testing.T) {
	path := writeConfig(t, "petstore.yaml", "name: FromFile\ncount: 1\n")
	t.Setenv("PETSTORE_NAME", "FromEnvironment")
	t.Setenv("PETSTORE_COUNT", "2")

	invoker := &recorder{result: api.Result{Body: []byte(`{}`)}}
	root := generate(t, petstore(), invoker, cli.Options{
		CommandName: "petstore",
		ConfigFile:  path,
		EnvPrefix:   "PETSTORE",
	})

	_, err := execute(t, root, "pets", "create-pet", "--name", "Typed", "--count", "3")
	require.NoError(t, err)
	assert.JSONEq(t, `{"body":{"name":"Typed","count":3}}`, string(invoker.last(t).Arguments))
}

// The last rank of the order is the description's own default, and it is a
// default rather than a value: a call the caller said nothing about sends nothing,
// because an operation's declared default is the operation's to apply.
func TestTheDescriptionsDefaultIsTheLastRank(t *testing.T) {
	described := things(api.Operation{
		Name:   "listThings",
		Method: "GET",
		Parameters: []api.Parameter{{
			Name:   "pageSize",
			In:     api.ParameterInQuery,
			Schema: &api.Schema{Type: api.TypeInteger, Default: 20},
		}},
	})
	invoker := &recorder{result: api.Result{Body: []byte(`{}`)}}
	root := generate(t, described, invoker, cli.Options{CommandName: "test"})

	command := find(t, root, "things", "list-things")
	assert.Equal(t, "20", command.Flags().Lookup("pageSize").DefValue)

	_, err := execute(t, root, "things", "list-things")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{}, arguments(t, invoker.last(t)),
		"a default the description declares is not an argument the caller typed")
}

// A key the description names nothing for is refused with the key in the message,
// because a misspelled key is a value that quietly does nothing.
func TestAnUnknownConfigurationKeyIsRefused(t *testing.T) {
	// A key is matched without regard to case, because viper lowercases one, so
	// the misspelling has to be a misspelling of the word.
	path := writeConfig(t, "petstore.yaml", "petIdd: pet-7\n")
	invoker := &recorder{}
	root := generate(t, petstore(), invoker, cli.Options{CommandName: "petstore", ConfigFile: path})

	_, err := execute(t, root, "pets", "get-pet")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "petidd", "the key is named as the reader saw it")
	assert.Empty(t, invoker.calls)
}

// A file a caller named is read or the run is refused: a file that is quietly
// ignored is a deployment nobody wrote.
func TestAMissingConfigurationFileIsRefused(t *testing.T) {
	invoker := &recorder{}
	root := generate(t, petstore(), invoker, cli.Options{
		CommandName: "petstore",
		ConfigFile:  filepath.Join(t.TempDir(), "absent.yaml"),
	})

	_, err := execute(t, root, "pets", "list-pets")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "absent.yaml")
}

// A value the flag cannot carry is refused by the flag's own parser, naming the
// flag, rather than being cast into a zero.
func TestAValueTheFlagCannotCarryIsRefused(t *testing.T) {
	path := writeConfig(t, "petstore.yaml", "name: Rex\ncount: many\n")
	invoker := &recorder{}
	root := generate(t, petstore(), invoker, cli.Options{CommandName: "petstore", ConfigFile: path})

	_, err := execute(t, root, "pets", "create-pet")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "count")
	assert.Empty(t, invoker.calls)
}

// A value in the environment is read per flag of the command being run, so a tool
// whose operations share a word does not have one operation's value imposed on
// another.
func TestTheEnvironmentIsReadForTheCommandThatRuns(t *testing.T) {
	t.Setenv("PETSTORE_PETID", "pet-9")

	invoker := &recorder{result: api.Result{Body: []byte(`{}`)}}
	root := generate(t, petstore(), invoker, cli.Options{CommandName: "petstore", EnvPrefix: "PETSTORE"})

	_, err := execute(t, root, "pets", "get-pet")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"petId": "pet-9"}, arguments(t, invoker.last(t)))
}

// Where calls go is a flag like any other, so it can be stated once in a file.
func TestALocationCanBeStatedInTheFile(t *testing.T) {
	path := writeConfig(t, "petstore.yaml", "server: http://localhost:9999\n")
	invoker := &recorder{result: api.Result{Body: []byte(`{}`)}}
	root := generate(t, petstore(), invoker, cli.Options{CommandName: "petstore", ConfigFile: path})

	_, err := execute(t, root, "pets", "list-pets")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:9999", invoker.last(t).Server.BaseURL)
}

// A description that has an input called "config" keeps the bare flag in a tree
// that reads no configuration, and loses it to the layer in one that does — the
// layer's name is not handed to the description.
func TestTheConfigFlagIsReservedOnlyWhenTheLayerIsOn(t *testing.T) {
	described := things(api.Operation{
		Name:   "findThing",
		Method: "GET",
		Parameters: []api.Parameter{{
			Name:   "config",
			In:     api.ParameterInQuery,
			Schema: api.StringSchema(),
		}},
	})
	plain := generate(t, described, &recorder{}, cli.Options{CommandName: "test"})
	assert.NotNil(t, find(t, plain, "things", "find-thing").Flags().Lookup("config"))

	reserved := generate(t, described, &recorder{}, cli.Options{CommandName: "test", EnvPrefix: "TEST"})
	assert.NotNil(t, find(t, reserved, "things", "find-thing").Flags().Lookup("query.config"),
		"the layer's flag keeps its name, so the description's input is qualified")
}

// The file's format is taken from its own extension, and a caller may state it
// instead for a file that has no extension to read it from.
func TestTheConfigurationFormatFollowsTheFile(t *testing.T) {
	yaml := writeConfig(t, "petstore.yaml", "petId: from-yaml\n")
	invoker := &recorder{result: api.Result{Body: []byte(`{}`)}}
	root := generate(t, petstore(), invoker, cli.Options{CommandName: "petstore", ConfigFile: yaml})
	_, err := execute(t, root, "pets", "get-pet")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"petId": "from-yaml"}, arguments(t, invoker.last(t)))

	json := writeConfig(t, "petstore.conf", `{"petId":"from-json"}`)
	invoker = &recorder{result: api.Result{Body: []byte(`{}`)}}
	root = generate(t, petstore(), invoker, cli.Options{
		CommandName: "petstore",
		ConfigFile:  json,
		ConfigType:  "json",
	})
	_, err = execute(t, root, "pets", "get-pet")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"petId": "from-json"}, arguments(t, invoker.last(t)))
}

// The tree's own flags are not values a caller configures. An environment
// variable named after --help would otherwise turn every run into a help page.
func TestTheEnvironmentCannotSetTheTreesOwnFlags(t *testing.T) {
	t.Setenv("PETSTORE_HELP", "true")
	t.Setenv("PETSTORE_CONFIG", "/nowhere.yaml")

	invoker := &recorder{result: api.Result{Body: []byte(`{}`)}}
	root := generate(t, petstore(), invoker, cli.Options{CommandName: "petstore", EnvPrefix: "PETSTORE"})

	_, err := execute(t, root, "pets", "list-pets")
	require.NoError(t, err)
	require.Len(t, invoker.calls, 1, "the command ran rather than printing its help")
}

// --config names the file at run time, so one build of a tool reads whichever
// configuration the caller points it at.
func TestTheConfigFlagNamesTheFile(t *testing.T) {
	path := writeConfig(t, "petstore.yaml", "petId: from-the-flag\n")
	invoker := &recorder{result: api.Result{Body: []byte(`{}`)}}
	root := generate(t, petstore(), invoker, cli.Options{CommandName: "petstore", EnvPrefix: "PETSTORE"})

	_, err := execute(t, root, "pets", "get-pet", "--config", path)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"petId": "from-the-flag"}, arguments(t, invoker.last(t)))
}
