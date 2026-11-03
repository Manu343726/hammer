package cli

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The layout helpers are where a description's prose is turned into a terminal's
// prose, and every branch of the undoing is a shape a document can actually take:
// a leading blank line, a list item that continues on the next line, an ordered
// list number with more than one digit. None of them is visible from a generated
// tree, which is why they are held here rather than through Generate.

// A leading or repeated blank line is layout, not a paragraph: unwrap must not emit
// an empty paragraph for one, because a blank paragraph renders as two blank lines
// in the middle of a help page.
func TestUnwrapDropsEmptyParagraphs(t *testing.T) {
	assert.Equal(t, "one\n\ntwo", unwrap("\n\none\n\n\n\ntwo\n\n"))
	assert.Equal(t, "", unwrap(" \n\t\n"))
}

// A list item owns the line it starts, so an indented continuation under it stays
// on its own line instead of being joined to the item. Joining them would render a
// bulleted list as one paragraph of dashes, which is the defect the standalone flag
// exists to prevent.
func TestUnwrapKeepsAListItemAndItsContinuation(t *testing.T) {
	assert.Equal(t, "- one\nand its continuation\n- two", unwrap("- one\nand its continuation\n- two"))
}

// An ordered list number may have more than one digit: "10." is a list item as much
// as "1." is, and a reader that only checks the first digit turns the rest of the
// list into prose.
func TestUnwrapRecognizesAMultiDigitListItem(t *testing.T) {
	assert.Equal(t, "9. nine\n10. ten", unwrap("9. nine\n10. ten"))
	assert.Equal(t, "1.no space after the dot", unwrap("1.no space after the dot"))
}

// The width to wrap to is the reader's terminal, and a descriptor that cannot be
// asked falls back to the shell's own statement of it. A terminal is replaced here
// by a pipe so the answer does not depend on where the tests are run from.
func TestTheTerminalWidthFallsBackToTheShell(t *testing.T) {
	reader, writer, err := os.Pipe()
	assert.NoError(t, err)
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = writer, writer
	defer func() {
		os.Stdout, os.Stderr = stdout, stderr
		writer.Close()
		reader.Close()
	}()

	t.Setenv("COLUMNS", " 72 ")
	width, ok := terminalSize()
	assert.True(t, ok, "a shell that states a width has answered the question")
	assert.Equal(t, 72, width)
	assert.Equal(t, 72, terminalWidth())

	t.Setenv("COLUMNS", "not a number")
	width, ok = terminalSize()
	assert.False(t, ok, "a width that cannot be read is no answer rather than zero")
	assert.Equal(t, 0, width)
	assert.Equal(t, 100, terminalWidth(), "and the floor is the width a reader is given")
}
