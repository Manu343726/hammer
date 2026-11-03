package cli

import (
	"context"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Some branches below belong to a helper's own contract rather than to a state the
// generator can produce: a ladder asked about a service the generator requires a
// name for, a walker asked to read no page at all, a block reader handed a block
// with a blank line in it. They are held here, one call each, because the exported
// surface cannot produce the input — and a branch left untested because no caller
// reaches it is a branch nobody has ever seen run.

// The root's name is what a caller types, and a caller who states nothing still
// needs a word: a description with no name and no identifier falls back to "api"
// rather than to an empty command name, which cobra would refuse.
func TestTheRootCommandNameHasAWireWordFallback(t *testing.T) {
	assert.Equal(t, "api", commandName(api.API{}, Options{}))
	assert.Equal(t, "chosen", commandName(api.API{}, Options{CommandName: " chosen "}))
	assert.Equal(t, "pets", commandName(api.API{Name: "Pets"}, Options{}))
	assert.Equal(t, "v1-pets", commandName(api.API{ID: "v1.Pets"}, Options{}))
}

// The walk checks its command bound before it reads anything, because a bound of
// zero is a caller asking for no pages at all. DescribeBinary clamps its option to
// at least one page, so no caller of this package reaches the guard today; it is
// what makes the bound a bound rather than an assumption, and reading one page
// before reporting it would be one page over the budget the walker was given.
func TestTheWalkReadsNothingWhenItsCommandBoundIsAlreadyReached(t *testing.T) {
	walker := &helpWalker{path: "tool", maxCommands: 0, maxDepth: DefaultMaxDepth}

	pages, err := walker.walk(context.Background(), nil, 0)
	require.NoError(t, err)
	assert.Nil(t, pages)
	assert.Contains(t, walker.truncated, "command bound",
		"the walk states which bound stopped it")
}

// assemble is the single translation both readers share, and both of them guarantee
// a page before calling it. The guard states that guarantee rather than assuming it:
// a translation of no pages would otherwise produce an API with no services, which
// Normalize would refuse with a message about the wrong thing.
func TestThereIsNothingToAssembleFromNoPages(t *testing.T) {
	_, err := assemble(nil, DescribeOptions{}, "")
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "no command to describe")
}

// A value is the first one that states something, and a helper whose whole job is
// picking one has to answer for the case where none does rather than panicking on
// the empty slice.
func TestTheFirstNonEmptyValueMayBeNothing(t *testing.T) {
	assert.Equal(t, "one", firstNonEmpty("  ", " one ", "two"))
	assert.Equal(t, "", firstNonEmpty())
	assert.Equal(t, "", firstNonEmpty("", "   ", "\t"))
}

// A member with no flag of its own and no members of its own states no shape, and
// guessing one would put a type in the description that neither the tree nor the
// page stated. The empty schema is the honest answer.
func TestAMemberNodeWithNoShapeStatesNoShape(t *testing.T) {
	node := &memberNode{name: "mystery", description: "A value nothing was said about."}

	schema := node.schemaOf()
	require.NotNil(t, schema)
	assert.Empty(t, schema.Type)
	assert.Empty(t, schema.Properties)
	assert.Equal(t, "A value nothing was said about.", schema.Description)
}

// jsonWord words a decoded value for an error message, and the decoder produces a
// closed set of types. The default is the helper's total-function promise: a caller
// that hands it something else still gets a word rather than an empty string in the
// middle of a sentence.
func TestAWordForAValueTheDecoderNeverProduces(t *testing.T) {
	assert.Equal(t, "a value", jsonWord(struct{}{}))
	assert.Equal(t, "an object", jsonWord(map[string]any{}))
	assert.Equal(t, "an array", jsonWord([]any{}))
}

// A flag block entry with no lines states no flag: the parser is handed one slice
// per entry and has to answer for the empty one, which is what an entry that a
// future layout split leaves behind would be.
func TestAFlagItemWithNoLinesIsNotAFlag(t *testing.T) {
	flag, ok := parseFlagItem(nil)
	assert.False(t, ok)
	assert.Empty(t, flag.name)
}

// A command list entry with no words states no command: a blank line inside a
// command section is whitespace, not a command named "". Reading it as one would
// put an untypeable command in the description.
func TestACommandItemWithNoWordsIsNotACommand(t *testing.T) {
	for _, item := range [][]string{nil, {"   "}, {"\t"}} {
		command, ok := parseCommandItem(item)
		assert.False(t, ok, "%q states no command", item)
		assert.Empty(t, command.name)
	}
}

// A page that already carries a summary keeps it: the parent's command list is
// where a summary comes from, and a page read from a tree states its own. Replacing
// it would make a tree's summary depend on whether a parent happened to list it.
func TestASummaryAPageAlreadyStatesIsNotReplaced(t *testing.T) {
	pages := []commandPage{
		{path: []string{"tool"}, summary: "The tool's own summary."},
		{path: []string{"tool", "add"}},
	}

	attachSummaries(pages)
	assert.Equal(t, "The tool's own summary.", pages[0].summary)
	assert.Empty(t, pages[1].summary, "nothing listed the child, so it has no summary")
}

// A default is read from the text a page printed, and an empty text states no
// default at all. Answering nil there is what keeps "no default" from becoming the
// empty string, which is a value a caller could not tell from a stated one.
func TestAnEmptyDefaultStatesNothing(t *testing.T) {
	assert.Nil(t, defaultOf(api.StringSchema(), ""))
	assert.Nil(t, defaultOf(api.StringSchema(), "   "))
	assert.Equal(t, "set", defaultOf(api.StringSchema(), "set"))
}

// A declared servers block is one paragraph in a page, so the blank lines it
// contains are a hand-written page's layout rather than a second block. Skipping
// them keeps the location after one from being read as a URL of " ".
func TestABlankLineInsideAServerBlockIsSkipped(t *testing.T) {
	servers, ok := declaredServers("Declared servers:\n  https://a\n\n  https://b — The second.")
	require.True(t, ok)
	require.Len(t, servers, 2)
	assert.Equal(t, "https://a", servers[0].URL)
	assert.Equal(t, "https://b", servers[1].URL)
	assert.Equal(t, "The second.", servers[1].Description)
}

// A member path is read one member at a time, and the empty path is the one path
// that names no member: it reports "not there" rather than returning the whole
// document as if it were the member called "".
func TestAnEmptyMemberPathNamesNoMember(t *testing.T) {
	value, present := memberAt(map[string]any{"a": 1}, nil)
	assert.False(t, present)
	assert.Nil(t, value)
}

// configValues renders whatever a source resolved as the text a flag parses. A key
// nothing states resolves to nothing, and a list stated as []string is one value
// per element — the shape a viper source can hand over without a document.
func TestConfigValuesRendersEveryShapeASourceCanState(t *testing.T) {
	settings := viper.New()
	values, err := configValues(settings, "absent")
	require.NoError(t, err)
	assert.Nil(t, values, "a key no source states is no value")

	settings.Set("tags", []string{"one", "two"})
	values, err = configValues(settings, "tags")
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, values)
}

// A service's command is named from its identifier when the description gave it no
// name, which is the state a provider that only carries stable identifiers
// produces. The generator annotates names before naming commands, so the ladder is
// held here for the shape it documents.
func TestAServiceIsNamedFromItsIdentifierWhenItHasNoName(t *testing.T) {
	ladder := serviceLadder(api.API{}, api.Service{ID: "v1.ServerReflection"})
	assert.Equal(t, []string{"server-reflection", "v1.server-reflection", "v1-server-reflection"}, ladder)
}

// An operation's command is named from its identifier for the same reason, and the
// identifier is the last rung because it is the one thing a description guarantees
// unique.
func TestAnOperationIsNamedFromItsIdentifierWhenItHasNoName(t *testing.T) {
	ladder := operationLadder(api.Service{}, api.Operation{ID: "v1.GetPet"})
	assert.Equal(t, []string{"get-pet", "v1-get-pet"}, ladder)
}

// A service that states neither a name nor an identifier cannot be given a level of
// its own: there is no word to name it by, and a command named "" is one cobra
// refuses. The service then contributes its operations at the root.
func TestAServiceWithNoWordIsNotALevel(t *testing.T) {
	assert.False(t, redundantService("tool", api.Service{}, nil))
}

// An API with no name and no identifier states no qualifier, so a service name that
// needs one gets the identifier rung instead. The empty answer is what keeps "."
// from becoming a rung of a ladder.
func TestAnAPIWithNoWordStatesNoQualifier(t *testing.T) {
	assert.Equal(t, "", apiWord(api.API{}))
	assert.Equal(t, "pets", apiWord(api.API{Name: "Pets"}))
}

// An identifier that states no word renders as no command word, so a ladder that has
// nothing else left falls through to its fallback rather than to a rung named "".
func TestAnEmptyIdentifierRendersNoWord(t *testing.T) {
	assert.Equal(t, "", dottedIdentifier(""))
	assert.Equal(t, "", dottedIdentifier("   "))
	assert.Equal(t, "v1.pets.get-pet", dottedIdentifier("v1/Pets/getPet"))
}

// distinctWords drops a rung that says what an earlier one said, and it answers for
// a ladder whose every rung was empty: the namer still needs one word to offer, and
// the fallback is what keeps a command from being named "".
func TestALadderWithNoDistinctWordFallsBack(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, distinctWords([]string{"a", "a", "", "b"}, "fallback"))
	assert.Equal(t, []string{"fallback"}, distinctWords([]string{"", ""}, "fallback"))
}

// A command path is what a page states, and the two helpers that read one answer for
// a page that states nothing rather than indexing into an empty slice.
func TestAPageWithNoPathStatesNoWord(t *testing.T) {
	assert.Equal(t, "", programWord(commandPage{}))
	assert.Equal(t, "", lastPathWord(nil))
	assert.Equal(t, "tool", programWord(commandPage{path: []string{"tool", "add"}}))
	assert.Equal(t, "add", lastPathWord([]string{"tool", "add"}))
}
