package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A flag does not have to be typed on every invocation, and the sources it can be
// stated in have one conventional order. The tests here are about the edges of that
// layer rather than the order itself: a file that cannot be read, a key that names
// nothing, and a value whose shape the flag cannot carry are the three ways a
// configured command line fails, and each has to fail by naming the thing the
// caller wrote rather than by silently running with a value nobody chose.

// covConfigured builds a tree over a description with one plain string flag, one
// nested object, and one repeated value, which is every shape the configuration
// layer has to hand to a flag.
func covConfigured(t *testing.T, options cli.Options) (*cobra.Command, *recorder) {
	t.Helper()
	described := api.API{
		ID:   "configured",
		Name: "configured",
		DeclaredServers: []api.DeclaredServer{{
			URL: "https://configured.example.com",
		}},
		Services: []api.Service{{
			Name: "things",
			Operations: []api.Operation{{
				Name: "write",
				Request: api.ObjectSchema(
					api.Property{Name: "name", Schema: api.StringSchema()},
					api.Property{Name: "count", Schema: &api.Schema{Type: api.TypeInteger}},
					api.Property{Name: "tags", Schema: &api.Schema{
						Type:  api.TypeArray,
						Items: api.StringSchema(),
					}},
					api.Property{Name: "prompt", Schema: api.ObjectSchema(
						api.Property{Name: "id", Schema: api.StringSchema()},
					)},
					api.Property{Name: "a.b.c", Schema: api.StringSchema()},
				),
			}},
		}},
	}
	invoker := &recorder{result: api.Result{Body: []byte(`{"ok":true}`)}}
	return generate(t, described, invoker, options), invoker
}

// A file that cannot be read is refused, and the refusal names the path. A
// configuration file that is quietly ignored is a configuration nobody can rely
// on: the caller believes a value is in effect when it is not.
func TestAnUnreadableConfigurationFileIsRefusedByName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")
	root, invoker := covConfigured(t, cli.Options{ConfigFile: path})

	_, err := execute(t, root, "things", "write")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), path, "the refusal names the file the caller wrote")
	assert.Empty(t, invoker.calls)
}

// A file whose format cannot be parsed is refused for the same reason, and the
// format is taken from the file's own extension when the caller states none.
func TestAConfigurationFileOfTheWrongShapeIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))
	root, _ := covConfigured(t, cli.Options{ConfigFile: path})

	_, err := execute(t, root, "things", "write")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), path)
}

// An unknown key is refused by name rather than ignored: a misspelled key is a
// value the caller believes is in effect, and a command that ran anyway would
// report success for a configuration it never applied.
func TestAnUnknownConfigurationKeyIsRefusedByName(t *testing.T) {
	path := writeConfig(t, "configured.yaml", "nmae: Rex\n")
	root, invoker := covConfigured(t, cli.Options{ConfigFile: path})

	_, err := execute(t, root, "things", "write")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), `"nmae"`)
	assert.Empty(t, invoker.calls)
}

// A key that names a group of known keys is not a mistake: a nested document is
// how a caller states --prompt.id, and the group itself names no flag. Refusing it
// would refuse the shape the flag help documents.
func TestAGroupOfKnownKeysIsNotAnUnknownKey(t *testing.T) {
	// The description declares a flag named "a.b.c" — a member whose own name
	// carries dots — so "a" is a group above a known key and names no flag itself.
	path := writeConfig(t, "configured.yaml", "a:\n  b: 1\nprompt:\n  id: intro\n")
	root, invoker := covConfigured(t, cli.Options{ConfigFile: path})

	_, err := execute(t, root, "things", "write")
	require.NoError(t, err, "a group of known keys is read, not refused")
	body, ok := arguments(t, invoker.last(t))["body"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"id": "intro"}, body["prompt"])
}

// A flag that takes one value refuses a list. Handing the flag two values would
// mean the last one wins, which is a configuration the caller cannot read back
// from anything they wrote.
func TestAListForAFlagThatTakesOneValueIsRefused(t *testing.T) {
	path := writeConfig(t, "configured.yaml", "name: [Rex, Fido]\n")
	root, invoker := covConfigured(t, cli.Options{ConfigFile: path})

	_, err := execute(t, root, "things", "write")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "2 values for --name")
	assert.Empty(t, invoker.calls)
}

// A repeated flag takes its values from the file one element per occurrence, and
// an environment variable states one value. Both are the text the flag parses, so
// the call carries a list rather than the spelling the source happened to use.
func TestARepeatedValueIsReadFromEverySource(t *testing.T) {
	path := writeConfig(t, "configured.yaml", "tags:\n  - one\n  - two\n")
	t.Setenv("CONFIGURED_TAGS", "from-env")

	root, invoker := covConfigured(t, cli.Options{
		ConfigFile: path,
		EnvPrefix:  "CONFIGURED",
	})
	_, err := execute(t, root, "things", "write")
	require.NoError(t, err)
	body, ok := arguments(t, invoker.last(t))["body"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"from-env"}, body["tags"],
		"the environment wins over the file, and one exported value is one element")

	root, invoker = covConfigured(t, cli.Options{ConfigFile: path})
	_, err = execute(t, root, "things", "write")
	require.NoError(t, err)
	body, ok = arguments(t, invoker.last(t))["body"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"one", "two"}, body["tags"],
		"and a file states one value per line")
}

// A configured value the flag's own parser cannot read is refused naming the flag,
// because the message has to say which of the caller's statements was wrong. The
// value is handed over as text precisely so the flag, not a cast, reports it.
func TestAConfiguredValueTheFlagCannotParseIsRefused(t *testing.T) {
	path := writeConfig(t, "configured.yaml", "count: many\n")
	root, invoker := covConfigured(t, cli.Options{ConfigFile: path})

	_, err := execute(t, root, "things", "write")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "the configuration sets --count")
	assert.Empty(t, invoker.calls)
}

// No configuration options means the tree this package has always built: no
// --config flag, no hook, and a description's input named "config" keeps its own
// word. A tree that quietly gained a flag would shadow an input of that name.
func TestWithoutConfigurationOptionsNothingIsRead(t *testing.T) {
	described := api.API{
		ID:   "plain",
		Name: "plain",
		Services: []api.Service{{
			Name: "things",
			Operations: []api.Operation{{
				Name: "write",
				Request: api.ObjectSchema(
					api.Property{Name: "config", Schema: api.StringSchema()},
				),
			}},
		}},
	}
	root := generate(t, described, &recorder{}, cli.Options{})
	assert.Nil(t, root.PersistentFlags().Lookup("config"), "the layer is opt-in")
	assert.Nil(t, root.PersistentPreRunE, "and no hook that reads anything")
	command := find(t, root, "things", "write")
	assert.NotNil(t, command.Flags().Lookup("config"),
		"a description's own --config is the caller's, in a tree that reads no configuration")
}

// The precedence is the conventional one, and each rank is pinned against the one
// below it: a flag the caller typed beats the environment, which beats the file,
// which beats the default the description declares. The description's default is
// never sent, so it only ever decides what the help prints.
func TestTheConfigurationPrecedenceIsFlagsThenEnvThenFileThenDefault(t *testing.T) {
	described := api.API{
		ID:   "layered",
		Name: "layered",
		DeclaredServers: []api.DeclaredServer{{
			URL: "https://layered.example.com",
		}},
		Services: []api.Service{{
			Name: "things",
			Operations: []api.Operation{{
				Name: "write",
				Request: api.ObjectSchema(
					api.Property{Name: "name", Schema: &api.Schema{
						Type:        api.TypeString,
						Default:     "the-description",
						Description: "What to call the thing.",
					}},
				),
			}},
		}},
	}
	path := writeConfig(t, "layered.yaml", "name: the-file\n")

	for _, test := range []struct {
		name    string
		options cli.Options
		env     string
		args    []string
		want    string
	}{
		{"file", cli.Options{ConfigFile: path}, "", nil, "the-file"},
		{"environment", cli.Options{ConfigFile: path, EnvPrefix: "LAYERED"}, "the-env", nil, "the-env"},
		{"flag", cli.Options{ConfigFile: path, EnvPrefix: "LAYERED"}, "the-env",
			[]string{"--name", "the-flag"}, "the-flag"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.env != "" {
				t.Setenv("LAYERED_NAME", test.env)
			}
			invoker := &recorder{result: api.Result{Body: []byte(`{"ok":true}`)}}
			root := generate(t, described, invoker, test.options)
			args := append([]string{"things", "write"}, test.args...)
			_, err := execute(t, root, args...)
			require.NoError(t, err)
			body, ok := arguments(t, invoker.last(t))["body"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, test.want, body["name"])
		})
	}

	// The last rank states nothing on the wire: the description's default is the
	// server's to apply, and a command that filled it in would send a value nobody
	// chose.
	t.Run("description", func(t *testing.T) {
		invoker := &recorder{result: api.Result{Body: []byte(`{"ok":true}`)}}
		root := generate(t, described, invoker, cli.Options{})
		_, err := execute(t, root, "things", "write")
		require.NoError(t, err)
		assert.Empty(t, arguments(t, invoker.last(t))["body"],
			"an operation the caller said nothing about still sends nothing")
	})
}
