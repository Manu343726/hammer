package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A description's prose is wrapped to the width of the file it was written in, and
// everything derived from it inherits that wrapping. These tests are about the
// undoing of it: a summary that ends mid-clause is a help page that says less than
// the description it came from, and it is very easy to introduce by moving a line
// break in a document.

func TestASummaryIsAWholeSentenceNotASourceLine(t *testing.T) {
	// The comment an author wrote, wrapped at the width they happened to wrap at.
	// The first source line ends mid-clause.
	description := "Reads one pet. It is addressed by its identifier, so a\ncaller needs nothing else.\n\nA second paragraph says more."

	assert.Equal(t, "Reads one pet.", firstSentence(description),
		"the summary is the first sentence, wherever the author broke the line")
}

// A period inside a version is not the end of a sentence: a summary cut at "v1."
// names nothing and stops mid-thought.
func TestAVersionDoesNotEndASentence(t *testing.T) {
	assert.Equal(t, "Parses documents written for v1.2 of the format.",
		firstSentence("Parses documents written for v1.2 of the format. Then it validates them."))
}

// Neither does an initialism: "e.g." closes a word of one letter, not a sentence.
func TestAnAbbreviationDoesNotEndASentence(t *testing.T) {
	assert.Equal(t, "Accepts a format, e.g. openapi.",
		firstSentence("Accepts a format, e.g. openapi. Anything else is refused."))
}

// Structure survives the unwrapping that the author's line breaks do not: a blank
// line is a paragraph, and a list item starts its own line.
func TestUnwrapKeepsStructure(t *testing.T) {
	unwrapped := unwrap("One thought\nthat was wrapped.\n\n- first item\n- second item\n\nA closing paragraph.")

	assert.Equal(t, "One thought that was wrapped.\n\n- first item\n- second item\n\nA closing paragraph.", unwrapped)
	// The list did not collapse into a sentence of dashes.
	assert.Contains(t, unwrapped, "- first item\n- second item")
}

// A flag's help is one line: it sits in a column beside its name, where a paragraph
// break reads as three separate flags.
func TestFlattenIsOneLine(t *testing.T) {
	assert.Equal(t, "One thought that was wrapped. And another one.",
		flatten("One thought\nthat was wrapped.\n\nAnd another one."))
}

// A long help line is wrapped to the terminal rather than run off the edge, and the
// continuation is indented under the column it continues.
func TestTextWrapsToTheWidthItIsGiven(t *testing.T) {
	const indent = "  "
	wrapped := wrapText("one two three four five six", 16, indent, 0)

	lines := strings.Split(wrapped, "\n")
	require.Greater(t, len(lines), 1, "the text did not wrap at all")
	for _, line := range lines {
		assert.LessOrEqual(t, len(line), 16, "line %q is wider than the wrap width", line)
	}
	assert.True(t, strings.HasPrefix(lines[1], indent), "the continuation is indented")
	assert.Equal(t, "one two three four five six", strings.Join(strings.Fields(wrapped), " "),
		"wrapping moves words, it does not lose them")
}

// The width is bounded on both sides. A description is unreadable at any width, so a
// terminal that cannot be measured gets a readable default; and a description that is
// never wrapped is one that needs scrolling, so a very wide terminal gets a ceiling.
func TestTheTerminalWidthIsBounded(t *testing.T) {
	t.Setenv("COLUMNS", "20")
	assert.GreaterOrEqual(t, terminalWidth(), 60)

	t.Setenv("COLUMNS", "400")
	assert.LessOrEqual(t, terminalWidth(), 120)
}

// The help a reader sees is laid out for their terminal: a flag description longer
// than the terminal is wrapped, and no line runs past the edge.
func TestAFlagDescriptionWrapsToTheTerminal(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("word ", 60)) + " end"
	command := &cobra.Command{Use: "test", Short: "A test command."}
	command.Flags().String("flag", "", long)
	InstallHelpLayout(command)

	usage := command.UsageString()
	assert.NotContains(t, usage, long, "a description longer than the terminal is not one line")
	for _, line := range strings.Split(usage, "\n") {
		assert.LessOrEqual(t, len(line), 122, "line %q runs past the terminal", line)
	}
}

// A summary longer than the terminal is wrapped under its own column instead of
// running off the edge or being written at column zero, where it would read as a
// separate command.
func TestACommandSummaryWrapsUnderItsColumn(t *testing.T) {
	summary := strings.TrimSpace(strings.Repeat("summary ", 40))
	rendered := commandSummary("some-command", len("some-command"), summary)

	lines := strings.Split(rendered, "\n")
	require.Greater(t, len(lines), 1)
	for _, line := range lines {
		assert.LessOrEqual(t, len(line), 122, "line %q runs past the terminal", line)
	}
	assert.True(t, strings.HasPrefix(lines[1], strings.Repeat(" ", len("some-command")+3)),
		"the continuation is indented past the name column")
}
