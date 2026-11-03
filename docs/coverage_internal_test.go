package docs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// documentedFile decides which descriptor documents a file, and it is the only place that does. Its
// callers all check for a missing file before reaching it, so the guard against a nil one cannot be
// reached through the exported API — which is why it is asserted from inside the package, where a new
// caller would be written. Without the guard, that new caller's first instinct would be a nil
// dereference in a help path rather than an empty answer.
func TestDocumentedFileOfNothingIsNothing(t *testing.T) {
	assert.Nil(t, documentedFile(nil))
}

// hasProtoSource is asked on every document extraction, and the empty path — the path a descriptor
// that arrived without a name reports — has to be answered rather than panicking on a map lookup.
func TestAnEmptyPathHasNoSource(t *testing.T) {
	assert.False(t, hasProtoSource(""))
}
