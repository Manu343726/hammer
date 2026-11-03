// Package cli turns an API description into a cobra command tree, so any API this
// library can describe can be driven from a terminal.
//
// It consumes the standard model and an api.Invoker, and nothing else. It never
// learns a transport and never reaches into a description format: the tree is built
// from what a parser produced, and a call is handed to whatever invoker the caller
// supplies — which is why Generate refuses a nil one. The same tree therefore drives
// an OpenAPI document, a protobuf contract, or a format contributed later, without a
// line here changing.
//
//	described, _, err := openapi.Parse(document, openapi.ParseOptions{})
//	if err != nil {
//		log.Fatal(err)
//	}
//	root, err := cli.Generate(described, openapi.NewInvoker(openapi.InvokerOptions{}), cli.Options{})
//	if err != nil {
//		log.Fatal(err)
//	}
//	os.Exit(cli.Execute(root))
//
// # What becomes what
//
// The API is the root command, a service is a subcommand, and an operation is a
// command that can be run:
//
//	petstore pets get-pet --petId 7
//
// A value the description declares becomes a flag. A path, query, header, or cookie
// parameter keeps its own name, and the request value's members are flags at their
// own names. A nested object is offered both as one JSON flag and as dotted flags —
// --prompt and --prompt.id — so setting one member does not mean hand-writing the
// document; the dotted flag wins for the member it names, because the JSON is
// applied first. An empty value is not sent at all: the difference between "the
// caller left it out" and "the caller sent zero" is not one a command line can see.
//
// Values travel as JSON, because the call carries JSON. A number, a boolean, an
// array, and a nested object therefore need no encoding machinery of their own, and
// nothing here depends on protobuf or on any other format's runtime.
//
// # What is refused
//
// An operation that streams is a command, and takes its flags so its help documents
// what it takes, but running it reports that a command line invokes unary
// operations only. A required input the caller omitted is refused before the call
// is made, because the server's rejection would name the wire field while the
// caller typed a flag.
//
// # The other direction
//
// A tool that only exists as a binary is still describable, so this package reads
// a command line back into a description as well as writing one out:
//
//	described, err := cli.DescribeBinary(ctx, "/usr/local/bin/tool", cli.DescribeOptions{})
//	described, err := cli.DescribeHelp(helpText, cli.DescribeOptions{})
//	described, err := cli.Describe(root, cli.DescribeOptions{})
//
// A command group is a service and a runnable command is an operation, which is the
// inverse of what the generator writes. The two readers share one translation, so a
// tree and the help it prints describe the same surface; the tree is the honest path
// when the source is available, and the text is the only path when it is not. A
// binary is asked for its help one page at a time, always with --help and never
// without it, bounded by depth, by the number of pages, and by a timeout.
//
// # What a command line cannot state
//
// A help page is a lossy document, and each loss below is a fact about the format
// rather than a gap this package could close. They are listed so a consumer of a
// description read this way knows what it is missing:
//
//   - A flag block is printed in one order, which pflag sorts. The description's
//     own order of a request value's members is therefore lost.
//   - An array's element type survives only for a list of scalars: a repeated value
//     is printed as stringArray, with the element type in the help text, so an
//     array of objects comes back as an array of undeclared elements.
//   - A command name is the word a caller types, which the generator derived from
//     the description's name. Only the fully-qualified identifier a generated help
//     prints restores the description's own spelling; without one, "get-pet" is the
//     operation's name and the round trip through the generator is stable but the
//     original spelling is gone.
//   - A member the description marks read-only gets no flag, so it is not described.
//   - A shorthand (-h beside --help) is a fact about the command line and the model
//     has no field for it, so it is not modelled.
//   - A hidden command, a hidden flag, a worked example, and an alias are all
//     invisible: the tool does not advertise them, and a description of what the
//     tool offers does not claim them. A deprecated command is described from a
//     tree, which knows it, and not from text, which does not.
//   - A group command's own run is not an operation. The model has no operation
//     that is also a service, and inventing one would make the generator flatten
//     the service and lose the children it was read for.
//   - A page prints one prose block: the description when there is one and the
//     summary when there is not, so an undocumented operation comes back documented
//     by its summary. Line breaks in prose are the document's layout, not the
//     thought's, and they do not survive.
//   - A parameter's location is only read back for the locations the model
//     documents. A format of the caller's own that carries a value elsewhere states
//     it in a helper the model has no word for, and it degrades to a request member.
//
// # A configurable command line
//
// A flag does not have to be typed on every invocation. Options.ConfigFile and
// Options.EnvPrefix add a persistent --config flag and read the flags from a file
// and the environment as well, in the conventional order: a flag the caller typed,
// then the environment, then the file, then the default the description declares.
// A default is the description's and not the generator's, and an operation the
// caller said nothing about still sends nothing.
//
// Both halves are opt-in: a caller who sets neither gets the tree this package has
// always built, without a --config flag and without a hook.
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	api "github.com/Manu343726/hammer"
	"github.com/spf13/cobra"
)

// Exit codes Execute returns. They follow the shell's own convention: a failure is
// one thing, and a request the caller has to change is another, so a script can
// tell "fix your arguments" from "try again later" without parsing a message.
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitInvalid = 2
)

// Options configures the generated command tree.
type Options struct {
	// CommandName is the name of the root command. An empty value uses the API's
	// own name, then its identifier, then "api": a terminal needs a word to type,
	// and a description with no name still describes an API somebody wants to call.
	CommandName string
	// Description replaces the API's own description in the root help.
	Description string
	// Server is the registered server calls are made against. The zero value is
	// derived from the location the description itself declares.
	//
	// It is a server rather than a URL because a call carries one: an invoker needs
	// the identity the endpoint is registered under, not only where to connect.
	// The generated root also takes --server, for the case where the description
	// declares more than one location or none.
	Server api.Server
	// ConfigFile is a file the flags are also read from. Empty states no file,
	// and the tree then offers --config so a caller can name one.
	//
	// A file a caller named is read or the run is refused: a configuration file
	// that is quietly ignored is a configuration nobody can rely on.
	ConfigFile string
	// ConfigType is the file's format — "yaml", "json", "toml" — and an empty
	// value takes it from the file's own extension.
	ConfigType string
	// EnvPrefix is the prefix of the environment variables the flags are read
	// from, so MYTOOL_PORT reads --port. Empty reads no environment.
	//
	// A caller who states neither ConfigFile nor EnvPrefix gets exactly the tree
	// this package has always built: no --config flag, and no hook.
	EnvPrefix string
}

// configured reports whether the caller asked for the configuration layer at all.
// Both halves of it are opt-in, and a tree that states neither is the tree this
// package has always built.
func (o Options) configured() bool {
	return strings.TrimSpace(o.ConfigFile) != "" || strings.TrimSpace(o.EnvPrefix) != ""
}

// Generate builds the command tree for one description.
//
// The invoker is what makes a command runnable; api.InvokerFunc adapts a function
// to it, which is all a test or an in-process integration needs. A description is
// annotated before it is named, so every operation has a stable identifier to
// print in its help and to hand to the invoker.
func Generate(described api.API, invoker api.Invoker, options Options) (*cobra.Command, error) {
	if invoker == nil {
		return nil, api.Errorf(api.KindInvalid, "a command tree needs an invoker to call its operations through")
	}
	description := described.Clone()
	if err := description.AnnotateOperationIDs(); err != nil {
		return nil, api.WrapError(api.KindInvalid, err, "the description cannot be turned into commands")
	}
	builder := &builder{
		description: description,
		invoker:     invoker,
		server:      targetServer(description, options),
		reserved:    reservedInputFlags(options),
	}
	root := builder.root(options)
	for _, plan := range builder.plans(root.Name()) {
		for _, operation := range plan.operations {
			command, err := builder.operationCommand(operation.operation, operation.name)
			if err != nil {
				return nil, err
			}
			if plan.flattened {
				root.AddCommand(command)
				continue
			}
			plan.command.AddCommand(command)
		}
		if !plan.flattened {
			root.AddCommand(plan.command)
		}
	}
	installConfiguration(root, options)
	InstallHelpLayout(root)
	return root, nil
}

// reservedInputFlags are the names a command of this tree owns. --config is only
// claimed when the configuration layer is on, so a description that has an input
// of that name keeps the bare flag in a tree that does not read one.
func reservedInputFlags(options Options) map[string]bool {
	reserved := make(map[string]bool, len(reservedFlags)+1)
	for name := range reservedFlags {
		reserved[name] = true
	}
	if options.configured() {
		reserved[configFlag] = true
	}
	return reserved
}

// Execute runs a generated tree and reports the outcome as a process exit code.
//
// A generated command returns a classified error rather than exiting, so the tree
// composes with a host command that owns its own process; this is the sibling that
// turns that classification into something a shell can read.
func Execute(root *cobra.Command) int {
	if root == nil {
		return ExitFailure
	}
	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	kind := api.KindOf(err)
	// The classification is printed when it says something the message does not.
	// "internal" is what an unclassified failure gets, and repeating it over a
	// transport error would read as if the caller had done something wrong.
	if kind == "" || kind == api.KindInternal {
		fmt.Fprintf(root.ErrOrStderr(), "%s: %s\n", root.CommandPath(), err)
	} else {
		fmt.Fprintf(root.ErrOrStderr(), "%s: %s: %s\n", root.CommandPath(), kind, err)
	}
	if kind == api.KindInvalid {
		return ExitInvalid
	}
	return ExitFailure
}

// builder carries what every command of one tree needs.
type builder struct {
	description api.API
	invoker     api.Invoker
	server      api.Server
	// reserved are the flag names the tree owns, so a description's input with one
	// of those names is qualified rather than shadowing it.
	reserved map[string]bool
}

func (b *builder) root(options Options) *cobra.Command {
	description := b.description
	if text := strings.TrimSpace(options.Description); text != "" {
		description.Description = text
	}
	root := &cobra.Command{
		Use:           commandName(description, options),
		Short:         firstSentence(rootSummary(description)),
		Long:          rootLong(description),
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.PersistentFlags().String("server", b.server.BaseURL,
		"Base URL to call, when the description declares none or another one is wanted.")
	return root
}

// commandName is the root command's name: what the caller chose, then what the
// description calls itself. It is taken as written rather than kebab-cased, because
// a caller who named their own command has already decided how it reads.
func commandName(description api.API, options Options) string {
	if name := strings.TrimSpace(options.CommandName); name != "" {
		return name
	}
	for _, candidate := range []string{description.Name, description.ID} {
		if word := commandWord(candidate); word != "" {
			return word
		}
	}
	return "api"
}

func rootSummary(description api.API) string {
	for _, candidate := range []string{description.Description, description.Title, description.Name} {
		if summary := firstSentence(candidate); summary != "" {
			return summary
		}
	}
	return "Commands generated from an API description"
}

// rootLong states where the description came from and where its calls go, which is
// what a reader who ran the bare command wants before choosing one of its children.
func rootLong(description api.API) string {
	var sections []string
	if text := unwrap(description.Description); text != "" {
		sections = append(sections, text)
	} else if title := strings.TrimSpace(description.Title); title != "" {
		sections = append(sections, title)
	}
	if len(description.DeclaredServers) > 0 {
		lines := make([]string, 0, len(description.DeclaredServers))
		for _, server := range description.DeclaredServers {
			line := "  " + server.URL
			if note := flatten(server.Description); note != "" {
				line += " — " + note
			}
			lines = append(lines, line)
		}
		sections = append(sections, "Declared servers:\n"+strings.Join(lines, "\n"))
	}
	if format := strings.TrimSpace(description.Format); format != "" {
		sections = append(sections, "Described from "+format+".")
	}
	return strings.Join(sections, "\n\n")
}

// targetServer is the server a call goes to when the caller does not say otherwise.
//
// Fields the description states are filled in from it: an invoker that wants the
// format or the transport should not have to guess them back out of a URL.
func targetServer(description api.API, options Options) api.Server {
	server := options.Server
	if strings.TrimSpace(server.ID) == "" {
		server.ID = description.ID
	}
	if strings.TrimSpace(server.Name) == "" {
		server.Name = description.Name
	}
	if strings.TrimSpace(server.Format) == "" {
		server.Format = description.Format
	}
	if strings.TrimSpace(server.Transport) == "" {
		server.Transport = description.Transport
	}
	if strings.TrimSpace(server.BaseURL) == "" && len(description.DeclaredServers) > 0 {
		server.BaseURL = strings.TrimSpace(description.DeclaredServers[0].URL)
	}
	return server
}

// printResult writes the response body as indented JSON.
//
// The body alone is printed, because a command's output is piped into another tool
// as often as it is read: a status line or a header block in the same stream would
// have to be stripped by every consumer. A body that is not JSON is written as it
// arrived rather than refused — a transport that serves text is still a transport
// this adapter describes, and mangling it would be worse than passing it through.
func printResult(out io.Writer, result api.Result) error {
	body := bytes.TrimSpace(result.Body)
	if len(body) == 0 {
		return nil
	}
	if json.Valid(body) {
		var indented bytes.Buffer
		if err := json.Indent(&indented, body, "", "  "); err == nil {
			body = indented.Bytes()
		}
	}
	if _, err := out.Write(body); err != nil {
		return api.WrapError(api.KindUnavailable, err, "write the response")
	}
	if _, err := io.WriteString(out, "\n"); err != nil {
		return api.WrapError(api.KindUnavailable, err, "write the response")
	}
	return nil
}
