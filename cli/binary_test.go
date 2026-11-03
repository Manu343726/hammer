package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A binary is described by asking it, so these tests drive a program that is one:
// a shell script whose help is supplied by the test, which refuses to be run
// without --help and writes down every invocation. That last part is what lets a
// test assert the walk never executed a command for its side effects rather than
// merely that it happened to pass --help.

const fakeToolScript = `#!/bin/sh
here=$(dirname "$0")
printf '%s\n' "$*" >> "$here/invocations"
case "$*" in
  *--help) ;;
  *) printf 'refused: %s\n' "$*" >&2; exit 9 ;;
esac
key=$(printf '%s' "$*" | sed 's/^--help$//; s/ --help$//')
[ -z "$key" ] && key=root
page="$here/pages/$(printf '%s' "$key" | tr ' ' '_').page"
if [ -f "$page" ]; then cat "$page"; exit 0; fi
printf 'no page for %s\n' "$key" >&2
exit 1
`

// fakeTool writes a program whose help is the supplied pages, keyed by the words a
// caller types after the program's name. The empty key is the program's own page.
func fakeTool(t *testing.T, pages map[string]string) (string, func() []string) {
	t.Helper()
	directory := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(directory, "pages"), 0o755))
	script := filepath.Join(directory, "tool")
	require.NoError(t, os.WriteFile(script, []byte(fakeToolScript), 0o755))
	for key, page := range pages {
		name := strings.ReplaceAll(key, " ", "_")
		if name == "" {
			name = "root"
		}
		require.NoError(t, os.WriteFile(filepath.Join(directory, "pages", name+".page"), []byte(page), 0o644))
	}
	return script, func() []string {
		raw, err := os.ReadFile(filepath.Join(directory, "invocations"))
		if err != nil {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
}

const binaryRootPage = `A tool that does things.

Usage:
  tool [command]

Available Commands:
  add    Adds a thing.
  deep   Looks deeper.
  help   Help about any command
`

const binaryAddPage = `tool/tool/add

Adds a thing.

Usage:
  tool add [flags]

Flags:
      --file string   The file to add. (required)
`

// A binary is described by reading its own page and then one page per command it
// advertises, recursively.
func TestABinaryIsDescribedFromItsOwnHelp(t *testing.T) {
	path, invocations := fakeTool(t, map[string]string{
		"":          binaryRootPage,
		"add":       binaryAddPage,
		"deep":      "tool/tool/deep\n\nLooks deeper.\n\nUsage:\n  tool deep [command]\n\nAvailable Commands:\n  leaf   A leaf.\n",
		"deep leaf": "tool/deep/leaf\n\nA leaf.\n\nUsage:\n  tool deep leaf [flags]\n\nFlags:\n      --tag stringArray   Labels. (repeatable)\n",
	})

	described, err := cli.DescribeBinary(context.Background(), path, cli.DescribeOptions{})
	require.NoError(t, err)

	assert.Equal(t, "tool", described.ID)
	assert.Equal(t, cli.Format, described.Format)
	assert.Equal(t, path, described.Source.Location)
	require.Empty(t, described.Metadata[cli.MetadataTruncated], "a small tree is described whole")

	assert.Equal(t, []string{"tool/tool/add", "tool/deep/leaf"}, described.OperationIDs(),
		"the root's own commands are described before the ones a group under it holds")

	for _, invocation := range invocations() {
		assert.True(t, strings.HasSuffix(invocation, "--help"),
			"a command is only ever read, never run: %q", invocation)
	}
}

// A tool that groups subcommands advertises cobra's own help command, which is the
// tool's plumbing and is never read as an operation — nor invoked.
func TestABinarysHelpCommandIsNotRead(t *testing.T) {
	const page = `A tool that does things.

Usage:
  tool [command]

Available Commands:
  add    Adds a thing.
  help   Help about any command
`
	path, invocations := fakeTool(t, map[string]string{"": page, "add": binaryAddPage})

	_, err := cli.DescribeBinary(context.Background(), path, cli.DescribeOptions{})
	require.NoError(t, err)
	for _, invocation := range invocations() {
		assert.NotContains(t, invocation, "help --help")
	}
}

// A tool with one command is one operation, and its own page is both the API's
// prose and the operation's.
func TestABinaryOfOneCommandIsOneOperation(t *testing.T) {
	path, _ := fakeTool(t, map[string]string{
		"": "Prints the state of the world.\n\nUsage:\n  tool [flags]\n\nFlags:\n      --verbose   Say more.\n",
	})

	described, err := cli.DescribeBinary(context.Background(), path, cli.DescribeOptions{})
	require.NoError(t, err)
	require.Len(t, described.Operations(), 1)
	operation := described.Operations()[0]
	assert.Equal(t, "Prints the state of the world.", operation.Description)
	assert.NotNil(t, operation.Request)
	_, present := operation.Request.Property("verbose")
	assert.True(t, present, "a flag with no location is an input of the command, not a parameter of a transport")
}

// The walk is bounded by depth, so a tree deeper than the bound is described in
// part and says so rather than passing for a complete description.
func TestTheDepthBoundStopsTheWalk(t *testing.T) {
	path, invocations := fakeTool(t, map[string]string{
		"":            binaryRootPage,
		"add":         binaryAddPage,
		"deep":        "tool/tool/deep\n\nLooks deeper.\n\nUsage:\n  tool deep [command]\n\nAvailable Commands:\n  leaf1   A leaf.\n  deeper  Even deeper.\n",
		"deep leaf1":  "tool/deep/leaf1\n\nA leaf.\n\nUsage:\n  tool deep leaf1 [flags]\n",
		"deep deeper": "tool/deep/deeper\n\nDeeper.\n\nUsage:\n  tool deep deeper [command]\n\nAvailable Commands:\n  leaf2   The deepest leaf.\n",
	})

	described, err := cli.DescribeBinary(context.Background(), path, cli.DescribeOptions{MaxDepth: 2})
	require.NoError(t, err)
	assert.Contains(t, described.Metadata[cli.MetadataTruncated], "depth bound")
	assert.Equal(t, []string{"tool/tool/add", "tool/deep/leaf1"}, described.OperationIDs(),
		"the commands inside the bound are described")
	for _, invocation := range invocations() {
		assert.NotContains(t, invocation, "leaf2", "a command past the bound is not read at all")
	}
}

// The walk is bounded by the number of pages it reads, so a large tool is
// described in part rather than at any cost — and the part it left is stated.
func TestTheCommandBoundStopsTheWalk(t *testing.T) {
	pages := map[string]string{"": binaryRootPage, "add": binaryAddPage}
	path, invocations := fakeTool(t, pages)

	described, err := cli.DescribeBinary(context.Background(), path, cli.DescribeOptions{MaxCommands: 2})
	require.NoError(t, err)
	assert.Contains(t, described.Metadata[cli.MetadataTruncated], "command bound")
	assert.LessOrEqual(t, len(invocations()), 2, "the bound counts the pages the walk reads")
}

// One help invocation is bounded in time, and the caller's own deadline is
// honoured, so a tool that hangs is a failure rather than a hang.
func TestASlowHelpIsBoundByItsTimeout(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "slow")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755))

	started := time.Now()
	_, err := cli.DescribeBinary(context.Background(), script, cli.DescribeOptions{HelpTimeout: 50 * time.Millisecond})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindUnavailable), "a tool that does not answer is unavailable")
	assert.Less(t, time.Since(started), 10*time.Second)
}

// A caller's deadline is the outer bound, and it is reported as such.
func TestTheCallersDeadlineIsHonoured(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "slow")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := cli.DescribeBinary(ctx, script, cli.DescribeOptions{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindUnavailable))
}

// A program that is not there is named, rather than reported as a tool with no
// commands.
func TestAMissingProgramIsReported(t *testing.T) {
	_, err := cli.DescribeBinary(context.Background(), filepath.Join(t.TempDir(), "absent"), cli.DescribeOptions{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindNotFound))

	_, err = cli.DescribeBinary(context.Background(), "  ", cli.DescribeOptions{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
}

// Output that is not a help page is refused: a description is assembled from what
// the tool states, and a page it never printed describes nothing.
func TestOutputThatIsNotAHelpPageIsRefused(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "noisy")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho 'good morning'\n"), 0o755))

	_, err := cli.DescribeBinary(context.Background(), script, cli.DescribeOptions{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindFailedPrecondition))
}

// A command whose page cannot be read fails the description instead of being
// dropped: an incomplete description published as complete is the worse outcome.
func TestACommandThatCannotBeReadFailsTheDescription(t *testing.T) {
	path, _ := fakeTool(t, map[string]string{"": binaryRootPage})

	_, err := cli.DescribeBinary(context.Background(), path, cli.DescribeOptions{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindFailedPrecondition))
	assert.Contains(t, err.Error(), "add", "the failure names the command it could not read")
}

// A help page written to the diagnostic stream is still the tool's help, and
// refusing it would refuse a whole class of tools.
func TestHelpOnTheDiagnosticStreamIsRead(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "chatty")
	require.NoError(t, os.WriteFile(script, []byte(
		"#!/bin/sh\nprintf 'Prints the state.\\n\\nUsage:\\n  chatty [flags]\\n' >&2\n"), 0o755))

	described, err := cli.DescribeBinary(context.Background(), script, cli.DescribeOptions{})
	require.NoError(t, err)
	require.Len(t, described.Operations(), 1)
	assert.Equal(t, "Prints the state.", described.Operations()[0].Description)
}
