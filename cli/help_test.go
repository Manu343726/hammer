package cli_test

import (
	"strings"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A help page is the only document a binary publishes, so these tests are about
// reading what cobra actually prints rather than what it ought to print. Each
// fixture below is a page taken from a real generator, including the parts that
// are the layout's rather than the description's.

const (
	// leafPage is one operation's page, as this package's generator writes it:
	// the fully-qualified identifier, the wire line, the prose, and the declared
	// side effects, then the flag block.
	leafPage = `petstore/pets/createPet

POST /pets

Creates a pet in the store.

Declared side effects: create.

Usage:
  petstore pets create-pet [flags]

Flags:
      --body string              The whole request value. (JSON object)
      --count int                How many to create.
      --friends string           JSON array
      --name string              What to call the pet. (required)
      --prompt string            JSON object
      --prompt.id string         Identifier of the introduction.
      --prompt.template string   string
      --sizes stringArray        list of integers (repeatable)
      --status string            Where the pet is in its life. (one of: available, sold)
      --tag stringArray          Labels the pet carries. (repeatable)

Global Flags:
      --server string   Base URL to call, when the description declares none or another one is
                        wanted. (default "https://pets.example.com")

Use "petstore pets [command] --help" for more information about a command.
`

	// groupPage is one service's page: an identifier of two segments, its prose,
	// and a command list.
	groupPage = `petstore/pets

Operations on pets.

Usage:
  petstore pets [flags]
  petstore pets [command]

Available Commands:
  create-pet  Creates a pet.
  get-pet     Reads a pet, and the summary beside it is long enough that cobra
              wraps it under its own column.
  list-pets   Lists every pet.

Flags:
  -h, --help   help for pets

Global Flags:
      --server string   Base URL to call.
`

	// rootPage is the program's page: prose, the declared locations, where the
	// description came from, and the command list.
	rootPage = `A small pet store. It exists to be described, not to be called.

Declared servers:
  https://pets.example.com — The live store.

Described from openapi.

Usage:
  petstore [flags]
  petstore [command]

Available Commands:
  pets    Operations on pets.
  users   Operations on users.

Flags:
      --server string   Base URL to call, when the description declares none or another one is
                        wanted. (default "https://pets.example.com")

Use "petstore [command] --help" for more information about a command.
`
)

// single is the one operation a one-page description carries.
func single(t *testing.T, described api.API) api.Operation {
	t.Helper()
	operations := described.Operations()
	require.Len(t, operations, 1, "a one-page description carries one operation")
	return operations[0]
}

// A page's sections are read in any order and a page boundary is the prose that
// follows a page which has already stated its usage, because a tree's help is a
// stream of pages rather than a document with a table of contents.
func TestSeveralPagesAreReadFromOneStream(t *testing.T) {
	text := strings.Join([]string{rootPage, groupPage, leafPage}, "\n")

	described, err := cli.DescribeHelp(text, cli.DescribeOptions{})
	require.NoError(t, err)

	assert.Equal(t, "petstore", described.ID)
	assert.Equal(t, "A small pet store. It exists to be described, not to be called.", described.Description)
	require.Len(t, described.DeclaredServers, 1)
	assert.Equal(t, "https://pets.example.com", described.DeclaredServers[0].URL)
	assert.Equal(t, "The live store.", described.DeclaredServers[0].Description)
	assert.Equal(t, "openapi", described.Metadata[cli.MetadataOrigin],
		"where a tool says its own description came from is metadata, not this description's format")
	assert.Equal(t, cli.Format, described.Format)
}

// The flag column is pflag's: a shorthand, the long name, a type word, then two or
// more spaces. A description that wrapped is one sentence laid out for a terminal,
// so its lines are joined rather than read as separate flags.
func TestTheFlagColumnIsRead(t *testing.T) {
	described, err := cli.DescribeHelp(leafPage, cli.DescribeOptions{})
	require.NoError(t, err)
	operation := single(t, described)

	request := operation.Request
	require.NotNil(t, request)
	for _, name := range []string{"count", "friends", "name", "prompt", "sizes", "status", "tag"} {
		_, present := request.Property(name)
		assert.True(t, present, "the page states --%s, so the request has %q", name, name)
	}
	count, _ := request.Property("count")
	assert.Equal(t, api.TypeInteger, count.Schema.Type)
	assert.Equal(t, "How many to create.", count.Schema.Description)

	sizes, _ := request.Property("sizes")
	require.Equal(t, api.TypeArray, sizes.Schema.Type)
	assert.Equal(t, api.TypeInteger, sizes.Schema.Items.Type,
		"pflag prints every repeated value as stringArray, so the element type is only in the help text")

	tag, _ := request.Property("tag")
	require.Equal(t, api.TypeArray, tag.Schema.Type)
	assert.Equal(t, api.TypeString, tag.Schema.Items.Type)

	status, _ := request.Property("status")
	assert.Equal(t, []string{"available", "sold"}, status.Schema.Enum)
	assert.Equal(t, "Where the pet is in its life.", status.Schema.Description)

	name, _ := request.Property("name")
	assert.True(t, request.IsRequired("name"), "the (required) note is what makes a member required")
	assert.False(t, name.Schema.Deprecated)
}

// A wrapped flag description is joined rather than cut where the terminal ended.
func TestAWrappedFlagDescriptionIsOneSentence(t *testing.T) {
	const page = `tool sync

Usage:
  tool sync [flags]

Flags:
      --server string       Where to call, when the description declares none or another one is
                            wanted, and the sentence is long enough that pflag wrapped it.
`

	described, err := cli.DescribeHelp(page, cli.DescribeOptions{})
	require.NoError(t, err)
	// --server is the tree's own flag, so the point of this page is that the
	// wrapping does not disturb the reading of the block it sits in.
	assert.Equal(t, "tool", described.ID)
}

// A page that describes no operation is refused rather than published as an empty
// API: a caller would publish it, and an empty surface is a broken one.
func TestAPageWithNoOperationIsRefused(t *testing.T) {
	_, err := cli.DescribeHelp(rootPage, cli.DescribeOptions{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
}

// A parameter states where it travels, and that is what tells it apart from a
// member of the request value: the member's value travels in the request, so it
// states no location of its own.
func TestAParameterIsToldFromARequestMemberByItsLocation(t *testing.T) {
	const page = `petstore/pets/getPet

GET /pets/{petId}

Reads one pet.

Usage:
  petstore pets get-pet [flags]

Flags:
      --petId string   Identifier of the pet to read. (required; path)
      --trace string   A header the operation accepts. (header)
`

	described, err := cli.DescribeHelp(page, cli.DescribeOptions{})
	require.NoError(t, err)
	operation := single(t, described)

	require.Len(t, operation.Parameters, 2)
	assert.Equal(t, "petId", operation.Parameters[0].Name)
	assert.Equal(t, api.ParameterInPath, operation.Parameters[0].In)
	assert.True(t, operation.Parameters[0].Required)
	assert.Equal(t, "Identifier of the pet to read.", operation.Parameters[0].Description)
	assert.Equal(t, api.ParameterInHeader, operation.Parameters[1].In)
	assert.Nil(t, operation.Request, "a page with no member flags describes no request value")
	assert.Equal(t, "GET", operation.Method)
	assert.Equal(t, "/pets/{petId}", operation.Path)
}

// A flag the tree owns is not one of the operation's inputs: --server is where
// calls go and --help is cobra's, and describing either as an input would put the
// tool's plumbing in the API's contract.
func TestTheTreesOwnFlagsAreNotDescribed(t *testing.T) {
	const page = `petstore/pets/listPets

GET /pets

Usage:
  petstore pets list-pets [flags]

Flags:
  -h, --help   help for list-pets

Global Flags:
      --config string   A file to read flag values from.
      --server string   Base URL to call.
`

	described, err := cli.DescribeHelp(page, cli.DescribeOptions{})
	require.NoError(t, err)
	operation := single(t, described)
	assert.Empty(t, operation.Parameters)
	assert.Nil(t, operation.Request)
}

// A boolean flag is printed with no type word at all, which is how pflag renders
// it, and an option value states what a bare flag means rather than a type.
func TestABooleanFlagHasNoTypeWord(t *testing.T) {
	const page = `tool sync

Usage:
  tool sync [flags]

Flags:
      --dry-run            Report what would change and change nothing. (required)
      --ratio float        How much to take.
      --timeout duration   How long to wait.
      --color string[="auto"]   When to colour the output.
`

	described, err := cli.DescribeHelp(page, cli.DescribeOptions{})
	require.NoError(t, err)
	operation := single(t, described)
	require.NotNil(t, operation.Request)

	dryRun, paused := operation.Request.Property("dry-run")
	require.True(t, paused)
	assert.Equal(t, api.TypeBoolean, dryRun.Schema.Type)
	assert.True(t, operation.Request.IsRequired("dry-run"))

	ratio, _ := operation.Request.Property("ratio")
	assert.Equal(t, api.TypeNumber, ratio.Schema.Type)

	timeout, _ := operation.Request.Property("timeout")
	assert.Equal(t, api.TypeString, timeout.Schema.Type)
	assert.Equal(t, "duration", timeout.Schema.Format)

	color, present := operation.Request.Property("color")
	require.True(t, present)
	assert.Equal(t, api.TypeString, color.Schema.Type, "an option value is not part of the type")
}

// A default is a fact the page prints beside the help, and it is read back as the
// schema's default rather than as the end of the sentence.
func TestADefaultIsReadOutOfTheHelpText(t *testing.T) {
	const page = `tool sync

Usage:
  tool sync [flags]

Flags:
      --name string     What to call it. (default "thing")
      --count int       How many. (default 3)
      --ratio float     How much. (default 0.5)
`

	described, err := cli.DescribeHelp(page, cli.DescribeOptions{})
	require.NoError(t, err)
	request := single(t, described).Request
	require.NotNil(t, request)

	name, _ := request.Property("name")
	assert.Equal(t, "What to call it.", name.Schema.Description)
	assert.Equal(t, "thing", name.Schema.Default)

	count, _ := request.Property("count")
	assert.Equal(t, int64(3), count.Schema.Default)

	ratio, _ := request.Property("ratio")
	assert.Equal(t, 0.5, ratio.Schema.Default)
}

// A parenthetical that is not a note this reader knows stays prose. Moving a
// sentence of documentation into a parameter's location because it happened to end
// in brackets would be a silent corruption of the description.
func TestAParentheticalThatIsNotANoteStaysProse(t *testing.T) {
	const page = `tool sync

Usage:
  tool sync [flags]

Flags:
      --direction string   Which way to go (forwards or backwards)
`

	described, err := cli.DescribeHelp(page, cli.DescribeOptions{})
	require.NoError(t, err)
	operation := single(t, described)
	direction, _ := operation.Request.Property("direction")
	assert.Equal(t, "Which way to go (forwards or backwards)", direction.Schema.Description)
	assert.Empty(t, operation.Parameters)
}

// A deprecated operation says so in its own page, and a deprecated flag says so in
// its own line. Both still run, so both are described.
func TestDeprecationIsReadFromThePagesOwnWords(t *testing.T) {
	const page = `tool/things/oldThing

GET /old

Does the old thing.

This operation is deprecated and should not be used for new work.

This operation streams responses, and a command line invokes unary operations only.

Usage:
  tool old [flags]

Flags:
      --legacy string   The old way. (deprecated)
`

	described, err := cli.DescribeHelp(page, cli.DescribeOptions{})
	require.NoError(t, err)
	operation := single(t, described)
	assert.True(t, operation.Deprecated)
	assert.True(t, operation.Streaming.Server)
	assert.False(t, operation.Streaming.Client)
	assert.Equal(t, "Does the old thing.", operation.Description)
	assert.Contains(t, operation.Description, "old thing")
	legacy, _ := operation.Request.Property("legacy")
	assert.True(t, legacy.Schema.Deprecated)
}

// A command that both groups subcommands and runs is a service, and its own run is
// not invented as an operation: the model has no operation that is also a service,
// and a fabricated one would make the generator flatten the service and lose the
// children the page was read for.
func TestACommandThatGroupsAndRunsIsAService(t *testing.T) {
	described, err := cli.DescribeHelp(strings.Join([]string{rootPage, groupPage, leafPage}, "\n"), cli.DescribeOptions{})
	require.NoError(t, err)

	service, ok := described.Service("pets")
	require.True(t, ok)
	assert.Equal(t, "Operations on pets.", service.Description)
	assert.Equal(t, "Operations on pets.", service.Title)
	for _, operation := range service.Operations {
		assert.NotEqual(t, "pets", operation.Name, "the group's own run is not an operation")
	}
}

// A command with no flags is still an operation: its page states what it does and
// its usage line is all the shape it has.
func TestACommandWithNoFlagsIsAnOperation(t *testing.T) {
	const page = `Prints the current state.

Usage:
  tool status
`

	described, err := cli.DescribeHelp(page, cli.DescribeOptions{})
	require.NoError(t, err)
	operation := single(t, described)
	assert.Equal(t, "Prints the current state.", operation.Description)
	assert.Empty(t, operation.Parameters)
	assert.Nil(t, operation.Request)
}

// The summary a page shows for a command is the one its parent gives it, and it
// may be wrapped under its own column.
func TestASummaryIsReadFromTheParentsCommandList(t *testing.T) {
	text := strings.Join([]string{groupPage, `petstore/pets/getPet

GET /pets/{petId}

Reads a pet, and the summary beside it is long enough that cobra wraps it under
its own column in the list.

Usage:
  petstore pets get-pet [flags]
`}, "\n")

	described, err := cli.DescribeHelp(text, cli.DescribeOptions{})
	require.NoError(t, err)
	operation, ok := described.Operation("petstore/pets/getPet")
	require.True(t, ok)
	assert.Equal(t, "Reads a pet, and the summary beside it is long enough that cobra wraps it under its own column.",
		operation.Summary)
}

// Older cobra spells the command list "Commands:" rather than "Available
// Commands:", and a grouped tree adds "Additional Commands:". Both are the same
// fact.
func TestTheOlderCommandListHeaderIsRead(t *testing.T) {
	const page = `tool

Usage:
  tool [command]

Commands:
  add   Adds a thing.
  rm    Removes a thing.
`
	pages := strings.Join([]string{page, `tool add

Usage:
  tool add [flags]
`}, "\n")

	described, err := cli.DescribeHelp(pages, cli.DescribeOptions{})
	require.NoError(t, err)
	_, ok := described.Operation("tool/tool/add")
	assert.True(t, ok, "the older header is the same section")
}

// A tool may replace cobra's usage template and print the usage on the header
// line, with the description after it and its flags under "Options". The grammar
// is still cobra's; only the words that introduce the sections change.
func TestATemplateOfTheToolsOwnIsRead(t *testing.T) {
	const page = `Usage:  dock [OPTIONS] COMMAND

A self-sufficient runtime.

Common Commands:
  run         Create and run a new container
  exec        Execute a command in a running container

Options:
  -D, --debug                 Enable debug mode
      --log-level string      Set the logging level (default info)
`
	const run = `Usage:  dock run [OPTIONS] IMAGE

Run a command in a new container

Options:
  -d, --detach                Run container in background
      --name string           Assign a name to the container
`

	described, err := cli.DescribeHelp(strings.Join([]string{page, run}, "\n"), cli.DescribeOptions{})
	require.NoError(t, err)
	assert.Equal(t, "A self-sufficient runtime.", described.Description,
		"a description printed after the usage is still the page's prose")

	operation, ok := described.Operation("dock/dock/run")
	require.True(t, ok, "%v", described.OperationIDs())
	require.NotNil(t, operation.Request)
	detach, present := operation.Request.Property("detach")
	require.True(t, present)
	assert.Equal(t, api.TypeBoolean, detach.Schema.Type)

	name, present := operation.Request.Property("name")
	require.True(t, present)
	assert.Equal(t, api.TypeString, name.Schema.Type)
}

// Cobra groups a command list under a title of the tool's own choosing, which it
// prints where the list would be. It is a command list like any other.
func TestACommandGroupTitleIsACommandList(t *testing.T) {
	const page = `A tool with grouped commands.

Usage:
  tool [command]

Builtin/Core:
  one   The first.
  two   The second.

Plugin Tools:
  three   The third.

Flags:
  -h, --help   help for tool
`
	text := strings.Join([]string{page,
		"tool/tool/three\n\nThe third.\n\nUsage:\n  tool three [flags]\n",
	}, "\n")

	described, err := cli.DescribeHelp(text, cli.DescribeOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"tool/tool/three"}, described.OperationIDs(),
		"a page for an entry under a group title is that command's page")
}

// A parenthetical that starts with the word "default" and goes on is a sentence,
// not a value: reading it as one would invent a default and cut the explanation of
// the flag in half.
func TestADefaultWordInAProseParentheticalIsNotAValue(t *testing.T) {
	const page = `tool sync

Usage:
  tool sync [flags]

Flags:
      --port string   daemon port (default DOTFILESD_PORT env or 9105)
`

	described, err := cli.DescribeHelp(page, cli.DescribeOptions{})
	require.NoError(t, err)
	port, present := single(t, described).Request.Property("port")
	require.True(t, present)
	assert.Nil(t, port.Schema.Default)
	assert.Equal(t, "daemon port (default DOTFILESD_PORT env or 9105)", port.Schema.Description)
}

// Cobra's own help command is listed in every page of a tool with subcommands and
// is the tool's plumbing: running it prints another page.
func TestCobrasHelpCommandIsNotAnOperation(t *testing.T) {
	const page = `tool

Usage:
  tool [command]

Available Commands:
  help   Help about any command
  add    Adds a thing.
`
	text := strings.Join([]string{page, `tool add

Usage:
  tool add [flags]
`, `tool help [command]

Help provides help for any command in the application.

Usage:
  tool help [command] [flags]
`}, "\n")

	described, err := cli.DescribeHelp(text, cli.DescribeOptions{})
	require.NoError(t, err)
	for _, operation := range described.Operations() {
		assert.NotEqual(t, "help", operation.Name)
	}
	assert.Equal(t, []string{"tool/tool/add"}, described.OperationIDs())
}

// A page that states nothing this reader recognizes is refused rather than
// described as an empty API: an empty description is one a caller would publish.
func TestTextWithNoPageIsRefused(t *testing.T) {
	_, err := cli.DescribeHelp("hello, this is not a help page\n", cli.DescribeOptions{})
	require.Error(t, err)
	assert.True(t, api.IsKind(err, api.KindInvalid))
}

// The identifier a page opens with is the description's own spelling of its names,
// which is the one thing a command line cannot otherwise state: a command is typed
// as get-pet and the operation is called getPet.
func TestTheIdentifierLineNamesTheOperation(t *testing.T) {
	described, err := cli.DescribeHelp(leafPage, cli.DescribeOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"petstore/pets/createPet"}, described.OperationIDs())

	// Without one, the word a caller types is the name: nothing else is available,
	// and a command name is stable through the generator in both directions.
	const foreign = `tool add

Usage:
  tool add [flags]

Flags:
      --file string   The file to add.
`
	unnamed, err := cli.DescribeHelp(foreign, cli.DescribeOptions{APIID: "tool"})
	require.NoError(t, err)
	assert.Equal(t, []string{"tool/tool/add"}, unnamed.OperationIDs())
}
