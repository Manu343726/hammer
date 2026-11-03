package cli

import (
	"bytes"
	"strconv"
	"strings"

	api "github.com/Manu343726/hammer"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// describedCommands is every command of a tree this reader describes, in the
// order the tree presents them: a command, then its own children.
func describedCommands(root *cobra.Command) []*cobra.Command {
	commands := make([]*cobra.Command, 0, 8)
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		commands = append(commands, command)
		for _, child := range describedChildren(command) {
			walk(child)
		}
	}
	walk(root)
	return commands
}

func describedChildren(command *cobra.Command) []*cobra.Command {
	children := make([]*cobra.Command, 0, len(command.Commands()))
	for _, child := range command.Commands() {
		if describedCommand(child) {
			children = append(children, child)
		}
	}
	return children
}

// describedCommand reports whether a command is one the tool offers.
//
// A hidden command is not: the tool keeps it out of its help, and a description
// of what a caller may invoke should say what the tool says. Cobra's own help
// command is not either — it is the tool's plumbing, recognisable as the one
// command that is neither available, hidden, nor deprecated.
func describedCommand(command *cobra.Command) bool {
	if command.Hidden {
		return false
	}
	if !command.IsAvailableCommand() && command.Deprecated == "" {
		return false
	}
	return true
}

// pageOf reads one command as the page it would print.
func pageOf(command *cobra.Command) commandPage {
	page := commandPage{
		path:       strings.Fields(command.CommandPath()),
		summary:    command.Short,
		long:       command.Long,
		runnable:   command.Runnable(),
		deprecated: command.Deprecated != "",
	}
	// A page opens with the long help, or with the one line when there is none,
	// which is what cobra's help template does.
	if strings.TrimSpace(page.long) == "" {
		page.long = command.Short
	}
	for _, child := range describedChildren(command) {
		page.children = append(page.children, commandRef{name: child.Name(), summary: child.Short})
	}
	seen := make(map[string]bool, 8)
	add := func(flag *pflag.Flag) {
		if flag == nil || seen[flag.Name] {
			return
		}
		seen[flag.Name] = true
		if line, ok := flagLineOf(flag); ok {
			page.flags = append(page.flags, line)
		}
	}
	// The page's own flags come first and the ones it inherits after, which is
	// the order the two blocks appear in, and the order a description's
	// parameters were declared in.
	command.Flags().VisitAll(add)
	command.InheritedFlags().VisitAll(add)
	return page
}

// flagLineOf reads one flag as the line a page would print for it.
func flagLineOf(flag *pflag.Flag) (flagLine, bool) {
	if flag.Hidden {
		return flagLine{}, false
	}
	line := flagLine{name: flag.Name}
	// UnquoteUsage is pflag's own rendering of the type column, including its
	// reduction of int64 to int, so reading a tree and reading a page agree about
	// a flag's type word — and a description taken from a tree is not a more
	// precise one by accident.
	line.typeWord, line.usage = pflag.UnquoteUsage(flag)
	// The default and the deprecation are appended exactly where pflag appends
	// them, so a flag read from a tree and the same flag read from a page carry
	// the same help text and go through one reader.
	if !zeroDefault(flag) {
		value := flag.DefValue
		if flag.Value.Type() == "string" {
			// A page prints a string default quoted and every other default bare,
			// so a default states its own type and one reader can read both.
			value = strconv.Quote(flag.DefValue)
		}
		line.usage += " (default " + value + ")"
	}
	if flag.Deprecated != "" {
		line.usage += " (DEPRECATED: " + flag.Deprecated + ")"
	}
	return line, true
}

// zeroDefault reports whether a flag's default is the zero value its type starts
// at. pflag prints a default only when it is not, so a reader that wants to agree
// with a printed page has to ask the same question.
func zeroDefault(flag *pflag.Flag) bool {
	switch flag.Value.Type() {
	case "bool", "boolfunc":
		return flag.DefValue == "false"
	case "string":
		return flag.DefValue == ""
	case "duration":
		return flag.DefValue == "0s"
	case "count", "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float", "float32", "float64":
		return flag.DefValue == "0"
	default:
		return flag.DefValue == "" || flag.DefValue == "[]" || flag.DefValue == "0s" ||
			flag.DefValue == "false"
	}
}

// renderHelp renders one command's help page.
func renderHelp(command *cobra.Command) (string, error) {
	out, errOut := command.OutOrStdout(), command.ErrOrStderr()
	page := &bytes.Buffer{}
	command.SetOut(page)
	command.SetErr(page)
	err := command.Help()
	command.SetOut(out)
	command.SetErr(errOut)
	if err != nil {
		return "", api.WrapError(api.KindInternal, err, "render the help of %q", command.CommandPath())
	}
	return page.String(), nil
}
