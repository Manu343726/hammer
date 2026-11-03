package cli

import (
	"fmt"
	"sort"
	"strings"

	api "github.com/Manu343726/hammer"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// A command line is read by a person and by a script, and neither of them wants to
// repeat every value on every invocation. The layer below lets a caller state a
// flag's value once, in a file or in the environment, and keeps the conventional
// precedence: what was typed wins over what was exported, which wins over what was
// written down, which wins over what the description declares.
//
// The description's own default is the last rank and is deliberately never applied:
// it is a statement about the operation, and a command that filled it in would be
// turning a default the server already applies into an argument nobody typed.

// configFlag names the flag that states which file to read. It is reserved for
// the layer, which is why a description that declares an input of that name gets
// a qualified flag instead of taking it.
const configFlag = "config"

// keyDelimiter is what viper splits a key on, and it is not a dot.
//
// A nested request member is addressed as --prompt.id, so a dot is data in a flag
// name. With viper's default delimiter that name reads as a key "id" under
// "prompt", and viper then refuses to look it up at all — a bound key shadows any
// longer key beneath it, so --prompt would silently make --prompt.id unreadable
// from both the file and the environment. The separator therefore has to be
// something a flag name cannot contain.
const keyDelimiter = "/"

// configUsage is the help of the flag the layer adds.
const configUsage = "A file to read flag values from, in addition to the environment and the command line."

// installConfiguration adds the configuration layer to a tree.
//
// A caller who states neither a file nor an environment prefix gets exactly the
// tree they would have got without it: no flag, no hook, no file read.
func installConfiguration(root *cobra.Command, options Options) {
	if !options.configured() {
		return
	}
	root.PersistentFlags().String(configFlag, strings.TrimSpace(options.ConfigFile), configUsage)
	// The hook is the root's, so every command of the tree resolves its values the
	// same way. Cobra runs one persistent hook, the closest one, and a host that
	// composes this tree into its own owns that hook from then on.
	root.PersistentPreRunE = func(command *cobra.Command, _ []string) error {
		return applyConfiguration(command, options)
	}
}

// settingsFor builds the resolver one run uses.
//
// It is built per run rather than once per tree, because a value read out of one
// file must not linger into a run that names another: a caller who executes a tree
// twice — a test, a long-lived host — would otherwise be configured by a file it no
// longer points at.
func settingsFor(root *cobra.Command, options Options) *viper.Viper {
	settings := viper.NewWithOptions(viper.KeyDelimiter(keyDelimiter))
	if prefix := strings.TrimSpace(options.EnvPrefix); prefix != "" {
		settings.SetEnvPrefix(prefix)
		// A flag name carries the hyphens a word is spelled with and the dots a
		// nested member is addressed with, and neither may appear in an
		// environment variable's name.
		settings.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
		settings.AutomaticEnv()
	}
	bindFlags(settings, root)
	return settings
}

// bindFlags binds every flag of a tree to its own name, which is the key a file
// and the environment use. A value therefore applies to every operation that
// declares a flag of that name — the names are the description's own, and a
// description that uses one name for two inputs has already said they are the
// same value.
func bindFlags(settings *viper.Viper, root *cobra.Command) {
	bind := func(flag *pflag.Flag) {
		if flag == nil || flag.Name == configFlag || flag.Name == "help" {
			return
		}
		// Viper lowercases a key, and so does every accessor below.
		_ = settings.BindPFlag(strings.ToLower(flag.Name), flag)
	}
	for _, command := range everyCommand(root) {
		command.Flags().VisitAll(bind)
	}
	// The inherited flags are bound once, at the root that declares them, because
	// they are one flag however many commands see it.
	root.PersistentFlags().VisitAll(bind)
}

func everyCommand(root *cobra.Command) []*cobra.Command {
	commands := make([]*cobra.Command, 0, 8)
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		commands = append(commands, command)
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(root)
	return commands
}

// applyConfiguration resolves one command's flag values from every source.
func applyConfiguration(command *cobra.Command, options Options) error {
	settings := settingsFor(command.Root(), options)
	path, err := configPath(command, options)
	if err != nil {
		return err
	}
	if path != "" {
		document, keys, err := readConfigFile(path, options.ConfigType)
		if err != nil {
			return err
		}
		if err := checkConfigKeys(keys, command.Root()); err != nil {
			return err
		}
		for key, value := range document {
			// A default is the one rank that sits above a flag's own default and
			// below the file, the environment, and the command line, which is
			// where a value read out of a file belongs once the file's own shape
			// has been flattened into the keys the flags live under.
			settings.SetDefault(key, value)
		}
	}
	return applyValues(command, settings)
}

// configPath is the file to read: what the caller named, then what was typed.
func configPath(command *cobra.Command, options Options) (string, error) {
	path := strings.TrimSpace(options.ConfigFile)
	flag := command.Flags().Lookup(configFlag)
	if flag == nil || !flag.Changed {
		return path, nil
	}
	value, err := command.Flags().GetString(configFlag)
	if err != nil {
		return "", api.WrapError(api.KindInvalid, err, "read --%s", configFlag)
	}
	// A caller who typed the flag meant what they typed, including nothing.
	return strings.TrimSpace(value), nil
}

// readConfigFile parses one file into the flat keys a flag is bound under.
//
// The file is read by a viper of its own, so the document's keys and the tree's
// keys are not confused: this instance knows nothing about flags, which is what
// makes its settings the file's own content and nothing else.
func readConfigFile(path, kind string) (map[string]any, []string, error) {
	document := viper.NewWithOptions(viper.KeyDelimiter(keyDelimiter))
	document.SetConfigFile(path)
	if kind = strings.TrimSpace(kind); kind != "" {
		document.SetConfigType(kind)
	}
	if err := document.ReadInConfig(); err != nil {
		return nil, nil, api.WrapError(api.KindInvalid, err, "read the configuration file %q", path)
	}
	flat := make(map[string]any, 16)
	keys := make([]string, 0, 16)
	flattenConfig(document.AllSettings(), "", flat, &keys)
	sort.Strings(keys)
	return flat, keys, nil
}

// flattenConfig joins a nested document into the dotted keys a flag is named by,
// so `prompt: {id: x}` and `prompt.id: x` are one statement.
func flattenConfig(values map[string]any, prefix string, flat map[string]any, keys *[]string) {
	for name, value := range values {
		key := strings.ToLower(name)
		if prefix != "" {
			key = prefix + "." + key
		}
		if nested, ok := value.(map[string]any); ok && len(nested) > 0 {
			flattenConfig(nested, key, flat, keys)
			continue
		}
		flat[key] = value
		*keys = append(*keys, key)
	}
}

// checkConfigKeys refuses a key that names nothing, so a misspelled one is a
// mistake the caller is told about rather than a value that quietly does nothing.
func checkConfigKeys(keys []string, root *cobra.Command) error {
	known := knownKeys(root)
	for _, key := range keys {
		if known[key] || prefixOfKnown(known, key) {
			continue
		}
		return api.Errorf(api.KindInvalid,
			"the configuration file states %q, which no flag of %q is named", key, root.Name())
	}
	return nil
}

func knownKeys(root *cobra.Command) map[string]bool {
	known := make(map[string]bool, 16)
	for _, command := range everyCommand(root) {
		for _, set := range []*pflag.FlagSet{command.Flags(), command.PersistentFlags()} {
			set.VisitAll(func(flag *pflag.Flag) { known[strings.ToLower(flag.Name)] = true })
		}
	}
	return known
}

// prefixOfKnown reports whether a key is the group above a known key. A document
// is read as a tree, and a group is not a value.
func prefixOfKnown(known map[string]bool, key string) bool {
	for candidate := range known {
		if strings.HasPrefix(candidate, key+".") {
			return true
		}
	}
	return false
}

// applyValues hands every resolved value to the flag that carries it.
//
// The value is set on the flag rather than read at call time, so a configured
// value and a typed one are the same thing from here on: the required-input check,
// the JSON decoding, and the dotted members all treat it exactly as they treat
// what a caller typed.
func applyValues(command *cobra.Command, settings *viper.Viper) error {
	var failure error
	command.Flags().VisitAll(func(flag *pflag.Flag) {
		if failure != nil || flag.Changed {
			return
		}
		// The tree's own flags are not values a caller configures: --config names
		// the source, and an environment variable named after --help would turn
		// every run into a help page.
		if flag.Name == configFlag || flag.Name == "help" {
			return
		}
		key := strings.ToLower(flag.Name)
		// A key no source states leaves the flag alone, so the description's own
		// default is the only default a call ever sees.
		if !settings.IsSet(key) {
			return
		}
		values, err := configValues(settings, key)
		if err != nil {
			failure = err
			return
		}
		if _, slice := flag.Value.(pflag.SliceValue); !slice && len(values) > 1 {
			failure = api.Errorf(api.KindInvalid,
				"the configuration states %d values for --%s, which takes one", len(values), flag.Name)
			return
		}
		for _, value := range values {
			if err := command.Flags().Set(flag.Name, value); err != nil {
				failure = api.WrapError(api.KindInvalid, err,
					"the configuration sets --%s", flag.Name)
				return
			}
		}
	})
	return failure
}

// configValues renders one resolved value as the text a flag parses. A value is
// handed over as text so the flag's own parser reports a value it cannot read,
// naming itself, instead of a cast quietly turning a misspelling into a zero.
func configValues(settings *viper.Viper, key string) ([]string, error) {
	switch value := settings.Get(key).(type) {
	case nil:
		return nil, nil
	case []string:
		return append([]string(nil), value...), nil
	case []any:
		values := make([]string, 0, len(value))
		for _, element := range value {
			values = append(values, fmt.Sprint(element))
		}
		return values, nil
	default:
		return []string{fmt.Sprint(value)}, nil
	}
}
