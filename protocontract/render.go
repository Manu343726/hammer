package protocontract

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	api "github.com/Manu343726/hammer"
	shareddocs "github.com/Manu343726/hammer/docs"
	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Rendering is the other half of reading a contract.
//
// The standard description is deliberately wider than any one format:
// it remembers what an OpenAPI document says about paths and statuses, and what a
// protobuf contract says about message shapes and streaming. Reading a contract
// keeps what protobuf can state; writing one has to decide, for every fact the
// description carries that protobuf cannot state, whether to lose it silently or
// say so. This file says so: every loss is a warning, and the warnings are part of
// the result rather than a log line a caller can miss.
//
// The one thing a rendered contract must never do is stop being readable by this
// package. A rendered contract is what a consumer feeds back into a reader, so a
// construct that reads back as something else — a JSON name that silently changes,
// a streaming direction that swaps sides — is worse than a construct that is
// reported as lost.

// renderOptions is the resolved form of RenderOptions.
type renderOptions struct {
	filename          string
	packageName       string
	goPackage         string
	includeDescriptor bool
	// primary is the media type of the file the caller asked to start from.
	primary string
}

// renderContract writes the contract and compiles it.
func renderContract(described api.API, options renderOptions) ([]File, []string, error) {
	if len(described.Services) == 0 {
		// A file with no service compiles and describes nothing, so a caller that
		// rendered one would publish an artifact no reader can register.
		return nil, nil, api.Errorf(api.KindInvalid, "api %q declares no service, so no contract can be rendered", described.ID)
	}
	r := &renderer{options: options, api: described, seen: make(map[string]bool)}
	files, err := r.contract()
	if err != nil {
		return nil, nil, err
	}
	sources := make(map[string]string, len(files))
	for _, file := range files {
		sources[file.path] = file.source()
	}
	descriptorSet, err := compileContract(sources)
	if err != nil {
		return nil, nil, err
	}
	rendered := make([]File, 0, len(files)+1)
	for _, file := range files {
		rendered = append(rendered, File{
			Name:      file.path,
			Content:   []byte(sources[file.path]),
			MediaType: MediaTypeContract,
		})
	}
	if options.includeDescriptor {
		rendered = append(rendered, File{
			Name:      descriptorSetName(files[0].path),
			Content:   descriptorSet,
			MediaType: MediaTypeDescriptorSet,
		})
	}
	return rendered, r.warnings, nil
}

// descriptorSetName names the compiled file after the contract it was compiled
// from, so a caller that writes both to a directory cannot mix up contracts.
func descriptorSetName(contract string) string {
	return strings.TrimSuffix(contract, "."+FileExtensionContract) + "." + FileExtensionDescriptorSet
}

// compileContract compiles rendered sources into one FileDescriptorSet.
//
// Compiling is part of rendering, not a check a test performs separately. A
// renderer whose output no compiler accepts produces an artifact a parser cannot
// read, and the caller who discovers that is the one holding the artifact. Doing it
// here also means the source info — and with it the comments and the
// @toolbox.side-effects annotations — is what the emitted descriptor set carries,
// so a contract read back from the .pb re-parses to the same facts as the .proto
// without a registry that knows its path.
//
// The rendered contract imports nothing, so a resolver that knows only these
// files is a complete resolver. That is a property worth keeping: a contract that
// imports well-known types cannot be parsed without the registry that holds them,
// and the point of rendering is to produce something self-contained.
func compileContract(sources map[string]string) ([]byte, error) {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	compiler := protocompile.Compiler{
		Resolver: &protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(sources),
		},
		SourceInfoMode: protocompile.SourceInfoStandard,
	}
	compiled, err := compiler.Compile(context.Background(), names...)
	if err != nil {
		return nil, api.WrapError(api.KindInvalid, err, "the rendered contract does not compile")
	}
	set := &descriptorpb.FileDescriptorSet{}
	for _, file := range compiled {
		set.File = append(set.File, protodesc.ToFileDescriptorProto(file))
	}
	encoded, err := proto.Marshal(set)
	if err != nil {
		return nil, api.WrapError(api.KindInternal, err, "encode the rendered descriptor set")
	}
	return encoded, nil
}

// renderer carries the state of one translation: the switches, the API being
// rendered, and the findings accumulated while walking it.
type renderer struct {
	options  renderOptions
	api      api.API
	warnings []string
	seen     map[string]bool

	// Counts of facts the description stated and protobuf has no spelling for.
	// They are counted rather than reported one by one because a contract of fifty
	// operations would otherwise bury every finding a reader has to act on under
	// fifty findings they cannot.
	paths, methods, statuses, parameters int
	security, deprecated, constraints    int
	declarations                         int
	presenceGained, presenceLost         int
}

func (r *renderer) warnf(format string, args ...any) {
	r.warnings = append(r.warnings, fmt.Sprintf(format, args...))
}

// warnOnce reports a finding whose cause is one value seen many times, such as a
// format identifier that repeats on every field of a message.
func (r *renderer) warnOnce(key, format string, args ...any) {
	if r.seen[key] {
		return
	}
	r.seen[key] = true
	r.warnf(format, args...)
}

// contract renders the API's services into one file per protobuf package.
//
// One file per package rather than one file for everything, because a file
// declares exactly one package and a service's package is part of its name: an API
// that describes services from two contracts has to keep both names, and folding
// them into one package would rename one of them.
func (r *renderer) contract() ([]*protoFile, error) {
	files := make([]*protoFile, 0, 1)
	index := make(map[string]*protoFile, 1)
	for _, service := range r.api.Services {
		pkg, name, err := r.serviceIdentity(service)
		if err != nil {
			return nil, err
		}
		file, ok := index[pkg]
		if !ok {
			file = &protoFile{
				path:        r.filePath(pkg, len(files)),
				packageName: pkg,
				goPackage:   r.options.goPackage,
				renderer:    r,
				scope:       newScope(pkg),
				byFullName:  make(map[string]*protoMessage),
			}
			index[pkg] = file
			files = append(files, file)
		}
		if err := r.buildService(file, name, service); err != nil {
			return nil, err
		}
	}
	// The API's own security is a description-wide fact, so it is counted once
	// rather than per operation: a contract of fifty methods would otherwise report
	// the same absence fifty times.
	if len(r.api.SecuritySchemes) > 0 || len(r.api.Security) > 0 {
		r.security++
	}
	if strings.TrimSpace(r.api.Version) != "" || len(r.api.DeclaredServers) > 0 {
		r.declarations++
	}
	files[0].header = r.header()
	r.flushFindings()
	return files, nil
}

// header is the prose a contract opens with. It is written above the syntax
// statement, where it documents the file instead of becoming the leading comment
// of the first declaration — a comment there would read back as that service's own
// description.
func (r *renderer) header() string {
	title := strings.TrimSpace(r.api.Title)
	if title == "" {
		title = strings.TrimSpace(r.api.Name)
	}
	if title == "" {
		title = strings.TrimSpace(r.api.ID)
	}
	description := strings.TrimSpace(r.api.Description)
	switch {
	case title == "" || title == description:
		return description
	case description == "":
		return title
	default:
		return title + "\n\n" + description
	}
}

// filePath names the file a package is rendered into.
func (r *renderer) filePath(pkg string, already int) string {
	if already == 0 {
		return r.options.filename
	}
	directory := strings.ReplaceAll(pkg, ".", "/")
	if directory == "" {
		directory = "unpackaged"
	}
	return path.Join(directory, r.options.filename)
}

// serviceIdentity splits a service name into the package it is declared in and the
// name it is declared under.
//
// A service read from a contract is named the way that contract names it, so the
// split is what keeps its full name — and with it every operation identifier
// derived from it — unchanged across a render. A service named without a package,
// as a tag group is, is rendered into a file with no package at all: declaring one
// would prefix the service and rename it.
func (r *renderer) serviceIdentity(service api.Service) (pkg, name string, err error) {
	declared := strings.TrimSpace(service.Name)
	if declared == "" {
		return "", "", api.Errorf(api.KindInvalid, "api %q has an unnamed service", r.api.ID)
	}
	pkg, name = "", declared
	if index := strings.LastIndex(declared, "."); index >= 0 {
		pkg, name = declared[:index], declared[index+1:]
	}
	if r.options.packageName != "" {
		pkg = r.options.packageName
	}
	sanitizedPackage := sanitizePackageName(pkg)
	sanitizedName := sanitizeIdentifier(name)
	if sanitizedPackage != pkg || sanitizedName != name {
		r.warnOnce("service:"+declared,
			"service %q is not a protobuf name; it was declared as %q, so the description's service name will not survive a re-parse",
			declared, qualifyName(sanitizedPackage, sanitizedName))
	}
	return sanitizedPackage, sanitizedName, nil
}

// buildService renders one service and its operations.
func (r *renderer) buildService(file *protoFile, name string, service api.Service) error {
	claimed := file.scope.claim(name)
	if claimed != name {
		r.warnOnce("service-collision:"+name,
			"service %q collides with another declaration in package %q; it was declared as %q",
			name, file.packageName, claimed)
	}
	built := &protoService{
		name:    claimed,
		comment: strings.TrimSpace(service.Description),
		scope:   newScope(file.packageName),
	}
	// A service may classify every method it declares at once, and a method that
	// declares nothing inherits that classification. Writing the service-level line
	// is therefore only legal when no method is relying on staying unclassified:
	// an operation nobody classified is the one state a policy cannot grant, so
	// convenience here would silently authorize what the description left unknown.
	serviceEffects := len(service.SideEffects) > 0
	if serviceEffects {
		for _, operation := range service.Operations {
			if len(operation.SideEffects) > 0 {
				continue
			}
			serviceEffects = false
			r.warnings = append(r.warnings, fmt.Sprintf(
				"service %q declares side effects %v, but operation %q declares none: protobuf makes a method inherit its service's declaration, so the effects were written per operation and the service's own classification was dropped",
				service.Name, service.SideEffects, operation.Name))
			break
		}
	}
	if serviceEffects {
		built.annotations = []string{r.sideEffects(service.SideEffects)}
	}
	for _, operation := range service.Operations {
		if err := r.buildMethod(file, built, service, operation, serviceEffects); err != nil {
			return err
		}
	}
	file.services = append(file.services, built)
	return nil
}

// sideEffects renders a side-effect declaration as the annotation line this
// package's own reader understands.
func (r *renderer) sideEffects(effects []api.SideEffect) string {
	values := make([]string, 0, len(effects))
	for _, effect := range effects {
		values = append(values, string(effect))
	}
	return annotationLine(api.AnnotationSideEffects, values)
}

// annotationLine writes one machine-readable comment line. The syntax belongs to
// the documentation parser, so it is built from that package's own constants
// rather than spelled here: two spellings of one annotation is how a renderer
// produces a contract its own reader does not understand.
func annotationLine(key string, values []string) string {
	line := shareddocs.AnnotationPrefix + key
	if len(values) > 0 {
		line += " " + strings.Join(values, " ")
	}
	return line
}

// buildMethod renders one operation as an rpc.
func (r *renderer) buildMethod(file *protoFile, service *protoService, declared api.Service, operation api.Operation, serviceEffects bool) error {
	name := strings.TrimSpace(operation.Name)
	if name == "" {
		return api.Errorf(api.KindInvalid, "service %q has an unnamed operation", service.name)
	}
	claimed := service.scope.claim(sanitizeIdentifier(name))
	if claimed != name {
		r.warnOnce("rpc:"+service.name+"/"+name,
			"operation %q is not a protobuf method name, or is already declared in %q; it was declared as %q, so the description's operation name will not survive a re-parse",
			name, service.name, claimed)
	}
	if strings.TrimSpace(operation.Path) != "" {
		r.paths++
	}
	if method := strings.TrimSpace(operation.Method); method != "" && !strings.EqualFold(method, name) {
		// A protobuf method is named, not addressed: its name is the whole of what
		// the transport needs, so an HTTP verb or a path is a fact the contract
		// cannot carry.
		r.methods++
	}
	if len(operation.Tags) > 0 || len(r.api.Tags) > 0 {
		r.warnOnce("tags", "the description declares tags for itself or its operations; a protobuf contract groups operations by service only, so they were dropped")
	}
	if len(operation.Security) > 0 {
		r.security++
	}
	if operation.Deprecated {
		r.deprecated++
	}
	if len(operation.Responses) > 0 {
		r.statuses++
	}
	request, err := r.requestMessage(file, claimed, operation)
	if err != nil {
		return err
	}
	response, err := r.responseMessage(file, claimed, operation)
	if err != nil {
		return err
	}
	method := &protoMethod{
		name:            claimed,
		request:         request,
		response:        response,
		clientStreaming: operation.Streaming.Client,
		serverStreaming: operation.Streaming.Server,
		deprecated:      operation.Deprecated,
	}
	annotations := make([]string, 0, 1)
	switch {
	case len(operation.SideEffects) == 0:
		// Nothing is written: an operation that declared no effect must read back as
		// unclassified, and any annotation here would classify it.
	case serviceEffects && equalEffects(operation.SideEffects, declared.SideEffects):
		// The service already says it, and protobuf makes this method inherit it.
	default:
		annotations = append(annotations, r.sideEffects(operation.SideEffects))
	}
	method.comment = commentFor(operation.Summary, operation.Description, annotations)
	service.methods = append(service.methods, method)
	return nil
}

// requestMessage renders the message an operation takes.
func (r *renderer) requestMessage(file *protoFile, rpc string, operation api.Operation) (string, error) {
	message, created := r.messageForValue(file, rpc+"Request", operation.Request)
	if len(operation.Parameters) == 0 {
		return file.printableName(message.fullName), nil
	}
	r.parameters++
	// Parameters may only be folded into a message this operation owns. A message
	// the description named is shared: a field added for one operation would appear
	// in every other operation that names the same shape, which is a defect a reader
	// of the rendered contract cannot even see.
	named := operation.Request != nil && strings.TrimSpace(operation.Request.Ref) != ""
	if !created || named {
		r.warnOnce("parameters-shared:"+rpc,
			"operation %q names a request message the contract already declares and also declares parameters; the parameters were declared beside it in a message of its own",
			operation.Name)
		wrapper := file.declareMessage(typeNameHint(rpc+"Request"), nil)
		r.appendField(wrapper, "request", operation.Request, 1, true)
		message = wrapper
	}
	for _, parameter := range operation.Parameters {
		r.appendField(message, parameter.Name, parameter.Schema, int32(len(message.fields)+1), parameter.Required)
	}
	return file.printableName(message.fullName), nil
}

// responseMessage renders the message an operation returns.
func (r *renderer) responseMessage(file *protoFile, rpc string, operation api.Operation) (string, error) {
	message, _ := r.messageForValue(file, rpc+"Response", operation.Response)
	return file.printableName(message.fullName), nil
}

// messageForValue returns the message that carries a whole operation value.
//
// protobuf gives an operation a message and not a value, so a description whose
// request or response is a scalar, an array, or a map has to be wrapped: dropping
// the value would make the operation unable to express what it takes at all.
func (r *renderer) messageForValue(file *protoFile, hint string, schema *api.Schema) (*protoMessage, bool) {
	if schema == nil {
		// protobuf does not allow an operation to take nothing, so an operation that
		// declared no request value gets an empty message rather than no input.
		return file.declareMessage(typeNameHint(hint), nil), true
	}
	if messageShaped(schema) {
		return r.messageFor(file, file, hint, schema)
	}
	r.warnOnce("wrapped:"+hint,
		"the value of %q is not an object; protobuf gives an operation a message, so it was wrapped in a field of a message",
		hint)
	message := file.declareMessage(typeNameHint(hint), schema)
	field := "value"
	if schema.Type == api.TypeArray {
		field = "items"
	}
	r.appendField(message, field, schema, 1, false)
	return message, true
}

// messageShaped reports whether a schema is a message already, rather than a value
// that has to be wrapped to become one.
func messageShaped(schema *api.Schema) bool {
	if schema == nil {
		return false
	}
	if len(schema.Properties) > 0 {
		return true
	}
	if schema.AdditionalProperties != nil {
		// An object with arbitrary keys is a protobuf map, and a map is a field type
		// rather than a message.
		return false
	}
	return schema.Type == api.TypeObject
}

// messageFor returns the message a schema is rendered as, declaring it when it is
// new.
//
// A schema that names itself keeps that name: the description already said what
// the shape is called, and that name is what the contract's own references use, so
// renaming it would make the rendered message disagree with every reference the
// description carries. A schema that names nothing is named after the context it
// appears in, which is the only name the description left to derive one from.
func (r *renderer) messageFor(file *protoFile, owner declarationScope, hint string, schema *api.Schema) (*protoMessage, bool) {
	if claimed, ok := r.claimedNameFor(file, schema); ok {
		if existing, found := file.byFullName[claimed.fullName]; found {
			return existing, false
		}
		message := file.declareMessage(claimed.name, schema)
		if message.fullName != claimed.fullName {
			// The name the reference asked for is already declared in this file by
			// something else, so the shape had to be renamed, and the reference will
			// read back as the renamed message.
			r.warnOnce("ref-taken:"+claimed.fullName,
				"the name %q a schema reference asked for is already declared in package %q; the shape was declared as %q instead",
				claimed.fullName, file.packageName, message.fullName)
		}
		return message, true
	}
	return owner.declareMessage(typeNameHint(hint), schema), true
}

// claimedNameFor resolves the name a schema claims through its reference.
//
// A reference names a type in the contract being rendered when it is a protobuf
// name in this file's package. Anything else — a JSON Schema pointer, a name from
// a package this file does not declare — is carried as the message its last
// segment names, because a field has to have a type and the shape is the thing the
// reference resolved to.
func (r *renderer) claimedNameFor(file *protoFile, schema *api.Schema) (claimedName, bool) {
	if schema == nil {
		return claimedName{}, false
	}
	reference := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(schema.Ref), "."))
	if reference == "" {
		return claimedName{}, false
	}
	prefix, last := "", reference
	// A reference is a protobuf name in the common case and a JSON Schema pointer
	// otherwise, so a segment boundary is either separator: only the last segment
	// names a type either way.
	if index := strings.LastIndexAny(reference, "./"); index >= 0 {
		prefix, last = reference[:index], reference[index+1:]
	}
	last = sanitizeIdentifier(last)
	full := qualifyName(file.packageName, last)
	if prefix != file.packageName || full != reference {
		r.warnOnce("ref:"+reference,
			"schema reference %q is not a name in package %q; it was declared as %q, so the reference will read back as that message",
			schema.Ref, file.packageName, full)
	}
	return claimedName{name: last, fullName: full}, true
}

// buildFields renders an object's members as a message's fields.
func (r *renderer) buildFields(message *protoMessage, schema *api.Schema) {
	if schema == nil {
		return
	}
	if hasConstraints(schema) {
		r.constraints++
	}
	for index, property := range schema.Properties {
		required := property.Required || schema.IsRequired(property.Name)
		r.appendField(message, property.Name, property.Schema, int32(index+1), required)
	}
}

// appendField renders one member as a field.
//
// The field name is the description's name in protobuf's spelling, and the
// description's name travels in json_name. Both are written explicitly because the
// reader names a property by its JSON name: leaving the JSON name implicit means
// deriving it from a field name that was itself derived from it, and a description
// whose property is not lowerCamelCase would read back under a different name.
func (r *renderer) appendField(message *protoMessage, name string, schema *api.Schema, number int32, required bool) {
	jsonName := strings.TrimSpace(name)
	if jsonName == "" {
		jsonName = fmt.Sprintf("field_%d", number)
	}
	field := &protoField{
		name:     message.scope.claim(protoFieldName(jsonName)),
		jsonName: jsonName,
		number:   number,
		comment:  schemaComment(schema),
	}
	field.deprecated = schema != nil && schema.Deprecated
	if field.deprecated {
		r.deprecated++
	}
	if schema != nil && hasConstraints(schema) {
		r.constraints++
	}
	field.typ, field.repeated = r.typeFor(message, field.name, schema)
	switch {
	case required && (field.repeated || field.typ.mapType):
		// A repeated or map field has no presence in proto3, so nothing can make it
		// required again.
		r.presenceLost++
	case required && !field.typ.message:
		field.optional = true
	case !required && !field.repeated && !field.typ.mapType && field.typ.message:
		// A message field always has presence, which the reader reports as required.
		r.presenceGained++
	}
	message.fields = append(message.fields, field)
}

// typeFor renders a value as a protobuf type, reporting whether it repeats.
func (r *renderer) typeFor(owner declarationScope, hint string, schema *api.Schema) (renderType, bool) {
	if schema == nil {
		return renderType{name: "string"}, false
	}
	switch {
	case schema.Type == api.TypeArray:
		element := schema.Items
		if element == nil {
			r.warnOnce("array:"+hint, "array %q declares no element schema; its elements were rendered as strings", hint)
			element = api.StringSchema()
		}
		typ, repeated := r.typeFor(owner, hint, element)
		if repeated || typ.mapType {
			// repeated of repeated and repeated of map are not protobuf types. The
			// element keeps its shape inside a message, so the contract still says
			// what the array holds.
			typ = renderType{name: r.wrapValue(owner, hint, element), message: true}
		}
		return typ, true

	case messageShaped(schema):
		if len(schema.OneOf) > 0 || len(schema.AnyOf) > 0 {
			// An object that also declares alternatives states two shapes, and only
			// one of them can be written. The members were written, so the
			// alternatives are what the contract lost.
			r.warnOnce("union-object:"+hint,
				"value %q declares both members and alternatives; the members were rendered as a message and the alternatives were dropped",
				hint)
		}
		message, _ := r.messageFor(owner.fileOf(), owner, hint, schema)
		return renderType{name: owner.fileOf().printableName(message.fullName), message: true}, false

	case schema.AdditionalProperties != nil:
		value, repeated := r.typeFor(owner, hint, schema.AdditionalProperties)
		if repeated || value.mapType {
			// A map's value cannot itself repeat or be a map.
			value = renderType{name: r.wrapValue(owner, hint+"Value", schema.AdditionalProperties), message: true}
		}
		return renderType{name: "map<string, " + value.name + ">", mapType: true}, false

	case schema.AdditionalPropertiesAllowed:
		// protobuf has no free-form object: every field is declared. The message is
		// empty rather than absent, so the field still exists and still has a type.
		r.warnOnce("free-form:"+hint,
			"object %q accepts arbitrary members; protobuf declares every field, so it was rendered as an empty message",
			hint)
		message, _ := r.messageFor(owner.fileOf(), owner, hint, schema)
		return renderType{name: owner.fileOf().printableName(message.fullName), message: true}, false

	case len(schema.OneOf) > 0 || len(schema.AnyOf) > 0:
		// protobuf has oneof, and a oneof is not a union a reader can express: every
		// member of a oneof is an independently named field, and this package's
		// reader reports all of them present. Rendering one would claim every
		// alternative is required, so the first alternative is rendered instead and
		// the rest are reported.
		alternatives := schema.OneOf
		if len(alternatives) == 0 {
			alternatives = schema.AnyOf
		}
		r.warnOnce("union:"+hint,
			"value %q declares %d alternatives; protobuf has no union a reader can express, so only the first alternative was rendered and %d were dropped",
			hint, len(alternatives), len(alternatives)-1)
		return r.typeFor(owner, hint, alternatives[0])

	case len(schema.Enum) > 0:
		return renderType{name: r.enumFor(owner, hint, schema).name}, false
	}
	switch schema.Type {
	case api.TypeBoolean:
		return renderType{name: "bool"}, false
	case api.TypeInteger:
		if isUnsigned(schema.Format) {
			return renderType{name: "uint64"}, false
		}
		r.warnFormat(schema.Format, "integer", "int64")
		return renderType{name: "int64"}, false
	case api.TypeNumber:
		r.warnFormat(schema.Format, "number", "double")
		return renderType{name: "double"}, false
	case api.TypeString:
		if api.NormalizeIdentifier(schema.Format) == "base64" {
			return renderType{name: "bytes"}, false
		}
		r.warnFormat(schema.Format, "string", "string")
		return renderType{name: "string"}, false
	case api.TypeNull:
		r.warnOnce("null:"+hint, "value %q is JSON null; protobuf has no null type, so it was rendered as a string", hint)
		return renderType{name: "string"}, false
	default:
		r.warnOnce("unknown:"+hint, "value %q declares no type protobuf can carry; it was rendered as a string", hint)
		return renderType{name: "string"}, false
	}
}

// warnFormat reports a refinement protobuf has no type for.
//
// A format is what tells a caller that a string is a date and an integer is an
// identifier rather than a count. protobuf spells a few of them and nothing else,
// so the ones it does not are carried by the wider type and reported: a consumer
// that reads the rendered contract has to know the calendar was dropped.
func (r *renderer) warnFormat(format, kind, rendered string) {
	normalized := api.NormalizeIdentifier(format)
	if normalized == "" || knownFormat(kind, normalized) {
		return
	}
	r.warnOnce("format:"+kind+":"+normalized,
		"format %q of a %s value has no protobuf type; it was rendered as %s and the refinement was dropped",
		format, kind, rendered)
}

// knownFormat reports whether a format is the protobuf spelling of its own type,
// so rendering it is not a loss.
func knownFormat(kind, format string) bool {
	switch kind {
	case "integer":
		switch format {
		case "int32", "int64", "sint32", "sint64", "uint32", "uint64", "fixed32", "fixed64":
			return true
		}
	case "number":
		return format == "double" || format == "float"
	case "string":
		return format == "base64" || format == "bytes"
	}
	return false
}

func isUnsigned(format string) bool {
	switch api.NormalizeIdentifier(format) {
	case "uint32", "uint64", "fixed32", "fixed64":
		return true
	default:
		return false
	}
}

// hasConstraints reports whether a schema states a bound protobuf cannot.
func hasConstraints(schema *api.Schema) bool {
	return schema.Minimum != nil || schema.Maximum != nil || schema.MinLength != nil ||
		schema.MaxLength != nil || schema.Pattern != "" || schema.Default != nil || schema.ReadOnly
}

// wrapValue declares the message that holds a value a protobuf field cannot carry
// directly, and answers with the type a field can name.
func (r *renderer) wrapValue(owner declarationScope, hint string, schema *api.Schema) string {
	message := owner.declareMessage(typeNameHint(hint)+"Entry", nil)
	r.appendField(message, "value", schema, 1, false)
	return owner.fileOf().printableName(message.fullName)
}

// enumFor renders a value's allowed literals as an enum.
//
// protobuf enum values are declared in the scope that contains the enum, not
// inside it, so two enums of one message cannot share a value name even when the
// description's two enumerations do. A name protobuf refuses is renamed and
// reported rather than dropped, because the literal is what the value means.
func (r *renderer) enumFor(owner declarationScope, hint string, schema *api.Schema) *protoEnum {
	values := make([]string, 0, len(schema.Enum))
	seen := make(map[string]bool, len(schema.Enum))
	for _, value := range schema.Enum {
		if seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	enum := &protoEnum{comment: schemaComment(schema)}
	enum.name = owner.scopeOf().claim(typeNameHint(hint))
	for _, value := range values {
		claimed := owner.scopeOf().claim(enumValueName(value))
		if claimed != value {
			r.warnOnce("enum-value:"+value,
				"enum value %q is not a name protobuf can declare here, or is already declared in this scope; it was rendered as %q",
				value, claimed)
		}
		enum.values = append(enum.values, claimed)
	}
	owner.addEnum(enum)
	return enum
}

// flushFindings reports the facts the description stated and the contract cannot.
func (r *renderer) flushFindings() {
	if r.paths > 0 {
		r.warnf("a protobuf method is named rather than addressed: the request path declared by %s was dropped",
			counted(r.paths, "operation", "operations"))
	}
	if r.methods > 0 {
		r.warnf("a protobuf method's name is the whole of what its transport needs: the transport method declared by %s was dropped",
			counted(r.methods, "operation", "operations"))
	}
	if r.parameters > 0 {
		r.warnf("a protobuf operation takes one request value, so the parameters of %s were folded into its request message and where each one is carried was dropped",
			counted(r.parameters, "operation", "operations"))
	}
	if r.statuses > 0 {
		r.warnf("%s documented responses by status; a protobuf method has one response value, so the statuses and the descriptions of the other responses were dropped",
			counted(r.statuses, "operation", "operations"))
	}
	if r.security > 0 {
		r.warnf("the description declares security; a protobuf contract has no credential scheme, so authentication was dropped")
	}
	if r.declarations > 0 {
		r.warnf("the description declares a version or servers for the API; a protobuf contract has no field for either, so both were dropped, and the title and description were written as a file comment the reader does not read back")
	}
	if r.deprecated > 0 {
		r.warnf("protobuf's deprecated option was written for %s, but this package's reader does not read it back",
			counted(r.deprecated, "declaration", "declarations"))
	}
	if r.constraints > 0 {
		r.warnf("bounds, lengths, patterns, defaults, and read-only marks declared on %s were dropped, because protobuf has no spelling for any of them",
			counted(r.constraints, "schema", "schemas"))
	}
	if r.presenceGained > 0 {
		// The reader reports presence as required, which is a rule of the reader and
		// not a fact the description stated — so the loss is measured against what
		// the contract can say, and reported.
		r.warnf("protobuf gives every message field presence, so an object-valued property that is not required reads back as required (%d properties)",
			r.presenceGained)
	}
	if r.presenceLost > 0 {
		r.warnf("a repeated or map field has no presence in proto3, so a required property of either kind reads back as not required (%d properties)",
			r.presenceLost)
	}
}

// counted spells a count with the noun it counts, so a finding about one operation
// does not read as a finding about many.
func counted(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}

// commentFor assembles the comment lines of a declaration: the prose the reader
// will see, then the machine-readable lines it must see.
func commentFor(summary, description string, annotations []string) string {
	paragraphs := make([]string, 0, 3)
	if summary = strings.TrimSpace(summary); summary != "" {
		paragraphs = append(paragraphs, summary)
	}
	if description = strings.TrimSpace(description); description != "" && description != summary {
		paragraphs = append(paragraphs, description)
	}
	if len(annotations) > 0 {
		paragraphs = append(paragraphs, strings.Join(annotations, "\n"))
	}
	return strings.Join(paragraphs, "\n\n")
}

func schemaComment(schema *api.Schema) string {
	if schema == nil {
		return ""
	}
	return strings.TrimSpace(schema.Description)
}

func equalEffects(left, right []api.SideEffect) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
