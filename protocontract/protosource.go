package protocontract

import (
	"fmt"
	"strings"
	"unicode"

	api "github.com/Manu343726/hammer"
)

// This file is the lower half of rendering: a contract as a value, and the proto3
// text it is written as.
//
// The model is small on purpose — a protobuf contract is a package, a set of
// services, and the messages those services name — and it exists because the
// spelling rules belong in one place. A name has to be claimed in the right scope,
// an enum's values belong to the scope that contains the enum rather than to the
// enum, and a comment has to keep its paragraph breaks; a walker that wrote text
// directly would repeat each of those rules at every call site and get one of them
// wrong.

// claimedName is a name a schema claimed through its reference.
type claimedName struct {
	name     string
	fullName string
}

// renderType is a protobuf type as a field writes it.
type renderType struct {
	// name is the type as written, relative to the file's package.
	name string
	// message states that the type is a declared message, which is what decides
	// whether a field may be labelled optional.
	message bool
	// mapType states that the type is a map, which is a field type and never a
	// message.
	mapType bool
}

// declarationScope is where a value's type is declared: the file, for a message an
// operation names, or the message that carries it, for one derived from a member.
type declarationScope interface {
	fileOf() *protoFile
	scopeOf() *scope
	declareMessage(hint string, schema *api.Schema) *protoMessage
	addEnum(enum *protoEnum)
}

// scope is one protobuf declaration scope: a file's package, or a message's body.
//
// Every name in a scope has to be unique, and protobuf puts a nested type and an
// enum's values in the same scope as the fields' types, so the description's own
// names have to be claimed here rather than written as they arrive.
type scope struct {
	fullName string
	names    map[string]bool
}

func newScope(fullName string) *scope {
	return &scope{fullName: fullName, names: make(map[string]bool)}
}

// claim reserves a name, answering with a free one when it is taken.
func (s *scope) claim(name string) string {
	if name == "" {
		name = "value"
	}
	if !s.names[name] {
		s.names[name] = true
		return name
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", name, suffix)
		if !s.names[candidate] {
			s.names[candidate] = true
			return candidate
		}
	}
}

// protoFile is one .proto file under construction.
type protoFile struct {
	path        string
	packageName string
	goPackage   string
	header      string
	// renderer is the translation this file belongs to, because declaring a
	// message reads the schema it carries and the findings belong to the one walk.
	renderer *renderer
	services []*protoService
	messages []*protoMessage
	enums    []*protoEnum
	// byFullName indexes every message this file declares, nested ones included,
	// so a shape named twice is declared once and a recursive shape terminates
	// instead of expanding forever.
	byFullName map[string]*protoMessage
	scope      *scope
}

func (f *protoFile) fileOf() *protoFile { return f }

func (f *protoFile) scopeOf() *scope { return f.scope }

func (f *protoFile) declareMessage(hint string, schema *api.Schema) *protoMessage {
	message := &protoMessage{
		name:    f.scope.claim(sanitizeIdentifier(hint)),
		comment: schemaComment(schema),
		file:    f,
	}
	message.fullName = qualifyName(f.packageName, message.name)
	message.scope = newScope(message.fullName)
	f.byFullName[message.fullName] = message
	f.messages = append(f.messages, message)
	f.renderer.buildFields(message, schema)
	return message
}

func (f *protoFile) addEnum(enum *protoEnum) { f.enums = append(f.enums, enum) }

// printableName is a full name as a field in this file writes it.
func (f *protoFile) printableName(fullName string) string {
	if f.packageName == "" {
		return strings.TrimPrefix(fullName, ".")
	}
	return strings.TrimPrefix(fullName, f.packageName+".")
}

// source writes the file's proto3 text.
func (f *protoFile) source() string {
	var builder strings.Builder
	if f.header != "" {
		writeComment(&builder, "", f.header)
		builder.WriteString("\n")
	}
	builder.WriteString("syntax = \"proto3\";\n")
	if f.packageName != "" {
		builder.WriteString("\npackage " + f.packageName + ";\n")
	}
	if f.goPackage != "" {
		builder.WriteString("\noption go_package = \"" + f.goPackage + "\";\n")
	}
	builder.WriteString("\n")
	for _, service := range f.services {
		service.write(&builder)
		builder.WriteString("\n")
	}
	for _, message := range f.messages {
		message.write(&builder, "")
		builder.WriteString("\n")
	}
	for _, enum := range f.enums {
		enum.write(&builder, "")
		builder.WriteString("\n")
	}
	return strings.TrimRight(builder.String(), "\n") + "\n"
}

// protoMessage is a message under construction.
type protoMessage struct {
	name     string
	fullName string
	comment  string
	fields   []*protoField
	nested   []*protoMessage
	enums    []*protoEnum
	scope    *scope
	file     *protoFile
}

func (m *protoMessage) fileOf() *protoFile { return m.file }

func (m *protoMessage) scopeOf() *scope { return m.scope }

func (m *protoMessage) declareMessage(hint string, schema *api.Schema) *protoMessage {
	message := &protoMessage{
		name:    m.scope.claim(sanitizeIdentifier(hint)),
		comment: schemaComment(schema),
		file:    m.file,
	}
	message.fullName = m.fullName + "." + message.name
	message.scope = newScope(message.fullName)
	m.file.byFullName[message.fullName] = message
	m.nested = append(m.nested, message)
	m.file.renderer.buildFields(message, schema)
	return message
}

func (m *protoMessage) addEnum(enum *protoEnum) { m.enums = append(m.enums, enum) }

func (m *protoMessage) write(builder *strings.Builder, indent string) {
	writeComment(builder, indent, m.comment)
	builder.WriteString(indent + "message " + m.name + " {\n")
	for _, field := range m.fields {
		field.write(builder, indent+"  ")
	}
	for _, enum := range m.enums {
		if len(m.fields) > 0 {
			builder.WriteString("\n")
		}
		enum.write(builder, indent+"  ")
	}
	for index, nested := range m.nested {
		if len(m.fields) > 0 || len(m.enums) > 0 || index > 0 {
			builder.WriteString("\n")
		}
		nested.write(builder, indent+"  ")
	}
	builder.WriteString(indent + "}\n")
}

// protoField is one message field.
type protoField struct {
	name       string
	jsonName   string
	number     int32
	typ        renderType
	repeated   bool
	optional   bool
	deprecated bool
	comment    string
}

func (f *protoField) write(builder *strings.Builder, indent string) {
	writeComment(builder, indent, f.comment)
	label := ""
	switch {
	case f.repeated:
		label = "repeated "
	case f.optional:
		label = "optional "
	}
	fmt.Fprintf(builder, "%s%s%s %s = %d [json_name = %q", indent, label, f.typ.name, f.name, f.number, f.jsonName)
	if f.deprecated {
		builder.WriteString(", deprecated = true")
	}
	builder.WriteString("];\n")
}

// protoEnum is an enum under construction. Its values are declared in the scope
// that contains the enum, which is why the caller claims them there.
type protoEnum struct {
	name    string
	comment string
	values  []string
}

func (e *protoEnum) write(builder *strings.Builder, indent string) {
	writeComment(builder, indent, e.comment)
	builder.WriteString(indent + "enum " + e.name + " {\n")
	for index, value := range e.values {
		fmt.Fprintf(builder, "%s  %s = %d;\n", indent, value, index)
	}
	builder.WriteString(indent + "}\n")
}

// protoService is a service under construction.
type protoService struct {
	name        string
	comment     string
	annotations []string
	methods     []*protoMethod
	scope       *scope
}

func (s *protoService) write(builder *strings.Builder) {
	writeComment(builder, "", commentFor("", s.comment, s.annotations))
	builder.WriteString("service " + s.name + " {\n")
	for index, method := range s.methods {
		if index > 0 {
			builder.WriteString("\n")
		}
		method.write(builder, "  ")
	}
	builder.WriteString("}\n")
}

// protoMethod is one rpc.
type protoMethod struct {
	name            string
	request         string
	response        string
	clientStreaming bool
	serverStreaming bool
	deprecated      bool
	comment         string
}

func (m *protoMethod) write(builder *strings.Builder, indent string) {
	writeComment(builder, indent, m.comment)
	request, response := m.request, m.response
	if m.clientStreaming {
		request = "stream " + request
	}
	if m.serverStreaming {
		response = "stream " + response
	}
	header := fmt.Sprintf("%srpc %s(%s) returns (%s)", indent, m.name, request, response)
	if !m.deprecated {
		builder.WriteString(header + ";\n")
		return
	}
	builder.WriteString(header + " {\n" + indent + "  option deprecated = true;\n" + indent + "}\n")
}

// writeComment writes a comment block, one line per line of text.
//
// A blank line stays a blank comment line, because a paragraph break is the only
// structure a protobuf comment has: collapsing it would join two paragraphs into
// one every time the contract is read back, and the prose a description carries is
// what generated help is built from.
func writeComment(builder *strings.Builder, indent, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			builder.WriteString(indent + "//\n")
			continue
		}
		builder.WriteString(indent + "// " + line + "\n")
	}
}

// protoFieldName spells a description's property name as a protobuf field name.
//
// protobuf fields are snake_case and their JSON names are lowerCamelCase, and this
// package's reader names a property by its JSON name. Deriving the field name from
// the description's name keeps the contract readable, and json_name carries the
// description's name unchanged beside it.
func protoFieldName(name string) string {
	runes := []rune(name)
	var builder strings.Builder
	for index, r := range runes {
		switch {
		case r >= 'A' && r <= 'Z':
			if index > 0 && runes[index-1] >= 'a' && runes[index-1] <= 'z' {
				builder.WriteRune('_')
			}
			builder.WriteRune(r + ('a' - 'A'))
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "value"
	}
	if result[0] >= '0' && result[0] <= '9' {
		return "field_" + result
	}
	return result
}

// enumValueName spells a literal as a protobuf enum value name.
func enumValueName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "UNSPECIFIED"
	}
	var builder strings.Builder
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "UNSPECIFIED"
	}
	if unicode.IsDigit(rune(result[0])) {
		return "VALUE_" + result
	}
	return result
}

// typeNameHint spells a derived name as a type name. A message or an enum is a
// type, and protobuf types are capitalised by convention: a member-derived name
// that arrived as a field name reads as a field at every reference otherwise.
func typeNameHint(name string) string {
	sanitized := sanitizeIdentifier(name)
	runes := []rune(sanitized)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// sanitizeIdentifier spells a name protobuf can declare.
func sanitizeIdentifier(value string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "value"
	}
	if unicode.IsDigit(rune(result[0])) {
		return "_" + result
	}
	return result
}

// sanitizePackageName spells a package protobuf can declare, one identifier per
// dotted segment.
func sanitizePackageName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	segments := strings.Split(value, ".")
	kept := make([]string, 0, len(segments))
	for _, segment := range segments {
		if strings.TrimSpace(segment) == "" {
			continue
		}
		kept = append(kept, sanitizeIdentifier(segment))
	}
	return strings.Join(kept, ".")
}

// qualifyName joins a package and a name, so a file with no package declares the
// name as it is.
func qualifyName(pkg, name string) string {
	name = strings.TrimPrefix(strings.TrimSpace(name), ".")
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}
