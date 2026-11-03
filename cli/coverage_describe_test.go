package cli_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reading a command line back is a grammar problem: a page is prose, a section
// header, a padded column, and a parenthetical trailer, and each of those has a
// spelling a tool is free to change. The tests below feed one page per spelling
// rather than one page per feature, because the defect each one catches is "this
// legal page was silently read as nothing".

// The package states its own claim in a machine-readable form so an index can ask
// whether this library reads or renders a command line without importing it. A
// descriptor that drifts from the constants is a claim no consumer can trust.
func TestThePackageDescribesItsOwnClaim(t *testing.T) {
	assert.Equal(t, []api.Format{cli.Format}, cli.Formats())
	assert.Equal(t, []string{cli.Target}, cli.Targets())
	assert.True(t, cli.HandlesFormat(cli.Format))
	assert.True(t, cli.HandlesFormat(" CLI "), "a claim is matched the way every other identifier is")
	assert.False(t, cli.HandlesFormat("openapi"))
	assert.True(t, cli.HandlesTarget(cli.Target))
	assert.False(t, cli.HandlesTarget("tree"))

	format := cli.FormatDescriptor()
	assert.Equal(t, string(cli.Format), format.ID)
	assert.NotEmpty(t, format.Name)
	assert.NotEmpty(t, format.Description)
	assert.Equal(t, []string{"text/plain"}, format.MediaTypes)

	target := cli.TargetDescriptor()
	assert.Equal(t, cli.Target, target.ID)
	assert.NotEmpty(t, target.Name)
	assert.NotEmpty(t, target.Description)
	assert.Equal(t, []string{"text/plain"}, target.MediaTypes)
}

// HelpText renders what DescribeHelp reads, so it refuses the one input it cannot
// render rather than returning an empty stream a caller would describe as an API
// with no commands.
func TestHelpTextWithoutATreeIsRefused(t *testing.T) {
	_, err := cli.HelpText(nil)
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
}

// A command whose help renders to nothing contributes nothing: skipping it is what
// keeps one empty page from being appended as a separator in the middle of the
// stream. A tree with no help template at all is the smallest such case.
func TestACommandThatRendersNoHelpIsSkipped(t *testing.T) {
	text, err := cli.HelpText(&cobra.Command{})
	require.NoError(t, err)
	assert.Equal(t, "", text)
}

// A tool's own page may open with sections before its usage, which happens when a
// template prints the flag block first. The page is still read, and the flags it
// stated before the usage are not lost.
func TestAPageThatOpensWithASectionIsStillRead(t *testing.T) {
	described, err := cli.DescribeHelp(`Flags:
      --first string   The first value.

Usage:
  early [flags]
`, cli.DescribeOptions{})
	require.NoError(t, err)
	require.Len(t, described.Operations(), 1)
	assert.Equal(t, "early", described.Operations()[0].Name)
}

// A section header may carry content on its own line, which is what a template
// that prints one section per line does. The content is the section's first entry,
// not a line of prose.
func TestASectionHeaderMayStateItsContentOnItsOwnLine(t *testing.T) {
	described, err := cli.DescribeHelp(`Aliases: late, latter

Usage:
  tool late [flags]
`, cli.DescribeOptions{})
	require.NoError(t, err)
	require.Len(t, described.Operations(), 1,
		"an alias is not modelled, and a page that states one is still its command's page")
}

// An alias block is a section this reader knows and does not model: the model has
// no field for an alias. Its entries are still entries, read through the one rule
// that decides whether an indented line starts a new one.
func TestAnAliasBlockIsReadAndNotModelled(t *testing.T) {
	described, err := cli.DescribeHelp(`tool late

Usage:
  tool late [flags]

Aliases:
  latter
  latecomer
`, cli.DescribeOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"tool/tool/late"}, described.OperationIDs())
}

// Cobra writes a blank line between examples, and an example is documentation the
// model has no field for. Consuming it rather than ending the section is what keeps
// the second example from being read as prose of the page that follows.
func TestBlankLinesInsideExamplesStayInsideTheExamples(t *testing.T) {
	described, err := cli.DescribeHelp(`tool example

Usage:
  tool example [flags]

Examples:
  tool example --a x

  tool example --b y
`, cli.DescribeOptions{})
	require.NoError(t, err)
	operation := described.Operations()[0]
	assert.Empty(t, operation.Parameters, "an example is not a flag the tool has")
}

// Prose may be indented before any section is stated, which is how this package's
// own generator prints a block of declared locations. Reading it as prose rather
// than dropping it is what keeps a page's description intact. A usage section may
// also state a second line with no command word — cobra prints "[command]" under
// the runnable form — and that line names nothing rather than renaming the command
// to the literal word "[command]".
func TestIndentedProseIsStillProse(t *testing.T) {
	described, err := cli.DescribeHelp(`A tool. It is documented.

  And here is an indented paragraph of the same description.

Usage:
  tool [flags]
  [command]

Available Commands:
  add   Adds a thing.

tool add

Adds a thing.

Usage:
  tool add [flags]
`, cli.DescribeOptions{})
	require.NoError(t, err)
	assert.Contains(t, described.Description, "indented paragraph")
	assert.Equal(t, []string{"tool/tool/add"}, described.OperationIDs())
}

// A flag block entry that is not a long flag is not a flag: pflag prints a
// shorthand on its own line only when a tool wrote the block by hand, and reading
// "-x" as a flag named "x" would describe an input with no name to type.
func TestAFlagEntryWithoutALongNameIsSkipped(t *testing.T) {
	described, err := cli.DescribeHelp(`tool short

Usage:
  tool short [flags]

Flags:
  -x   Only a shorthand.
      -y   Another shorthand.
`, cli.DescribeOptions{})
	require.NoError(t, err)
	assert.Empty(t, described.Operations()[0].Parameters,
		"a spec with no long name names no flag")
}

// A flag line whose name is empty states no flag: there is no word to type, and a
// flag registered under "" would be one cobra refuses to parse at all.
func TestAFlagWithNoNameIsNotAFlag(t *testing.T) {
	described, err := cli.DescribeHelp(`tool nameless

Usage:
  tool nameless [flags]

Flags:
      --   A flag with no name at all.
`, cli.DescribeOptions{})
	require.NoError(t, err)
	assert.Nil(t, described.Operations()[0].Request)
}

// A blurb printed inside a flag block is prose, not a flag: a tool that writes a
// sentence where the entries begin has stated no input there, and reading the
// sentence as one would describe a flag named "This".
func TestASentenceInsideAFlagBlockIsProse(t *testing.T) {
	described, err := cli.DescribeHelp(`tool chatty

Usage:
  tool chatty [flags]

Flags:
  The following flags are all optional.
      --real string   A real flag.
`, cli.DescribeOptions{})
	require.NoError(t, err)
	request := described.Operations()[0].Request
	require.NotNil(t, request)
	_, present := request.Property("real")
	assert.True(t, present)
	_, invented := request.Property("The")
	assert.False(t, invented)
}

// A flag whose spec carries an option value — "--count=5" — is named "count": the
// value is what a bare occurrence means, not part of the name. Reading the whole
// word would describe a flag nobody can type.
func TestAnOptionValueIsNotPartOfAFlagName(t *testing.T) {
	described, err := cli.DescribeHelp(`tool counted

Usage:
  tool counted [flags]

Flags:
      --count=5   How many.
`, cli.DescribeOptions{})
	require.NoError(t, err)
	request := described.Operations()[0].Request
	require.NotNil(t, request)
	_, present := request.Property("count")
	assert.True(t, present)
}

// A flag printed with no description is still a flag. pflag prints one when a
// description is empty, and the column the reader learns from is then absent — the
// entry has to be read from its spec alone rather than dropped.
func TestAFlagWithNoDescriptionIsStillAFlag(t *testing.T) {
	described, err := cli.DescribeHelp(`tool bare

Usage:
  tool bare [flags]

Flags:
      --lonely
      --named string   A documented flag.
`, cli.DescribeOptions{})
	require.NoError(t, err)
	request := described.Operations()[0].Request
	require.NotNil(t, request)
	_, present := request.Property("lonely")
	assert.True(t, present)
}

// An identifier is only an identifier when every segment names something. A page
// whose first block is "a//b" states no name, so the command word stands in — and
// the description still has an operation.
func TestAnIdentifierWithAnEmptySegmentNamesNothing(t *testing.T) {
	described, err := cli.DescribeHelp(`a//b

A page whose identifier states no name.

Usage:
  tool broken [flags]
`, cli.DescribeOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"tool/tool/broken"}, described.OperationIDs())
}

// A wire line is a method and at most one path. A paragraph of three words is a
// sentence, and reading its first word as a method would invent a transport the
// page never stated. A one-word heading is the same mistake in a smaller shape: it
// is only a method where a method is expected, which is right after an identifier.
func TestAParagraphThatIsNotAWireLineStaysProse(t *testing.T) {
	described, err := cli.DescribeHelp(`RESPONSES

A sentence that begins with a heading.

Usage:
  tool sentence [flags]
`, cli.DescribeOptions{})
	require.NoError(t, err)
	operation := described.Operations()[0]
	assert.Empty(t, operation.Method)
	assert.Contains(t, operation.Description, "A sentence")
	assert.Contains(t, operation.Description, "RESPONSES",
		"a heading is prose unless it follows an identifier")
}

// A two-segment identifier on an operation's page names the method in the
// description's own spelling, which is the one thing a command word cannot state.
// Without it the caller's spelling of a camel-cased name is lost.
func TestATwoSegmentIdentifierNamesAnOperation(t *testing.T) {
	described, err := cli.DescribeHelp(`tool/add

Adds a thing.

Usage:
  tool add [flags]
`, cli.DescribeOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"tool/tool/add"}, described.OperationIDs())
}

// A group's page states the side effects its operations declare, and the
// identifier it opens with is not prose. The effect list survives the round trip
// through the page, which is what lets a description read from text warn a caller
// before choosing an operation.
func TestAGroupPageStatesItsSideEffects(t *testing.T) {
	described, err := cli.DescribeHelp(`A tool that stores pets.

Usage:
  tool [command]

Available Commands:
  pets   Operations on pets.

Flags:
  -h, --help   help for tool

tool/pets

Declared side effects: create, delete.

Operations on pets.

Usage:
  tool pets [command]

Available Commands:
  add   Adds a pet.

Flags:
  -h, --help   help for pets

tool/pets/add

Adds a pet.

Usage:
  tool pets add [flags]

Flags:
      --name string   What to call the pet.
`, cli.DescribeOptions{})
	require.NoError(t, err)
	require.Len(t, described.Services, 1)
	assert.Equal(t, []api.SideEffect{"create", "delete"}, described.Services[0].SideEffects)
	assert.Equal(t, "Operations on pets.", described.Services[0].Description)
	assert.Equal(t, []string{"tool/pets/add"}, described.OperationIDs(),
		"a group's own run is not an operation, and its children are")
}

// A group title is short and capitalised word by word. A sentence that ends in a
// colon is prose, and reading it as a command list would turn the sentence that
// follows it into commands.
func TestASentenceEndingInAColonIsNotAGroupTitle(t *testing.T) {
	described, err := cli.DescribeHelp(`A tool.

The following list of things is not a command list at all:

Usage:
  tool run [flags]
`, cli.DescribeOptions{})
	require.NoError(t, err)
	operation := described.Operations()[0]
	assert.Contains(t, operation.Description, "not a command list")
	assert.Nil(t, operation.Request, "no command was invented from the sentence")
}

// A page that advertises only groups describes no operation, and a description
// with no operation is not useful: it says what can be typed without saying what
// any of it does. Refusing it names the count of commands it could not read.
func TestAPageOfGroupsOnlyDescribesNoOperation(t *testing.T) {
	_, err := cli.DescribeHelp(`A tool.

Usage:
  tool [command]

Available Commands:
  sub   A group.

tool sub

A group.

Usage:
  tool sub [command]

Available Commands:
  deeper   A deeper group.
`, cli.DescribeOptions{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "describes no operation")
}

// Two pages can name one operation — a tool that documents the same command twice
// in one capture. Neither page may be dropped, and the second is numbered rather
// than replacing the first, because a description that silently lost a page is
// wrong about the surface.
func TestTwoPagesNamingOneOperationKeepBoth(t *testing.T) {
	described, err := cli.DescribeHelp(`tool/tool/op

One.

Usage:
  tool one [flags]

Flags:
      --a string   A.

tool/tool/op

Two.

Usage:
  tool two [flags]

Flags:
      --b string   B.
`, cli.DescribeOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"tool/tool/op", "tool/tool/op-2"}, described.OperationIDs())
}

// A flag whose name is exactly a location prefix keeps its own name: trimming the
// prefix would leave nothing to type, and a parameter with no name is not an input
// a caller can supply.
func TestAFlagNamedLikeALocationPrefixKeepsItsName(t *testing.T) {
	described, err := cli.DescribeHelp(`tool prefixed

Usage:
  tool prefixed [flags]

Flags:
      --query.   A flag named after its location. (query)
`, cli.DescribeOptions{})
	require.NoError(t, err)
	operation := described.Operations()[0]
	require.Len(t, operation.Parameters, 1)
	assert.Equal(t, "query.", operation.Parameters[0].Name)
	assert.Equal(t, api.ParameterInQuery, operation.Parameters[0].In)
}

// A nested object is stated twice on a page — once as the document, once as each
// member — and the object's own prose is the only place its comment survives. An
// object read with members must keep that prose rather than becoming an anonymous
// object.
func TestAnObjectKeepsItsOwnProseWhenItsMembersAreFlags(t *testing.T) {
	described, err := cli.DescribeHelp(`tool nested

Usage:
  tool nested [flags]

Flags:
      --prompt string      The introduction to use.
      --prompt.id string   Identifier of the introduction.
`, cli.DescribeOptions{})
	require.NoError(t, err)
	request := described.Operations()[0].Request
	require.NotNil(t, request)
	prompt, present := request.Property("prompt")
	require.True(t, present)
	assert.Equal(t, "The introduction to use.", prompt.Schema.Description,
		"the document's own comment is the object's description")
	_, nested := prompt.Schema.Property("id")
	assert.True(t, nested, "and its members are the members the dotted flags stated")
}

// Every word a page can print in the type column is a statement about the value,
// and each one has to come back as the shape it names: a map flag read as text, or
// an array read as one string, describes a value the tool cannot accept.
func TestEveryTypeWordAPageCanPrintIsRead(t *testing.T) {
	const page = `tool words

Reads words.

Usage:
  tool words [flags]

Flags:
      --aBool bool                A boolean.
      --aCount count              A counter.
      --anInt int                 An integer.
      --anInt8 int8               Eight bits.
      --anInt16 int16             Sixteen bits.
      --anInt32 int32             Thirty-two bits.
      --anInt64 int64             Sixty-four bits.
      --aUint uint                An unsigned integer.
      --aUint8 uint8              Eight unsigned bits.
      --aUint64 uint64            Sixty-four unsigned bits.
      --aNumber number            The number word.
      --aBoolean boolean          The boolean word.
      --aFloat float              A float.
      --aFloat64 float64          A float of sixty-four bits.
      --aFloat32 float32          A float of thirty-two bits.
      --aDuration duration        A duration.
      --anArray stringArray       Strings.
      --ownWords string           Written in this package's own words.
      --anotherArray strings      More strings.
      --ints ints                 Integers.
      --anIntSlice intSlice       Integers again.
      --int32s int32Slice         Sixteen or thirty-two bits.
      --int64s int64Slice         Sixty-four bits.
      --uints uints               Unsigned integers.
      --uintSlice uintSlice       More unsigned integers.
      --bools bools               Booleans.
      --boolSlice boolSlice       More booleans.
      --floats floats             Numbers.
      --float32s float32Slice     Numbers of thirty-two bits.
      --float64s float64Slice     Numbers of sixty-four bits.
      --aMap stringToString       A map of text.
      --anIntMap stringToInt      A map of integers.
      --aBigMap stringToInt64     A map of wide integers.
      --hex bytesHex              Bytes as hex.
      --base64 bytesBase64        Bytes as base64.
      --anIP ip                   An address.
      --aMask ipMask              A mask.
      --aNet ipNet                A network.
      --mystery mysteryWord       A word this reader does not know.
      --listed string             list of strings
`
	described, err := cli.DescribeHelp(page, cli.DescribeOptions{})
	require.NoError(t, err)
	request := described.Operations()[0].Request
	require.NotNil(t, request)

	want := map[string]api.SchemaType{
		"aBool": api.TypeBoolean, "aCount": api.TypeInteger, "anInt": api.TypeInteger,
		"anInt8": api.TypeInteger, "anInt64": api.TypeInteger, "aUint64": api.TypeInteger,
		"aFloat": api.TypeNumber, "aFloat64": api.TypeNumber, "aFloat32": api.TypeNumber,
		"aNumber": api.TypeNumber, "aBoolean": api.TypeBoolean, "listed": api.TypeArray,
		"aDuration": api.TypeString, "anArray": api.TypeArray, "anotherArray": api.TypeArray,
		"ints": api.TypeArray, "anIntSlice": api.TypeArray, "uints": api.TypeArray,
		"bools": api.TypeArray, "floats": api.TypeArray, "aMap": api.TypeObject,
		"anIntMap": api.TypeObject, "hex": api.TypeString, "anIP": api.TypeString,
	}
	for name, kind := range want {
		property, present := request.Property(name)
		require.True(t, present, "--%s is a flag the page printed", name)
		assert.Equal(t, kind, property.Schema.Type, "--%s is the shape its type word names", name)
	}

	formats := map[string]string{
		"anInt8": "int8", "anInt16": "int16", "anInt32": "int32", "anInt64": "int64",
		"aUint8": "uint8", "aUint64": "uint64", "aFloat32": "float32", "aDuration": "duration",
		"hex": "bytesHex", "base64": "bytesBase64", "anIP": "ip", "aMask": "ipMask", "aNet": "ipNet",
	}
	for name, format := range formats {
		property, _ := request.Property(name)
		assert.Equal(t, format, property.Schema.Format, "--%s keeps the format the page stated", name)
	}

	mystery, _ := request.Property("mystery")
	assert.Equal(t, "mysteryWord", mystery.Schema.Format,
		"a type this reader cannot classify is kept as a format rather than guessed at")
	assert.Empty(t, mystery.Schema.Type)

	// A wide unsigned list is the one array whose element format a page can state.
	uints, _ := request.Property("uintSlice")
	require.NotNil(t, uints.Schema.Items)
	assert.Equal(t, "uint64", uints.Schema.Items.Format)
}

// A value's shape may also be stated in the description's own words, which is
// where an array's element type survives: pflag prints every repeated value as
// stringArray, so the words are the only statement that an element is a number.
func TestAValueShapeStatedInWordsWinsOverTheTypeColumn(t *testing.T) {
	described, err := cli.DescribeHelp(`tool words

Usage:
  tool words [flags]

Flags:
      --anObject string   JSON object
      --anArray string    JSON array
      --aValue string     JSON value
      --bools string      list of booleans
      --numbers string    list of numbers
      --repeat string     A value given per occurrence. (repeatable)
      --choices stringArray   The choices. (one of: a, b)
`, cli.DescribeOptions{})
	require.NoError(t, err)
	request := described.Operations()[0].Request
	require.NotNil(t, request)

	object, _ := request.Property("anObject")
	assert.Equal(t, api.TypeObject, object.Schema.Type)
	array, _ := request.Property("anArray")
	assert.Equal(t, api.TypeArray, array.Schema.Type)
	value, _ := request.Property("aValue")
	assert.Empty(t, value.Schema.Type, "a value of several alternatives states no single type")
	bools, _ := request.Property("bools")
	assert.Equal(t, api.TypeArray, bools.Schema.Type)
	assert.Equal(t, api.TypeBoolean, bools.Schema.Items.Type)
	numbers, _ := request.Property("numbers")
	assert.Equal(t, api.TypeNumber, numbers.Schema.Items.Type)

	repeat, _ := request.Property("repeat")
	assert.Equal(t, api.TypeArray, repeat.Schema.Type,
		"a value stated as repeatable is a list even when the type column says string")

	choices, _ := request.Property("choices")
	require.NotNil(t, choices.Schema.Items)
	assert.Equal(t, []string{"a", "b"}, choices.Schema.Items.Enum,
		"an enumeration belongs to the element of a repeated value")
}

// A default is printed the way pflag prints one, so a reader that does not undo
// that printing states a default of the wrong type — a quoted string default read
// as a word, or a list read as one bracketed string.
func TestADefaultIsReadInEveryShapeAPagePrints(t *testing.T) {
	described, err := cli.DescribeHelp(`tool defaults

Usage:
  tool defaults [flags]

Flags:
      --b bool            Whether. (default true)
      --i int             A count. (default 42)
      --f float64         A ratio. (default 1.5)
      --s string          A word. (default foo)
      --q string          A quoted word. (default "foo")
      --l stringArray     A list. (default [a,b])
      --empty string      Nothing. (default "")
`, cli.DescribeOptions{})
	require.NoError(t, err)
	request := described.Operations()[0].Request
	require.NotNil(t, request)

	for name, want := range map[string]any{
		"b":     true,
		"i":     int64(42),
		"f":     1.5,
		"s":     "foo",
		"q":     "foo",
		"l":     []any{"a", "b"},
		"empty": "",
	} {
		property, present := request.Property(name)
		require.True(t, present, "--%s is a flag the page printed", name)
		assert.Equal(t, want, property.Schema.Default, "--%s carries the printed default", name)
	}
}

// A deprecation printed by pflag is a fact about the value, and it belongs on the
// value's schema: there is no member object to carry it, and dropping it would
// describe a flag the tool says should not be used without saying so.
func TestADeprecationPrintedByPflagIsRead(t *testing.T) {
	described, err := cli.DescribeHelp(`tool old

Usage:
  tool old [flags]

Flags:
      --legacy string   An old flag. (DEPRECATED: use --new)
`, cli.DescribeOptions{})
	require.NoError(t, err)
	property, present := described.Operations()[0].Request.Property("legacy")
	require.True(t, present)
	assert.True(t, property.Schema.Deprecated)
}

// A declared location block is prose of the tool's own, printed one location per
// line and separated from its note by an em dash. Both halves are read, because a
// location with no note is still the answer to "where does a call go".
func TestDeclaredServersAreReadWithTheirNotes(t *testing.T) {
	described, err := cli.DescribeHelp(`A tool.

Declared servers:
  https://a.example.com — The live one.

Usage:
  tool [flags]
  [command]

Available Commands:
  add   Adds a thing.

tool add

Adds a thing.

Usage:
  tool add [flags]
`, cli.DescribeOptions{})
	require.NoError(t, err)
	require.Len(t, described.DeclaredServers, 1)
	assert.Equal(t, "https://a.example.com", described.DeclaredServers[0].URL)
	assert.Equal(t, "The live one.", described.DeclaredServers[0].Description)
}

// A root command that is runnable and has no children is a tool of one command, so
// its page is the operation's page. A root that states no name at all is the other
// extreme: there is no word to type and no operation name to give, so the
// description is refused rather than published with an unnamed operation.
func TestATreeWithNoCommandWordIsRefused(t *testing.T) {
	root := &cobra.Command{Use: "", RunE: func(*cobra.Command, []string) error { return nil }}

	_, err := cli.Describe(root, cli.DescribeOptions{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "unnamed operation")
}

// What a tool hides is not offered, so it is not described: a hidden command and a
// hidden flag are both invisible to the caller a description is written for.
func TestAHiddenCommandAndAHiddenFlagAreNotDescribed(t *testing.T) {
	root := &cobra.Command{Use: "tool", RunE: func(*cobra.Command, []string) error { return nil }}
	runnable := &cobra.Command{Use: "run", Short: "Runs.", Run: func(*cobra.Command, []string) {}}
	runnable.Flags().String("shown", "", "A visible flag.")
	runnable.Flags().String("secret", "", "A hidden flag.")
	require.NoError(t, runnable.Flags().MarkHidden("secret"))
	hidden := &cobra.Command{Use: "shadow", Short: "Not offered.", Run: func(*cobra.Command, []string) {}}
	hidden.Hidden = true
	// A command that neither runs nor groups anything is not available: the tool
	// does not offer it, so a description of the tool does not claim it.
	inert := &cobra.Command{Use: "inert", Short: "Does nothing."}
	root.AddCommand(runnable, hidden, inert)

	described, err := cli.Describe(root, cli.DescribeOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"tool/tool/run"}, described.OperationIDs(),
		"a hidden command is not part of the surface the tool advertises")
	request := described.Operations()[0].Request
	require.NotNil(t, request)
	_, present := request.Property("secret")
	assert.False(t, present, "a hidden flag is not an input either")
}

// A flag's default is printed only when it is not the zero value, and a tree read
// back has to ask the same question a page's printer asks: a boolean that defaults
// to false and a duration that defaults to zero state no default, while the same
// flag with another value states one.
func TestAZeroDefaultIsNotADeclaredDefault(t *testing.T) {
	root := &cobra.Command{Use: "tool", RunE: func(*cobra.Command, []string) error { return nil }}
	runnable := &cobra.Command{Use: "run", Short: "Runs.", Run: func(*cobra.Command, []string) {}}
	runnable.Flags().Bool("verbose", false, "Prints more.")
	runnable.Flags().Bool("quiet", true, "Prints less.")
	runnable.Flags().Duration("wait", 0, "How long to wait.")
	runnable.Flags().Duration("pause", 5*time.Second, "How long to pause.")
	root.AddCommand(runnable)

	described, err := cli.Describe(root, cli.DescribeOptions{})
	require.NoError(t, err)
	request := described.Operations()[0].Request
	require.NotNil(t, request)

	verbose, _ := request.Property("verbose")
	assert.Nil(t, verbose.Schema.Default, "the zero value is not a declared default")
	quiet, _ := request.Property("quiet")
	assert.Equal(t, true, quiet.Schema.Default)
	wait, _ := request.Property("wait")
	assert.Nil(t, wait.Schema.Default)
	pause, _ := request.Property("pause")
	assert.Equal(t, "5s", pause.Schema.Default,
		"a duration default is read as the text the page prints")
}

// A deprecated flag on a tree is stated on the value it describes, because the
// tree knows what a page only prints, and a caller reading --legacy's help has to
// be told not to use it.
func TestADeprecatedFlagOfATreeIsRead(t *testing.T) {
	root := &cobra.Command{Use: "tool", RunE: func(*cobra.Command, []string) error { return nil }}
	runnable := &cobra.Command{Use: "run", Short: "Runs.", Run: func(*cobra.Command, []string) {}}
	runnable.Flags().String("legacy", "", "An old flag.")
	// MarkDeprecated also hides the flag, and a hidden flag is not described at
	// all. A tool that states a deprecation and keeps the flag listed is the one
	// case this branch exists for: the tree knows what the page would print.
	runnable.Flags().Lookup("legacy").Deprecated = "use --new"
	root.AddCommand(runnable)

	described, err := cli.Describe(root, cli.DescribeOptions{})
	require.NoError(t, err)
	property, present := described.Operations()[0].Request.Property("legacy")
	require.True(t, present)
	assert.True(t, property.Schema.Deprecated)
}

// A binary is asked for its help, and the walk is bounded by the caller's context
// as well as by its own bounds. A nil context is the zero case — a caller who did
// not care — and it must not be dereferenced.
func TestABinaryIsDescribedWithNoContext(t *testing.T) {
	path, _ := fakeTool(t, map[string]string{"": "A tool.\n\nUsage:\n  tool run [flags]\n"})

	//nolint:staticcheck // a nil context is the documented zero value here.
	described, err := cli.DescribeBinary(nil, path, cli.DescribeOptions{})
	require.NoError(t, err)
	assert.Len(t, described.Operations(), 1)
}

// A cancelled context stops the walk before the first page is read, and it is
// reported as the walk failing rather than as a tool with no help: the difference
// is whether the caller should retry.
func TestACancelledContextStopsTheWalkBeforeItStarts(t *testing.T) {
	path, invocations := fakeTool(t, map[string]string{"": binaryRootPage, "add": binaryAddPage})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := cli.DescribeBinary(ctx, path, cli.DescribeOptions{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindUnavailable))
	assert.Contains(t, err.Error(), "describe")
	assert.Empty(t, invocations(), "no page is read once the caller has given up")
}

// covCancelAfter reports a cancellation once it has been asked a set number of
// times, so a test can cancel the walk between the root page and its children —
// the one moment a deadline can land in the middle of a tree rather than before it.
type covCancelAfter struct {
	context.Context
	remaining atomic.Int32
}

func (c *covCancelAfter) Err() error {
	if c.remaining.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

// A cancellation that arrives after the root page stops the walk there and is
// reported, rather than being ignored until the next call: a page already read is
// not a description, and returning the root alone would publish a partial one as
// whole.
func TestACancellationMidWalkStopsTheWalk(t *testing.T) {
	path, _ := fakeTool(t, map[string]string{"": binaryRootPage, "add": binaryAddPage})
	ctx := &covCancelAfter{Context: context.Background()}
	ctx.remaining.Store(1)

	_, err := cli.DescribeBinary(ctx, path, cli.DescribeOptions{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindUnavailable))
	assert.Contains(t, err.Error(), "describe")
}

// A walk that read only groups describes no operation, and the refusal is this
// package's, not the tool's: the pages were read, and they state nothing a caller
// can run. Reporting it as a description error is what keeps a downstream catalog
// from publishing an API with no operations.
func TestABinaryOfGroupsOnlyDescribesNoOperation(t *testing.T) {
	path, _ := fakeTool(t, map[string]string{
		"":    "A tool.\n\nUsage:\n  tool sub [command]\n\nAvailable Commands:\n  sub   A group.\n",
		"sub": "A group.\n\nUsage:\n  tool sub [command]\n\nAvailable Commands:\n  deeper   A deeper group.\n",
	})

	// The depth bound keeps the walk from descending into the deeper group, so the
	// pages read are the root and one group — a complete walk of a tool that offers
	// nothing to run.
	_, err := cli.DescribeBinary(context.Background(), path, cli.DescribeOptions{MaxDepth: 1})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
	assert.Contains(t, err.Error(), "describes no operation")
}
