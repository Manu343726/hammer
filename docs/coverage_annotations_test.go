package docs_test

import (
	"testing"

	shareddocs "github.com/Manu343726/hammer/docs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An annotation is the only place a ConnectRPC contract can say what calling a method does — every
// RPC is a POST, so nothing about the transport carries it. What is tested here is the parser's
// whole vocabulary: which lines are instructions, which are prose, and what a key this package does
// not recognise still has to survive. A rule that is wrong here does not fail loudly; it silently
// leaves an operation unclassified, and an unclassified operation is one a policy cannot grant.

func TestALookupAnswersTheValuesAKeyDeclared(t *testing.T) {
	set := shareddocs.AnnotationSet{
		{Key: "toolbox.side-effects", Values: []string{"read_only"}},
		{Key: "toolbox.deprecated"},
	}

	// The key is trimmed because it arrives from a caller that built it from a flag or a map key,
	// and a lookup that failed on surrounding space would report a declared annotation as absent.
	values, ok := set.Lookup(" toolbox.side-effects ")
	require.True(t, ok, "a declared key was reported absent")
	assert.Equal(t, []string{"read_only"}, values)

	// A valueless flag is still present: its presence is the whole declaration, and reporting it
	// absent would make "deprecated" indistinguishable from "not deprecated".
	values, ok = set.Lookup("toolbox.deprecated")
	require.True(t, ok)
	assert.Empty(t, values)

	values, ok = set.Lookup("toolbox.unknown")
	assert.False(t, ok)
	assert.Nil(t, values)
}

// A key declared twice is a contract that said the same thing twice, and a lookup has to answer
// something rather than panic or report the key absent. It answers the first declaration, which is
// the order the comment was written in.
func TestARepeatedKeyIsReportedOnceAndLookedUpFromTheFirstDeclaration(t *testing.T) {
	prose, annotations := shareddocs.ParseAnnotations(
		`Fetch one pet.

@toolbox.side-effects read_only
@toolbox.side-effects write`)

	assert.Equal(t, "Fetch one pet.", prose)
	require.Len(t, annotations, 2, "a repeated declaration was collapsed into one")

	values, ok := annotations.Lookup("toolbox.side-effects")
	require.True(t, ok)
	assert.Equal(t, []string{"read_only"}, values, "the first declaration is the one a lookup answers")

	// The distinct keys are what a caller that validates a vocabulary iterates. Both declarations
	// are the same key, so the key is reported once — a caller warned twice about one typo would
	// report the same problem as two.
	assert.Equal(t, []string{"toolbox.side-effects"}, annotations.Keys())
}

// Keys is what a caller that reports "I do not know this annotation" iterates, so it has to be
// sorted: an error message that reorders itself between runs cannot be compared or asserted on.
func TestKeysAreDistinctAndSorted(t *testing.T) {
	set := shareddocs.AnnotationSet{
		{Key: "toolbox.side-effects"},
		{Key: "acme.audit"},
		{Key: "toolbox.side-effects"},
		{Key: "acme.rate-limit", Values: []string{"10"}},
	}

	assert.Equal(t, []string{"acme.audit", "acme.rate-limit", "toolbox.side-effects"}, set.Keys())

	var empty shareddocs.AnnotationSet
	assert.Empty(t, empty.Keys(), "an undocumented method must not be reported as having keys")
}

// Merge is how a method inherits what its service declared. It is a union, and the receiver's
// declarations come first — which is the order a caller reads when it wants to know what the
// nearer declaration said.
func TestMergeIsAUnionThatKeepsEveryDeclaration(t *testing.T) {
	service := shareddocs.AnnotationSet{{Key: "toolbox.side-effects", Values: []string{"read_only"}}}
	method := shareddocs.AnnotationSet{
		{Key: "toolbox.rate-limit", Values: []string{"10"}},
		{Key: "toolbox.side-effects", Values: []string{"write"}},
	}

	merged := service.Merge(method)

	require.Len(t, merged, 3, "merging dropped a declaration")
	assert.Equal(t, "toolbox.side-effects", merged[0].Key, "the receiver's declarations come first")
	assert.Equal(t, []string{"read_only"}, merged[0].Values)

	// Both declarations of the same key survive; a union that deduplicated would lose the fact
	// that the method and its service disagree.
	values, ok := merged.Lookup("toolbox.rate-limit")
	require.True(t, ok)
	assert.Equal(t, []string{"10"}, values)

	assert.Equal(t, shareddocs.AnnotationSet(nil).Merge(nil).Keys(), shareddocs.AnnotationSet{}.Keys(),
		"merging two absent sets yields an empty set rather than a panic")
}

// A comment that is nothing but annotations has no prose, and that is the common shape for a method
// whose description is generated from its signature. Reporting the annotation line as prose would
// put a machine-readable instruction into the help text.
func TestACommentOfAnnotationsOnlyHasNoProse(t *testing.T) {
	prose, annotations := shareddocs.ParseAnnotations("@toolbox.side-effects write_only\n@toolbox.deprecated")

	assert.Empty(t, prose, "an annotation line leaked into the prose")
	require.Len(t, annotations, 2)
	assert.Equal(t, "toolbox.side-effects", annotations[0].Key)
	assert.Equal(t, []string{"write_only"}, annotations[0].Values)
	assert.Empty(t, annotations[1].Values)
}

// The vocabulary belongs to the package that owns the model, not to this parser: an unknown
// namespaced key is returned, because this package cannot know whether it is a typo or a key
// another package defines. Dropping it would make that other package's annotations unreadable.
func TestAnUnknownNamespacedAnnotationIsStillReturned(t *testing.T) {
	prose, annotations := shareddocs.ParseAnnotations("Do a thing.\n@some.other-vocabulary value1 value2")

	assert.Equal(t, "Do a thing.", prose)
	values, ok := annotations.Lookup("some.other-vocabulary")
	require.True(t, ok, "an annotation this package does not know was dropped")
	assert.Equal(t, []string{"value1", "value2"}, values)
}

// A line is only an annotation when it is a dotted, well-formed key. Everything else is prose, and
// the list below is every way a comment line can look like an annotation and not be one. Getting
// this wrong in the other direction is worse than losing a description: a sentence beginning with
// an at-sign would become a machine instruction the contract never declared.
func TestAMalformedAnnotationLineStaysProse(t *testing.T) {
	for _, line := range []string{
		"@toolbox",         // no namespace
		"@.side-effects",   // no namespace segment
		"@toolbox.",        // no name segment
		"@Toolbox.effects", // upper case is not part of the vocabulary
		"@toolbox.Side",    // neither is an upper-case name
		"@toolbox.side_fx", // an underscore is not part of a name
		"@to-olbox.side",   // nor is a dash part of a namespace
		"@tool.box.side",   // a key is one namespace segment and one name, not a path
		"@",                // a bare at-sign
	} {
		prose, annotations := shareddocs.ParseAnnotations(line)

		assert.Empty(t, annotations, "%q was read as an annotation", line)
		assert.Equal(t, line, prose, "%q was not kept as prose", line)
	}

	// Digits are legal in both segments and a dash is legal in the name, so asserting one of those
	// parses keeps the rejections above from being satisfied by a parser that refuses everything it
	// did not see in this file.
	_, annotations := shareddocs.ParseAnnotations("@acme2.rate-limit-2 10")
	values, ok := annotations.Lookup("acme2.rate-limit-2")
	require.True(t, ok, "a key with digits and a dash was refused")
	assert.Equal(t, []string{"10"}, values)
}

// A sentence that merely mentions an at-sign must not be reinterpreted, and a line that is not an
// annotation at all changes nothing about the prose around it.
func TestProseThatMentionsAnAtSignIsUntouched(t *testing.T) {
	prose, annotations := shareddocs.ParseAnnotations(
		"Ask the maintainer at @someone before changing this.\n@toolbox.deprecated")

	require.Len(t, annotations, 1, "the annotation on the second line was lost")
	assert.Equal(t, "toolbox.deprecated", annotations[0].Key)
	assert.Equal(t, "Ask the maintainer at @someone before changing this.", prose,
		"a sentence mentioning an at-sign was altered or read as an annotation")
}

// Extracting an annotation leaves the blank lines that separated it behind. Collapsing them is what
// keeps the prose the shape the author wrote, and the case that matters is an annotation in the
// middle of a paragraph: the two halves must join with one blank line, not two and not none.
func TestCollapsingBlankRunsKeepsParagraphsApart(t *testing.T) {
	prose, annotations := shareddocs.ParseAnnotations(
		"First paragraph.\n\n@toolbox.side-effects read_only\n\n\nSecond paragraph.")

	require.Len(t, annotations, 1)
	assert.Equal(t, "First paragraph.\n\nSecond paragraph.", prose,
		"removing an annotation changed the paragraph structure of the prose")
}
