package cli

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	api "github.com/Manu343726/hammer"
	"github.com/spf13/cobra"
)

// Format is the description format identifier a command line is described as.
const Format api.Format = "cli"

// Transport is the transport a described command line is invoked over: a
// process, started with arguments.
const Transport api.Transport = "cli"

// SourceKind is where a description read from a binary came from.
const SourceKind api.SourceKind = "binary"

// MetadataOrigin records the format the described tool states its own
// description came from, when its help says so. It is not this description's
// format — that is the command line — which is why it is metadata rather than
// the API's own format.
const MetadataOrigin = "cli.described_from"

// MetadataTruncated records what a bounded walk left unread, so a caller can
// tell a complete description of a small tool from a partial one of a large
// tool. A truncated description is a subset, never a guess.
const MetadataTruncated = "cli.truncated"

// Defaults of a bounded walk. They are generous enough to describe a large tool
// and small enough that a runaway tree — a command that lists itself, a wrapper
// that forwards every argument — ends.
const (
	DefaultMaxDepth    = 4
	DefaultMaxCommands = 256
)

// inventedBodySummary is the sentence the generator writes for a request value
// the description does not comment on (see wholeBodySummary). It is not
// documentation, so reading it back does not become a description.
const inventedBodySummary = "The whole request value."

// reservedDescribeFlags are the flags a generated tree owns rather than takes
// from the description. They are the tree's own plumbing, so they are not
// described as the command's inputs — and because they are persistent, a read
// that did not skip them would put --server and --config on every operation.
var reservedDescribeFlags = map[string]bool{"help": true, "server": true, "config": true}

// DescribeOptions configures reading a command line into a description.
type DescribeOptions struct {
	// APIID overrides the identifier given to the described API. Empty derives
	// one from what the help states, then from the command's own name.
	//
	// An override renames the API, and because an operation identifier is built
	// from it, it renames every operation too.
	APIID string
	// Name, Title, and Version override the corresponding fields. Each is
	// derived from the help when left empty.
	Name    string
	Title   string
	Version string
	// MaxDepth bounds how many levels of subcommands DescribeBinary reads. A
	// deeper tree is described in part, and MetadataTruncated says so.
	MaxDepth int
	// MaxCommands bounds how many help pages one DescribeBinary call reads.
	MaxCommands int
	// HelpTimeout bounds one help invocation. It defaults to 30 seconds.
	HelpTimeout time.Duration
}

func (o DescribeOptions) maxDepth() int {
	if o.MaxDepth > 0 {
		return o.MaxDepth
	}
	return DefaultMaxDepth
}

func (o DescribeOptions) maxCommands() int {
	if o.MaxCommands > 0 {
		return o.MaxCommands
	}
	return DefaultMaxCommands
}

// Describe reads a description out of an in-memory command tree.
//
// It is the honest path when the source is available: a flag's type, its default,
// and the command it belongs to are read from the tree rather than from a page
// whose layout had to be interpreted. It describes the same surface a page does,
// which is what makes the two directions meet: a tree described this way and its
// own help described by DescribeHelp produce the same description.
//
// What it does not describe, because the tool does not offer it: a hidden command
// or a hidden flag. What it describes although no page states it: a deprecated
// command, which still runs.
func Describe(root *cobra.Command, options DescribeOptions) (api.API, error) {
	if root == nil {
		return api.API{}, api.Errorf(api.KindInvalid, "a command tree is required")
	}
	pages := make([]commandPage, 0, 8)
	for _, command := range describedCommands(root) {
		pages = append(pages, pageOf(command))
	}
	return assemble(pages, options, root.CommandPath())
}

// DescribeHelp reads a description out of one or more captured help pages.
//
// One page per command is what cobra prints, so a tree's help is a stream of
// them; this reader takes either shape. A page that advertises subcommands
// describes a service, a page that runs describes an operation, and what a page
// cannot state is degraded in the documented ways rather than invented.
func DescribeHelp(help string, options DescribeOptions) (api.API, error) {
	pages := parseHelpPages(help)
	if len(pages) == 0 {
		return api.API{}, api.Errorf(api.KindInvalid,
			"the text states no command: it holds no usage section this reader recognizes")
	}
	return assemble(pages, options, "")
}

// HelpText renders a tree's help as the text DescribeHelp reads: one page per
// command, in the order the tree lists them.
//
// It is what makes a tree describable through the same path a binary is, and it
// is how the two directions are held to agree in the tests. The pages are
// rendered into memory, and each command's output is restored afterwards.
func HelpText(root *cobra.Command) (string, error) {
	if root == nil {
		return "", api.Errorf(api.KindInvalid, "a command tree is required")
	}
	var text strings.Builder
	for _, command := range describedCommands(root) {
		page, err := renderHelp(command)
		if err != nil {
			return "", err
		}
		page = strings.TrimRight(page, "\n")
		if page == "" {
			continue
		}
		if text.Len() > 0 {
			text.WriteString("\n\n")
		}
		text.WriteString(page)
	}
	return text.String(), nil
}

// identity is what one page says about which part of the description it belongs
// to.
type identity struct {
	service   string
	operation string
}

// assemble turns pages into one description.
//
// It is the single translation both readers share: a page parsed from text and a
// page taken from a tree reach this function with the same shape, so a rule about
// what becomes a service, an operation, a parameter, or a request member lives
// here once.
func assemble(pages []commandPage, options DescribeOptions, location string) (api.API, error) {
	if len(pages) == 0 {
		return api.API{}, api.Errorf(api.KindInvalid, "there is no command to describe")
	}
	sortPagesByDepth(pages)
	root := pages[0]
	identifier := apiIdentifier(pages, root, options, location)
	described := api.API{
		ID:        identifier,
		Name:      firstNonEmpty(options.Name, identifier),
		Title:     strings.TrimSpace(options.Title),
		Version:   strings.TrimSpace(options.Version),
		Format:    Format,
		Transport: Transport,
		Source:    api.Source{Kind: SourceKind, Location: location},
		Metadata:  map[string]string{},
	}

	// A tool that is one command has no prose of its own above the command: its
	// page is the operation's page, and reading it as the API's would put the
	// operation's wire line in the API's description.
	rootIsCommand := len(root.children) == 0 && root.runnable
	if !rootIsCommand {
		readRootPage(root, &described)
	} else {
		described.Description = ""
	}
	if described.Title == "" {
		described.Title = firstNonEmpty(firstSentence(described.Description), described.Name)
	}

	services := make([]api.Service, 0, len(pages))
	operations := make(map[string]map[string]bool, len(pages))
	serviceAt := func(name string) *api.Service {
		for index := range services {
			if services[index].Name == name {
				return &services[index]
			}
		}
		services = append(services, api.Service{Name: name})
		operations[name] = make(map[string]bool, 4)
		return &services[len(services)-1]
	}

	if rootIsCommand {
		rootIdentity := identify(root, described.ID, programWord(root))
		service := serviceAt(firstNonEmpty(rootIdentity.service, programWord(root), described.Name))
		operation, err := operationFromPage(root, rootIdentity)
		if err != nil {
			return api.API{}, err
		}
		operations[service.Name][operation.Name] = true
		service.Operations = append(service.Operations, operation)
	}
	for _, page := range pages[1:] {
		pageIdentity := identify(page, described.ID, programWord(root))
		service := serviceAt(pageIdentity.service)
		if len(page.children) > 0 {
			// A command that groups subcommands is a service. Its own run is not
			// described: the model has no operation that is also a service, and
			// inventing one would make the generator flatten the service and lose
			// exactly the children this page was read for.
			description, effects := readGroupPage(page)
			if service.Description == "" {
				service.Description = description
			}
			if len(service.SideEffects) == 0 {
				service.SideEffects = effects
			}
			if service.Title == "" {
				service.Title = firstSentence(page.summary)
			}
			continue
		}
		operation, err := operationFromPage(page, pageIdentity)
		if err != nil {
			return api.API{}, err
		}
		for suffix := 2; operations[service.Name][operation.Name]; suffix++ {
			operation.Name = pageIdentity.operation + "-" + strconv.Itoa(suffix)
		}
		operations[service.Name][operation.Name] = true
		service.Operations = append(service.Operations, operation)
	}

	if len(services) == 0 {
		return api.API{}, api.Errorf(api.KindInvalid,
			"the help advertises no command that can be described")
	}
	total := 0
	for _, service := range services {
		total += len(service.Operations)
	}
	if total == 0 {
		return api.API{}, api.Errorf(api.KindInvalid,
			"the help describes no operation: it advertises %d commands, and describing them takes one page each",
			len(pages[0].children))
	}
	described.Services = services
	if len(described.Metadata) == 0 {
		described.Metadata = nil
	}
	normalized, err := described.Normalize()
	if err != nil {
		return api.API{}, api.WrapError(api.KindInvalid, err, "the described command line is not a well-formed description")
	}
	return normalized, nil
}

// sortPagesByDepth puts a page above the pages that describe its commands, so a
// service is created before its operations whatever order the text was captured
// in. The sort is stable: the order within one depth is the tool's own.
func sortPagesByDepth(pages []commandPage) {
	sort.SliceStable(pages, func(i, j int) bool { return len(pages[i].path) < len(pages[j].path) })
}

// apiIdentifier is what the description calls itself: what the caller chose, then
// what the help states, then the command's own name.
func apiIdentifier(pages []commandPage, root commandPage, options DescribeOptions, location string) string {
	if identifier := strings.TrimSpace(options.APIID); identifier != "" {
		return identifier
	}
	for _, page := range pages {
		segments, ok := identifierLine(firstBlock(page.long))
		// Only a three-part identifier states which API this is: api/service/
		// operation. A two-part one is ambiguous — this package's own generator
		// writes api/service on a group's page, and a generator that describes a
		// service's methods writes service/method on an operation's page — so it
		// names nothing at the API level, and the command's own name decides.
		if !ok || len(segments) < 3 {
			continue
		}
		return strings.Join(segments[:len(segments)-2], "/")
	}
	// The program is the first word of the path a page states, not the last: the
	// last word is the command, and a description is not named after one of its
	// operations.
	var rootWord string
	if len(root.path) > 0 {
		rootWord = root.path[0]
	}
	candidates := []string{rootWord, filepath.Base(strings.TrimSpace(location))}
	for _, candidate := range candidates {
		if word := strings.TrimSuffix(candidate, filepath.Ext(candidate)); strings.TrimSpace(word) != "" && word != "." {
			return word
		}
	}
	return "cli"
}

// firstNonEmpty is the first value that states something.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstBlock(text string) string {
	blocks := paragraphs(text)
	if len(blocks) == 0 {
		return ""
	}
	return blocks[0]
}

// programWord is the word a caller types to reach the whole tool: the first word
// of the path a page states, which is the root command rather than any of the
// commands under it.
func programWord(root commandPage) string {
	if len(root.path) > 0 {
		return root.path[0]
	}
	return ""
}

func lastPathWord(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return path[len(path)-1]
}

// identify is which part of the description a page belongs to, read from the
// identifier it opens with and otherwise from where the command sits.
//
// The identifier is the more precise statement and wins where it speaks: it names
// the operation in the description's own spelling, where the command line only
// has the word a caller types.
func identify(page commandPage, apiID, programWord string) identity {
	position := identity{}
	if len(page.path) >= 3 {
		position.service = page.path[1]
		// The rest of the path names the operation. It is the command word rather
		// than the description's spelling of it, which is a loss a page without an
		// identifier cannot avoid.
		position.operation = strings.Join(page.path[2:], "-")
	} else {
		position.service = firstNonEmpty(programWord, apiID, "cli")
		position.operation = lastPathWord(page.path)
	}
	segments, ok := identifierLine(firstBlock(page.long))
	switch {
	case ok && len(segments) >= 3:
		position.service = segments[len(segments)-2]
		position.operation = segments[len(segments)-1]
	case ok && len(segments) == 2 && len(page.children) > 0:
		// api/service: the page is the service's own, which is the shape this
		// package's generator gives one.
		position.service = segments[1]
	case ok && len(segments) == 2:
		// service/method: the page is an operation's, and the identifier names
		// the method in the description's own spelling rather than the word a
		// caller types.
		position.operation = segments[1]
	}
	return position
}

// readRootPage reads what a program's own page states about the API.
func readRootPage(page commandPage, described *api.API) {
	prose := make([]string, 0, len(page.long))
	for _, block := range paragraphs(page.long) {
		if servers, ok := declaredServers(block); ok {
			described.DeclaredServers = servers
			continue
		}
		if origin, ok := declaredOrigin(block); ok {
			described.Metadata[MetadataOrigin] = origin
			continue
		}
		prose = append(prose, block)
	}
	described.Description = strings.TrimSpace(strings.Join(prose, "\n\n"))
}

// readGroupPage reads what a grouping command's page states about a service.
func readGroupPage(page commandPage) (string, []api.SideEffect) {
	prose := make([]string, 0, len(page.long))
	var effects []api.SideEffect
	for index, block := range paragraphs(page.long) {
		if index == 0 {
			if _, ok := identifierLine(block); ok {
				continue
			}
		}
		if declared, ok := declaredSideEffects(block); ok {
			effects = declared
			continue
		}
		prose = append(prose, block)
	}
	return strings.TrimSpace(strings.Join(prose, "\n\n")), effects
}

// operationFromPage reads one operation out of one page.
func operationFromPage(page commandPage, pageIdentity identity) (api.Operation, error) {
	operation := api.Operation{Name: pageIdentity.operation}
	method, path, description, effects, deprecated, streaming := readCommandPage(page)
	operation.Method = method
	operation.Path = path
	operation.Description = description
	operation.SideEffects = effects
	// A page cannot state that its own command is deprecated — a deprecated
	// command is not listed in its parent's help — so the tree is the only source
	// for it, and a page's own note is the other.
	operation.Deprecated = deprecated || page.deprecated
	operation.Streaming = streaming
	operation.Summary = firstSentence(page.summary)
	if operation.Summary == "" {
		operation.Summary = firstSentence(description)
	}
	operation.Parameters, operation.Request = readFlags(page.flags)
	return operation, nil
}

// readCommandPage separates a runnable page into the facts it states beside its
// prose. The order the blocks are read in is the order this package's generator
// writes them, and a block that is prose stays prose: an unrecognized block is
// never discarded, because dropping a sentence of documentation is worse than
// keeping a fact that reads oddly.
func readCommandPage(page commandPage) (
	method, path, description string,
	effects []api.SideEffect, deprecated bool, streaming api.Streaming,
) {
	prose := make([]string, 0, len(page.long))
	blocks := paragraphs(page.long)
	// A wire line is only read where a wire line belongs: the block after the
	// identifier, or the first block of a page that states none. Anywhere else a
	// capitalised word is a heading or a sentence beginning, and reading it as a
	// method would invent a transport the page never stated.
	wireAt := 0
	if len(blocks) > 0 {
		if _, ok := identifierLine(blocks[0]); ok {
			wireAt = 1
		}
	}
	for index, block := range blocks {
		if index == 0 && wireAt == 1 {
			continue
		}
		if index == wireAt {
			if readMethod, readPath, ok := readWireLine(block, wireAt == 1); ok {
				method, path = readMethod, readPath
				continue
			}
		}
		if declared, ok := declaredSideEffects(block); ok {
			effects = declared
			continue
		}
		if declaredDeprecated(block) {
			deprecated = true
			continue
		}
		if declared, ok := declaredStreaming(block); ok {
			streaming = declared
			continue
		}
		prose = append(prose, block)
	}
	return method, path, strings.TrimSpace(strings.Join(prose, "\n\n")), effects, deprecated, streaming
}

// memberNode is one value of a request being reassembled from the flags that
// address it: the flag that carries the value whole, and the flags that carry its
// members.
type memberNode struct {
	name        string
	schema      *api.Schema
	description string
	required    bool
	children    []*memberNode
	index       map[string]*memberNode
}

func (n *memberNode) child(name string) *memberNode {
	if n.index == nil {
		n.index = make(map[string]*memberNode, 4)
	}
	if existing, ok := n.index[name]; ok {
		return existing
	}
	created := &memberNode{name: name}
	n.index[name] = created
	n.children = append(n.children, created)
	return created
}

// schemaOf rebuilds one value, and the values under it.
func (n *memberNode) schemaOf() *api.Schema {
	if len(n.children) == 0 {
		if n.schema != nil {
			schema := n.schema.Clone()
			if schema.Description == "" && n.description != "" {
				schema.Description = n.description
			}
			return schema
		}
		// A value with no flag of its own states no shape, and guessing one here
		// would put a type in the description that no page and no tree stated.
		return &api.Schema{Description: n.description}
	}
	properties := make([]api.Property, 0, len(n.children))
	for _, child := range n.children {
		properties = append(properties, api.Property{
			Name:     child.name,
			Schema:   child.schemaOf(),
			Required: child.required,
		})
	}
	// The object's own shape is the one the whole-value flag stated, and its
	// members are the ones the dotted flags stated. The members win on structure:
	// they are the only statement about what the object holds.
	schema := api.ObjectSchema(properties...)
	if n.schema != nil {
		schema.Description = n.schema.Description
		schema.Default = n.schema.Default
	}
	if schema.Description == "" && n.description != "" {
		schema.Description = n.description
	}
	return schema
}

// readFlags separates a page's flags into the parameters a call carries beside
// its request value and the request value itself.
//
// The discriminator is the location a parameter states in its help: a parameter
// always says where it travels, because that is what a call has to know, and a
// request member never does, because the request value is where it travels.
func readFlags(flags []flagLine) ([]api.Parameter, *api.Schema) {
	parameters := make([]api.Parameter, 0, len(flags))
	request := &memberNode{}
	hasRequest := false
	for _, flag := range flags {
		if reservedDescribeFlags[flag.name] || flag.hidden {
			continue
		}
		prose, notes := splitFlagUsage(flag.usage)
		schema := schemaOfFlag(flag, notes)
		if notes.location != "" {
			name := strings.TrimPrefix(flag.name, string(notes.location)+".")
			if name == "" {
				name = flag.name
			}
			parameters = append(parameters, api.Parameter{
				Name:        name,
				In:          notes.location,
				Description: prose,
				Required:    notes.required,
				Deprecated:  notes.deprecated,
				Schema:      schema,
			})
			continue
		}
		hasRequest = true
		// A member's deprecation is the value's own: there is no member object to
		// carry it, so it belongs on the schema the flag describes.
		schema.Deprecated = notes.deprecated
		description := prose
		if description == inventedBodySummary {
			description = ""
		}
		if flag.name == "body" {
			request.schema = schema
			request.description = description
			request.required = notes.required
			continue
		}
		node := request
		for _, step := range strings.Split(strings.TrimPrefix(flag.name, "body."), ".") {
			node = node.child(step)
		}
		node.schema = schema
		node.description = description
		node.required = notes.required
	}
	if len(parameters) == 0 {
		parameters = nil
	}
	if !hasRequest {
		return parameters, nil
	}
	return parameters, request.schemaOf()
}
