package cli

import (
	"strconv"
	"strings"

	api "github.com/Manu343726/hammer"
)

// A command line publishes no document. Its help page is the whole of what it
// states about itself, so reading a description back out of one is a grammar
// problem — and the grammar is cobra's, with pflag's column layout inside it.
//
// The layout facts the grammar relies on are worth stating, because each one is
// something a naive line split gets silently wrong:
//
//   - pflag pads every flag's help to a single column computed from the longest
//     spec in the block. The description therefore starts at the same column on
//     every line, so a line indented less than that column is a new flag and one
//     indented to it is the previous description wrapped. Comparing against the
//     description column rather than a constant matters because a shorthand flag
//     starts at column two and a long-only flag at column six.
//   - cobra's template indents a command-list entry by two columns and its
//     wrapped summary to the padding column, so the same rule separates entries
//     from wrappers there.
//   - a blank line ends a block, and the sections are the template's own words:
//     Usage, Aliases, Examples, Available Commands, Flags, Global Flags. Older
//     cobra spells the command list "Commands:", and a grouped tree adds
//     "Additional Commands:".
//   - a flag's help carries facts beyond prose in a parenthetical trailer, which
//     is where this package's own generator puts them (see input.usage) and
//     where pflag puts a default and a deprecation. Peeling that trailer is what
//     recovers required-ness, a parameter's location, an enumerated value's
//     choices, and a default — none of which have a column of their own.

// commandPage is one command as its help page states it.
type commandPage struct {
	// path is the command path the usage line states, root word first.
	path []string
	// summary is the one line the parent page gives this command. It is empty
	// until the parent's command list has been read.
	summary string
	// long is the prose the page opens with.
	long string
	// children are the commands the page advertises, in the order it lists them.
	children []commandRef
	// flags are the page's own flags, then the ones it inherits.
	flags []flagLine
	// runnable reports whether the page states that the command runs itself.
	runnable bool
	// deprecated is only knowable from a tree: a deprecated command is not
	// listed in its parent's help, so no page states it.
	deprecated bool
}

// commandRef is one entry of a page's command list.
type commandRef struct {
	name    string
	summary string
}

// flagLine is one flag as a page lists it.
type flagLine struct {
	// name is the long name, without the leading dashes.
	name string
	// typeWord is the type column, as printed. It is empty for a flag the page
	// shows without a value, which is how pflag renders a boolean.
	typeWord string
	// usage is the help text after the type column, with a default and a
	// deprecation still in it — the same shape a page renders, so one reader
	// handles both a parsed page and a walked tree.
	usage string
	// hidden marks a flag the tool keeps out of its help. It is only knowable
	// from a tree, and such a flag is not described either way.
	hidden bool
}

func isFlagSection(header string) bool {
	switch header {
	case "Flags:", "Global Flags:", "Local Flags:":
		return true
	case "Options:", "Global Options:":
		// Not cobra's own words: a tool that replaced the usage template to say
		// "Options" is still listing pflag flags in pflag's columns.
		return true
	default:
		return false
	}
}

// sectionHeaders are the section titles this reader knows. A title may be
// followed by content on the same line, which is what a template that puts the
// usage on one line does.
var sectionHeaders = []string{
	"Usage:", "Aliases:", "Examples:", "Available Commands:", "Commands:",
	"Additional Commands:", "Additional help topics:", "Flags:", "Global Flags:",
	"Local Flags:", "Options:", "Global Options:",
}

// splitSectionHeader reads a section title off a line, with whatever the line
// states after it.
func splitSectionHeader(content string) (header, rest string, ok bool) {
	for _, candidate := range sectionHeaders {
		if content == candidate {
			return candidate, "", true
		}
		if strings.HasPrefix(content, candidate+" ") || strings.HasPrefix(content, candidate+"\t") {
			return candidate, strings.TrimSpace(content[len(candidate):]), true
		}
	}
	return "", "", false
}

func isCommandSection(header string) bool {
	switch header {
	case "Available Commands:", "Commands:", "Additional Commands:":
		return true
	default:
		return false
	}
}

// isGroupTitle reports whether a line is a title cobra printed above a command
// list instead of one of its own section headers.
//
// It is recognised by shape because cobra states no marker: a short line in
// capitals or title case, ending in a colon. A sentence that ends in a colon —
// "The following are supported:" — is longer and not capitalised word by word,
// which is what keeps a heading of prose from becoming a command list.
func isGroupTitle(content string) bool {
	if !strings.HasSuffix(content, ":") || strings.Contains(content, "  ") {
		return false
	}
	words := strings.Fields(strings.TrimSuffix(content, ":"))
	if len(words) == 0 || len(words) > 3 {
		return false
	}
	for _, word := range words {
		first := []rune(word)[0]
		if first < 'A' || first > 'Z' {
			return false
		}
	}
	return true
}

// isPageNote reports whether a line is cobra's own closing remark rather than
// content. It is emitted once per page and would otherwise read as the opening
// prose of the next page when several pages are captured into one stream.
func isPageNote(trimmed string) bool {
	return strings.HasPrefix(trimmed, `Use "`) && strings.HasSuffix(trimmed, "for more information about a command.")
}

// cobraHelpSummary is the one line cobra gives the help command it generates. A
// tool with subcommands always lists it, and it is the tool's own plumbing rather
// than an operation: running it prints another page.
const cobraHelpSummary = "Help about any command"

// parseHelpPages reads one or more help pages.
//
// More than one page is the normal case: capturing a tree's help appends one page
// per command, and a page boundary is prose that follows a page which has already
// stated its usage. A single page is the case of a tool that offers one command,
// or of a caller who read exactly one.
func parseHelpPages(text string) []commandPage {
	pages := make([]commandPage, 0, 4)
	var current *commandPage
	var prose []string
	section := ""
	// itemIndent is the indentation the current block's entries start at, and
	// descColumn the column its descriptions start at. Both are learned from the
	// first entry that states them, because neither is a constant.
	itemIndent, descColumn := -1, -1
	items := make([][]string, 0, 8)
	// sawFlags and sawNote state that the page has reached the parts a template
	// prints last: a flag block, or cobra's own closing remark. Both are what tell
	// prose that belongs to this page from prose that opens the next one, which is
	// the one thing a stream of pages does not state.
	sawFlags, sawNote := false, false
	settled := func() bool { return sawFlags || sawNote }

	flushItems := func() {
		if len(items) == 0 {
			return
		}
		if current != nil {
			switch {
			case section == "Usage:":
				for _, item := range items {
					current.applyUsage(strings.Join(item, " "))
				}
			case isFlagSection(section):
				for _, item := range items {
					if flag, ok := parseFlagItem(item); ok {
						current.flags = append(current.flags, flag)
					}
				}
			case isCommandSection(section):
				for _, item := range items {
					if child, ok := parseCommandItem(item); ok {
						current.children = append(current.children, child)
					}
				}
			}
		}
		items = items[:0]
	}
	flushPage := func() {
		flushItems()
		if current != nil && len(current.path) > 0 {
			pages = append(pages, *current)
		}
		current = nil
		section = ""
		itemIndent, descColumn = -1, -1
		sawFlags, sawNote = false, false
	}
	startPage := func() {
		flushPage()
		current = &commandPage{}
	}

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t")
		content := strings.TrimSpace(line)
		indent := leadingSpaces(line)
		header, rest, isHeaderLine := splitSectionHeader(content)
		switch {
		case content == "":
			if section == "Examples:" {
				continue
			}
			flushItems()
			section = ""
			if len(prose) > 0 {
				prose = append(prose, "")
			}
		case isHeaderLine && header == "Usage:":
			flushItems()
			if current == nil || len(current.path) > 0 {
				startPage()
			}
			current.long = strings.TrimSpace(strings.Join(prose, "\n"))
			prose = prose[:0]
			section = "Usage:"
			itemIndent, descColumn = -1, -1
			if rest != "" {
				current.applyUsage(rest)
			}
		case isHeaderLine:
			flushItems()
			if current == nil {
				current = &commandPage{}
			}
			section = header
			itemIndent, descColumn = -1, -1
			if isFlagSection(section) {
				sawFlags = true
			}
			if rest != "" {
				items = append(items, []string{rest})
			}
		case isPageNote(content):
			flushItems()
			section = ""
			sawNote = true
		case isGroupTitle(content) && current != nil && len(current.path) > 0 && !settled():
			// Cobra groups a command list under a title of the tool's own choosing
			// — "Plugin Tools:", "Scripts:" — and prints it where the command list
			// would be. It is a command list like any other, and the entries under
			// it are commands.
			flushItems()
			section = "Available Commands:"
		case section == "Examples:":
			// A worked example is the tool's documentation and the model has no
			// field for one. It is consumed rather than stored, because an example
			// is written as freely as prose: a line of it read as an entry would
			// become a flag the tool does not have.
		case indent > 0 && section != "":
			// An indented line belongs to the current block. Whether it starts a
			// new entry or continues the previous one is the column question.
			if startsEntry(content, indent, section, itemIndent, descColumn) {
				items = append(items, []string{line})
				if itemIndent < 0 || indent < itemIndent {
					itemIndent = indent
				}
				if isFlagSection(section) {
					if column := descriptionColumn(line); column > 0 &&
						(descColumn < 0 || column < descColumn) {
						descColumn = column
					}
				}
				continue
			}
			if len(items) > 0 {
				items[len(items)-1] = append(items[len(items)-1], line)
				continue
			}
			prose = append(prose, line)
		case indent > 0:
			// Indented prose: a page may open with a block written that way, and
			// the declared servers are.
			prose = append(prose, line)
		default:
			// Unindented content outside every block is prose. Which page it
			// belongs to is the only question: a template prints the description
			// before the usage, or after it, and a page whose blocks are behind it
			// is a page that is done.
			if current != nil && len(current.path) > 0 {
				if settled() {
					startPage()
				} else {
					// The usage is stated and the blocks are not: the description
					// came after the usage, which is a template of the caller's own.
					current.long = strings.TrimSpace(current.long + "\n\n" + content)
					continue
				}
			}
			prose = append(prose, content)
		}
	}
	flushPage()
	pages = dropCobrasHelp(pages)
	attachSummaries(pages)
	return pages
}

// dropCobrasHelp removes the help command cobra generates from the pages it
// appears on: the entry in its parent's command list and the page it prints.
//
// It is the tool's own plumbing rather than one of its operations — every tool
// with subcommands has it, it does the same thing in all of them, and running it
// only prints a page that is already in the stream. It is recognized by the one
// line cobra gives it, because that is the only statement about it the help makes.
func dropCobrasHelp(pages []commandPage) []commandPage {
	helpPaths := make(map[string]bool, 2)
	for _, page := range pages {
		for _, child := range page.children {
			if child.summary == cobraHelpSummary {
				helpPaths[pathKey(append(append([]string(nil), page.path...), child.name))] = true
			}
		}
	}
	if len(helpPaths) == 0 {
		return pages
	}
	kept := make([]commandPage, 0, len(pages))
	for _, page := range pages {
		if helpPaths[pathKey(page.path)] {
			continue
		}
		children := make([]commandRef, 0, len(page.children))
		for _, child := range page.children {
			if child.summary == cobraHelpSummary {
				continue
			}
			children = append(children, child)
		}
		page.children = children
		kept = append(kept, page)
	}
	return kept
}

// applyUsage reads one line of the usage section: the command path, and whether
// it runs itself. The shortest path is the command's own, because a runnable
// command that also has children states both a path with its arguments and one
// with "[command]".
func (p *commandPage) applyUsage(line string) {
	fields := strings.Fields(line)
	group := false
	if len(fields) > 0 && fields[len(fields)-1] == "[command]" {
		group = true
		fields = fields[:len(fields)-1]
	}
	path := make([]string, 0, len(fields))
	for _, field := range fields {
		if strings.HasPrefix(field, "[") || strings.HasPrefix(field, "<") ||
			strings.HasPrefix(field, "-") || strings.Contains(field, "...") {
			break
		}
		path = append(path, field)
	}
	if len(path) == 0 {
		return
	}
	if !group {
		p.runnable = true
	}
	if len(p.path) == 0 || len(path) < len(p.path) {
		p.path = path
	}
}

func leadingSpaces(line string) int {
	count := 0
	for count < len(line) && (line[count] == ' ' || line[count] == '\t') {
		count++
	}
	return count
}

// startsEntry decides whether an indented line begins a new entry of a block or
// continues the one before it.
func startsEntry(content string, indent int, section string, itemIndent, descColumn int) bool {
	if section == "Usage:" {
		return true
	}
	if isFlagSection(section) {
		if !strings.HasPrefix(content, "-") {
			return false
		}
		// A wrapped description can begin with a dash, and it is indented into
		// the description column while every spec is left of it.
		return descColumn < 0 || indent < descColumn
	}
	if isCommandSection(section) {
		return itemIndent < 0 || indent <= itemIndent
	}
	return true
}

// descriptionColumn is the column a line's description starts at, and zero when
// the line states none: that is the first run of two spaces after the spec, and
// pflag guarantees at least three.
func descriptionColumn(line string) int {
	for index := leadingSpaces(line); index+1 < len(line); index++ {
		if line[index] == ' ' && line[index+1] == ' ' {
			return index
		}
	}
	return 0
}

// parseFlagItem reads one flag entry: its spec line and the description lines
// wrapped under it.
func parseFlagItem(item []string) (flagLine, bool) {
	if len(item) == 0 {
		return flagLine{}, false
	}
	spec, description := splitAtDescription(item[0])
	flag, ok := parseFlagSpec(spec)
	if !ok {
		return flagLine{}, false
	}
	// A description that wrapped is one sentence laid out for a terminal, so its
	// lines are joined rather than read one by one: a break in the middle of a
	// sentence says where the terminal ended, not where the thought did.
	lines := []string{description}
	for _, continuation := range item[1:] {
		lines = append(lines, strings.TrimSpace(continuation))
	}
	flag.usage = strings.TrimSpace(strings.Join(nonEmpty(lines), " "))
	return flag, true
}

func splitAtDescription(line string) (spec, description string) {
	indent := leadingSpaces(line)
	body := line[indent:]
	for index := 0; index+1 < len(body); index++ {
		if body[index] == ' ' && body[index+1] == ' ' {
			return strings.TrimSpace(body[:index]), strings.TrimSpace(body[index:])
		}
	}
	return strings.TrimSpace(body), ""
}

// parseFlagSpec reads the flag column: a shorthand, the long name, and the type.
func parseFlagSpec(spec string) (flagLine, bool) {
	fields := strings.Fields(spec)
	flag := flagLine{}
	name := -1
	for index, field := range fields {
		if strings.HasPrefix(field, "--") {
			name = index
			break
		}
	}
	if name < 0 {
		return flagLine{}, false
	}
	flag.name = cutOptionValue(strings.TrimPrefix(fields[name], "--"))
	if flag.name == "" {
		return flagLine{}, false
	}
	if name+1 < len(fields) {
		flag.typeWord = cutOptionValue(fields[name+1])
	}
	return flag, true
}

// cutOptionValue drops a pflag option-value suffix, which states what a bare
// occurrence of the flag means. It is not the flag's type.
func cutOptionValue(word string) string {
	if index := strings.Index(word, "[="); index >= 0 {
		return word[:index]
	}
	if index := strings.Index(word, "="); index >= 0 {
		return word[:index]
	}
	return word
}

// parseCommandItem reads one entry of a command list: the name and the one-line
// summary beside it, which the entry may have wrapped under its own column.
func parseCommandItem(item []string) (commandRef, bool) {
	if len(item) == 0 {
		return commandRef{}, false
	}
	fields := strings.Fields(item[0])
	if len(fields) == 0 {
		return commandRef{}, false
	}
	ref := commandRef{name: fields[0], summary: strings.Join(fields[1:], " ")}
	for _, continuation := range item[1:] {
		if text := strings.TrimSpace(continuation); text != "" {
			ref.summary = strings.TrimSpace(ref.summary + " " + text)
		}
	}
	return ref, true
}

func nonEmpty(values []string) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			kept = append(kept, value)
		}
	}
	return kept
}

// attachSummaries copies each page's one-line summary out of its parent's
// command list, so an operation can be summarized without inventing a sentence
// from its prose.
func attachSummaries(pages []commandPage) {
	summaries := make(map[string]string, len(pages))
	for _, page := range pages {
		for _, child := range page.children {
			summaries[pathKey(append(append([]string(nil), page.path...), child.name))] = child.summary
		}
	}
	for index := range pages {
		if pages[index].summary != "" {
			continue
		}
		if summary, ok := summaries[pathKey(pages[index].path)]; ok {
			pages[index].summary = summary
		}
	}
}

func pathKey(path []string) string { return strings.Join(path, "/") }

// flagNotes is what the parenthetical trailer of a flag's help states. Each item
// has to be recognized for the trailer to be read at all: a description that
// happens to end in a parenthetical is far more likely than a note this reader
// does not know, and reading one as the other would quietly move a sentence of
// documentation into a flag's location.
type flagNotes struct {
	required   bool
	deprecated bool
	repeatable bool
	location   api.ParameterIn
	enum       []string
	// typeWord is the modelled value shape when the help states it in words
	// rather than in the type column. It is the only place an array's element
	// type survives: pflag prints every repeated value as stringArray.
	typeWord     string
	defaultValue string
	hasDefault   bool
}

// splitFlagUsage separates a flag's prose from the notes in its trailer.
func splitFlagUsage(usage string) (string, flagNotes) {
	notes := flagNotes{}
	text := strings.TrimSpace(usage)
	for {
		open := strings.LastIndex(text, " (")
		if open < 0 || !strings.HasSuffix(text, ")") {
			break
		}
		trailer := text[open+2 : len(text)-1]
		applied := notes
		recognized := true
		for _, token := range strings.Split(trailer, "; ") {
			if !applied.add(strings.TrimSpace(token)) {
				recognized = false
				break
			}
		}
		if !recognized {
			break
		}
		notes = applied
		text = strings.TrimSpace(text[:open])
	}
	// A help line that is only a type word documents nothing: it is what the
	// generator writes for a value the description does not comment on.
	if isTypeWord(text) {
		notes.typeWord = text
		text = ""
	}
	return text, notes
}

// add records one item of a trailer, and reports whether it is one this reader
// knows.
func (n *flagNotes) add(token string) bool {
	switch token {
	case "required":
		n.required = true
		return true
	case "deprecated":
		n.deprecated = true
		return true
	case "repeatable":
		n.repeatable = true
		return true
	}
	switch {
	case strings.HasPrefix(token, "one of: "):
		for _, value := range strings.Split(strings.TrimPrefix(token, "one of: "), ", ") {
			if value = strings.TrimSpace(value); value != "" {
				n.enum = append(n.enum, value)
			}
		}
		return true
	case strings.HasPrefix(token, "default "):
		// A default is one value: pflag prints a string quoted and anything else as
		// one bare word. A parenthetical that starts with the word and goes on —
		// "(default DOTFILESD_PORT env or 9105)" — is documentation that happens to
		// be written that way, and reading it as a value would both invent a default
		// and cut the sentence that explains the flag.
		value := strings.TrimSpace(strings.TrimPrefix(token, "default "))
		if value == "" || (strings.ContainsAny(value, " \t") && !isQuoted(value)) {
			return false
		}
		n.defaultValue = value
		n.hasDefault = true
		return true
	case strings.HasPrefix(token, "DEPRECATED: "), token == "DEPRECATED":
		n.deprecated = true
		return true
	}
	if isLocationWord(token) {
		n.location = api.ParameterIn(api.NormalizeIdentifier(token))
		return true
	}
	if isTypeWord(token) {
		n.typeWord = token
		return true
	}
	return false
}

// isLocationWord reports whether a word names a place a parameter is carried.
// The model keeps locations open, but only the documented ones are read back: an
// unknown word is far more likely to be part of a sentence.
// isQuoted reports whether a word is one quoted string.
func isQuoted(value string) bool {
	_, err := strconv.Unquote(value)
	return err == nil
}

func isLocationWord(word string) bool {
	switch api.ParameterIn(api.NormalizeIdentifier(word)) {
	case api.ParameterInPath, api.ParameterInQuery, api.ParameterInHeader,
		api.ParameterInCookie, api.ParameterInBody:
		return true
	default:
		return false
	}
}

// isTypeWord reports whether a phrase is a value shape written in words. These
// are exactly the words input.typeWord emits.
func isTypeWord(text string) bool {
	switch text {
	case "string", "integer", "number", "boolean",
		"list of strings", "list of booleans", "list of integers", "list of numbers",
		"JSON object", "JSON array", "JSON value":
		return true
	default:
		return false
	}
}

// schemaFromWord is the value shape a word states.
//
// The words are two vocabularies in one column: this package's own type words,
// which state the modelled shape, and pflag's type names, which state the Go type
// behind the flag. pflag's names are the fallback, and a name neither knows is
// kept as a format rather than guessed at as a string — a flag whose value this
// reader cannot classify is more useful stated as unknown than stated as text.
func schemaFromWord(word string) *api.Schema {
	switch word {
	case "string":
		return &api.Schema{Type: api.TypeString}
	case "integer":
		return &api.Schema{Type: api.TypeInteger}
	case "number":
		return &api.Schema{Type: api.TypeNumber}
	case "boolean":
		return &api.Schema{Type: api.TypeBoolean}
	case "JSON object":
		return &api.Schema{Type: api.TypeObject}
	case "JSON array":
		return api.ArraySchema(&api.Schema{})
	case "JSON value":
		return &api.Schema{}
	case "list of strings":
		return api.ArraySchema(api.StringSchema())
	case "list of booleans":
		return api.ArraySchema(&api.Schema{Type: api.TypeBoolean})
	case "list of integers":
		return api.ArraySchema(&api.Schema{Type: api.TypeInteger})
	case "list of numbers":
		return api.ArraySchema(&api.Schema{Type: api.TypeNumber})
	}
	// pflag's own names, after UnquoteUsage has reduced them: a bare int is any
	// width, so it states no format, while a name pflag prints verbatim does.
	switch word {
	case "", "bool", "boolfunc":
		return &api.Schema{Type: api.TypeBoolean}
	case "count", "int":
		return &api.Schema{Type: api.TypeInteger}
	case "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return &api.Schema{Type: api.TypeInteger, Format: word}
	case "float", "float64":
		return &api.Schema{Type: api.TypeNumber}
	case "float32":
		return &api.Schema{Type: api.TypeNumber, Format: word}
	case "duration":
		return &api.Schema{Type: api.TypeString, Format: "duration"}
	case "stringArray", "strings":
		return api.ArraySchema(api.StringSchema())
	case "ints", "intSlice", "int32Slice", "int64Slice":
		return api.ArraySchema(&api.Schema{Type: api.TypeInteger})
	case "uints", "uintSlice":
		return api.ArraySchema(&api.Schema{Type: api.TypeInteger, Format: "uint64"})
	case "bools", "boolSlice":
		return api.ArraySchema(&api.Schema{Type: api.TypeBoolean})
	case "floats", "float32Slice", "float64Slice":
		return api.ArraySchema(&api.Schema{Type: api.TypeNumber})
	case "stringToString":
		// A map flag is given one key=value per occurrence, so its values are
		// text and the set of keys is not stated.
		return &api.Schema{
			Type:                        api.TypeObject,
			AdditionalProperties:        api.StringSchema(),
			AdditionalPropertiesAllowed: true,
		}
	case "stringToInt", "stringToInt64":
		return &api.Schema{
			Type:                        api.TypeObject,
			AdditionalProperties:        &api.Schema{Type: api.TypeInteger},
			AdditionalPropertiesAllowed: true,
		}
	case "bytesHex", "bytesBase64", "ip", "ipMask", "ipNet":
		return &api.Schema{Type: api.TypeString, Format: word}
	default:
		return &api.Schema{Format: word}
	}
}

// schemaOfFlag is the value shape one flag states, and the prose beside it.
func schemaOfFlag(flag flagLine, notes flagNotes) *api.Schema {
	schema := schemaFromWord(flag.typeWord)
	if notes.typeWord != "" {
		// The modelled shape wins over the Go type: every repeated value is
		// registered as a string array, so the word is the only place an array's
		// element type survives.
		schema = schemaFromWord(notes.typeWord)
	}
	if notes.repeatable && schema.Type != api.TypeArray {
		schema = api.ArraySchema(schema)
	}
	if len(notes.enum) > 0 {
		// An enumerated list is documented on the element of a repeated value and
		// on the value itself otherwise, which is where input.usage writes it.
		if schema.Type == api.TypeArray {
			if schema.Items == nil {
				schema.Items = &api.Schema{}
			}
			schema.Items.Enum = append([]string(nil), notes.enum...)
		} else {
			schema.Enum = append([]string(nil), notes.enum...)
		}
	}
	if notes.hasDefault {
		schema.Default = defaultOf(schema, notes.defaultValue)
	}
	return schema
}

// defaultOf reads a default the way a help page prints one: a string quoted, a
// number bare, a list bracketed and comma separated.
func defaultOf(schema *api.Schema, text string) any {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	switch schema.Type {
	case api.TypeBoolean:
		if value, err := strconv.ParseBool(text); err == nil {
			return value
		}
	case api.TypeInteger:
		if value, err := strconv.ParseInt(text, 10, 64); err == nil {
			return value
		}
	case api.TypeNumber:
		if value, err := strconv.ParseFloat(text, 64); err == nil {
			return value
		}
	case api.TypeArray:
		inner := strings.TrimSuffix(strings.TrimPrefix(text, "["), "]")
		values := make([]any, 0, 4)
		for _, element := range strings.Split(inner, ",") {
			if element = strings.TrimSpace(element); element != "" {
				values = append(values, defaultOf(schema.Items, element))
			}
		}
		return values
	}
	// A quoted value is a string, and an unquoted one is a word the page states
	// without a type: both are carried as the text that was printed.
	if unquoted, err := strconv.Unquote(text); err == nil {
		return unquoted
	}
	return text
}

// paragraphs splits prose into the blocks a command's help separates with a
// blank line. The blocks are the sections this package's generator writes —
// identifier, wire line, prose, then one line per declared fact — so the split
// is what lets the rest of the reader tell a fact from a sentence.
func paragraphs(text string) []string {
	blocks := make([]string, 0, 6)
	var current []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			if len(current) > 0 {
				blocks = append(blocks, strings.Join(current, "\n"))
				current = nil
			}
			continue
		}
		current = append(current, strings.TrimRight(line, " \t"))
	}
	if len(current) > 0 {
		blocks = append(blocks, strings.Join(current, "\n"))
	}
	return blocks
}

// identifierLine reads the fully-qualified identifier a page opens with, when it
// opens with one. Two segments name a service and three name an operation, which
// is the whole reason the identifier is worth reading: it is the only place a
// page states the names the description gave its commands, in the spelling the
// description used rather than the spelling a command line types.
func identifierLine(block string) ([]string, bool) {
	if block == "" || strings.ContainsAny(block, " \t\n") {
		return nil, false
	}
	segments := strings.Split(block, "/")
	if len(segments) < 2 {
		return nil, false
	}
	for _, segment := range segments {
		if segment == "" {
			return nil, false
		}
	}
	return segments, true
}

// httpVerbs are the methods a wire line may state on its own. Any other word in
// capitals is only read as a method when it follows an identifier, which is how
// a format of the caller's own names its methods.
var httpVerbs = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
	"HEAD": true, "OPTIONS": true, "TRACE": true, "CONNECT": true,
}

// readWireLine reads a `METHOD /path` block.
func readWireLine(block string, afterIdentifier bool) (method, path string, ok bool) {
	fields := strings.Fields(block)
	if len(fields) == 0 || len(fields) > 2 {
		return "", "", false
	}
	word := fields[0]
	if !isMethodWord(word) {
		return "", "", false
	}
	if !httpVerbs[word] && !afterIdentifier {
		// A one-word paragraph in capitals is a method only where a method is
		// expected; anywhere else it is a heading, and a heading is prose.
		return "", "", false
	}
	if len(fields) == 2 {
		return word, fields[1], true
	}
	return word, "", true
}

func isMethodWord(word string) bool {
	if len(word) < 2 || len(word) > 12 {
		return false
	}
	for _, r := range word {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// declaredSideEffects reads the block this package's generator writes for the
// consequences a description declares.
func declaredSideEffects(block string) ([]api.SideEffect, bool) {
	const prefix = "Declared side effects: "
	if !strings.HasPrefix(block, prefix) {
		return nil, false
	}
	list := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(block, prefix)), ".")
	effects := make([]api.SideEffect, 0, 3)
	for _, effect := range strings.Split(list, ",") {
		if effect = strings.TrimSpace(effect); effect != "" {
			effects = append(effects, api.SideEffect(effect))
		}
	}
	return effects, len(effects) > 0
}

func declaredDeprecated(block string) bool {
	return strings.HasPrefix(block, "This operation is deprecated")
}

// declaredStreaming reads the note stating that an operation streams, which is
// the one place a page states it.
func declaredStreaming(block string) (api.Streaming, bool) {
	const prefix = "This operation streams "
	if !strings.HasPrefix(block, prefix) {
		return api.Streaming{}, false
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(block, prefix), ".")
	if index := strings.Index(rest, ","); index >= 0 {
		rest = rest[:index]
	}
	return api.Streaming{
		Client: strings.Contains(rest, "requests"),
		Server: strings.Contains(rest, "responses"),
	}, true
}

// declaredServers reads the block a page states its declared locations in.
func declaredServers(block string) ([]api.DeclaredServer, bool) {
	const header = "Declared servers:"
	if !strings.HasPrefix(block, header) {
		return nil, false
	}
	servers := make([]api.DeclaredServer, 0, 2)
	for _, line := range strings.Split(block, "\n")[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		server := api.DeclaredServer{URL: line}
		// The note is separated by an em dash, which is where this package's
		// generator puts a location's own description.
		if index := strings.Index(line, " — "); index >= 0 {
			server.URL = strings.TrimSpace(line[:index])
			server.Description = strings.TrimSpace(line[index+len(" — "):])
		}
		servers = append(servers, server)
	}
	return servers, len(servers) > 0
}

// declaredOrigin reads the line a page states where its own description came
// from, which is what a tool generated from an OpenAPI document or a contract
// says about itself.
func declaredOrigin(block string) (string, bool) {
	const prefix = "Described from "
	if !strings.HasPrefix(block, prefix) {
		return "", false
	}
	origin := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(block, prefix)), ".")
	return origin, origin != ""
}
