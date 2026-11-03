package cli

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	api "github.com/Manu343726/hammer"
	"github.com/spf13/cobra"
)

// A flag is a value the description declares, and the adapter's job in turning one
// into the other is to keep the description's own words: the flag's name is the
// parameter's or the member's name, and its help is that member's comment.
//
// What a flag carries is JSON, because the call carries JSON. A number is a number,
// a boolean is a boolean, and an array is an array without an encoding of its own
// to get wrong — no protobuf runtime, no wire type, no schema registry.

// maxFlagDepth bounds how deep a nested object is flattened into dotted flags.
//
// Each level a caller has to hand-write is a level where a misspelled member is
// reported by the server as "unknown field" rather than by the command as a flag
// that does not exist, so nested objects are flattened too. The bound is what stops
// a schema that contains itself from walking forever; a schema already on the path
// stops the walk sooner than the bound would.
const maxFlagDepth = 4

// reservedFlags are the names the command owns, so a description that declares an
// input with one of them gets a qualified flag rather than shadowing it. "help" is
// cobra's, and taking it would cost the caller -h; "server" is where calls go, and
// taking it would hide the one input a call cannot do without.
var reservedFlags = map[string]bool{"help": true, "server": true}

// carrier is how one flag carries its value.
type carrier uint8

const (
	carrierString carrier = iota
	carrierBool
	carrierInteger
	carrierNumber
	// The repeatable carriers take one element per occurrence, so an array is
	// spelled --tag one --tag two. StringArray rather than StringSlice on purpose:
	// a slice splits its value on commas, and an element is allowed to contain one.
	carrierStrings
	carrierBools
	carrierIntegers
	carrierNumbers
	// carrierJSON carries one whole JSON document, for a value whose members the
	// transport has no field-by-field shape for.
	carrierJSON
)

// repeated reports whether the flag is given once per element.
func (c carrier) repeated() bool {
	switch c {
	case carrierStrings, carrierBools, carrierIntegers, carrierNumbers:
		return true
	default:
		return false
	}
}

// shape is the JSON shape of a value, in the terms a flag cares about.
type shape uint8

const (
	shapeString shape = iota
	shapeBool
	shapeInteger
	shapeNumber
	shapeArray
	shapeObject
	shapeJSON
)

func shapeOf(schema *api.Schema) shape {
	if schema == nil {
		return shapeString
	}
	switch schema.Type {
	case api.TypeObject:
		return shapeObject
	case api.TypeArray:
		return shapeArray
	case api.TypeBoolean:
		return shapeBool
	case api.TypeInteger:
		return shapeInteger
	case api.TypeNumber:
		return shapeNumber
	case api.TypeString:
		return shapeString
	case api.TypeNull:
		return shapeJSON
	}
	// A description may state a shape without stating the JSON type — a schema
	// that lists its members, a value that is one of several alternatives. The
	// members are the more specific fact, and an alternative is a document because
	// no single flag can address it.
	switch {
	case len(schema.Properties) > 0:
		return shapeObject
	case schema.Items != nil:
		return shapeArray
	case len(schema.OneOf) > 0 || len(schema.AnyOf) > 0:
		return shapeJSON
	default:
		return shapeString
	}
}

// input is one value a flag supplies, and where it belongs in the arguments object
// the invoker receives.
type input struct {
	// flag is the name the caller types. It is assigned after every input of one
	// operation is known, because whether a name is usable depends on the others.
	flag string
	// preferred is the name the description gives the value, spelled the way the
	// caller reads the contract.
	preferred string
	// parameter is the top-level argument key when the value is a declared
	// parameter. It is empty when the value is part of the request body.
	parameter string
	// location is where a declared parameter is carried.
	location api.ParameterIn
	// path is the member path inside the request body value.
	path []string
	// whole states that the value is the request body itself rather than a member
	// of it.
	whole    bool
	schema   *api.Schema
	summary  string
	required bool
	// deprecated marks a value the description says should not be used for new
	// work.
	deprecated bool
	carrier    carrier
}

// operationCommand builds the command for one operation: its flags, its help, and
// what running it does.
func (b *builder) operationCommand(operation api.Operation, name string) (*cobra.Command, error) {
	command := &cobra.Command{
		Use:   name,
		Short: operationShort(operation),
		Long:  operationLong(operation),
		Args:  cobra.NoArgs,
	}
	inputs, err := b.inputs(operation)
	if err != nil {
		return nil, err
	}
	assignInputNames(inputs, b.reserved)
	for _, in := range inputs {
		in.register(command)
	}
	run := &operationRun{
		description: b.description,
		server:      b.server,
		invoker:     b.invoker,
		operation:   operation,
		inputs:      inputs,
	}
	if operation.Streaming.Streaming() {
		// The command exists and takes its flags, so its help documents what the
		// operation takes — for an operation whose only interesting input is the
		// stream, that is the whole contract. Calling it says which operation
		// cannot be invoked, rather than pretending it worked.
		command.Annotations = map[string]string{"hammer.streaming": "true"}
		command.RunE = func(*cobra.Command, []string) error {
			return api.Errorf(api.KindUnsupported,
				"operation %q streams, and a command line invokes unary operations only", operation.ID)
		}
		return command, nil
	}
	command.RunE = run.run
	return command, nil
}

// inputs is every flag the operation's description calls for.
//
// The order is the description's own: the declared parameters first, because they
// identify what the operation acts on, then the request value's members.
func (b *builder) inputs(operation api.Operation) ([]input, error) {
	var inputs []input
	for _, parameter := range operation.Parameters {
		name := strings.TrimSpace(parameter.Name)
		if name == "" {
			return nil, api.Errorf(api.KindInvalid,
				"operation %q declares a parameter with no name", operation.Name)
		}
		spec := member{
			preferred:  flagName(name),
			schema:     parameter.Schema,
			summary:    parameter.Description,
			required:   parameter.Required,
			deprecated: parameter.Deprecated,
		}
		if api.NormalizeIdentifier(parameter.In) == api.ParameterInBody {
			// A parameter the description carries in the body is a member of the
			// request value, so it is composed into the body like a property rather
			// than being a top-level argument of its own.
			spec.path = []string{name}
			if err := b.collect(&inputs, spec, nil); err != nil {
				return nil, err
			}
			continue
		}
		// Anything else is carried beside the body, so it is one argument of its
		// own. A value that is not a scalar travels as JSON, because one flag cannot
		// address the members of a value the transport has no fields for.
		inputs = append(inputs, input{
			preferred:  spec.preferred,
			parameter:  name,
			location:   parameter.In,
			schema:     parameter.Schema,
			summary:    parameter.Description,
			required:   parameter.Required,
			deprecated: parameter.Deprecated,
			carrier:    carrierFor(parameter.Schema),
		})
	}
	request := operation.Request
	if request == nil {
		return inputs, nil
	}
	if shapeOf(request) != shapeObject || len(request.Properties) == 0 {
		// A request value with no members to flag — a scalar, a list, an object of
		// undeclared shape — is the one case where the body is one flag.
		inputs = append(inputs, input{
			preferred: "body",
			whole:     true,
			schema:    request,
			summary:   wholeBodySummary(request),
			carrier:   carrierFor(request),
		})
		return inputs, nil
	}
	// The whole value stays addressable as one document. It is declared before its
	// members, so applying it first lets a dotted flag override the member it
	// names and the rest of the document survive.
	inputs = append(inputs, input{
		preferred: "body",
		whole:     true,
		schema:    request,
		summary:   wholeBodySummary(request),
		carrier:   carrierJSON,
	})
	visited := []*api.Schema{request}
	for _, property := range request.Properties {
		name := strings.TrimSpace(property.Name)
		if name == "" {
			return nil, api.Errorf(api.KindInvalid,
				"operation %q declares a request member with no name", operation.Name)
		}
		if property.Schema != nil && property.Schema.ReadOnly {
			// The description says the value is returned but not accepted, so
			// offering a flag for it would promise an input the operation ignores.
			continue
		}
		spec := member{
			preferred:  flagName(name),
			path:       []string{name},
			schema:     property.Schema,
			summary:    schemaDescription(property.Schema),
			required:   property.Required || request.IsRequired(name),
			deprecated: schemaDeprecated(property.Schema),
			depth:      0,
		}
		if err := b.collect(&inputs, spec, visited); err != nil {
			return nil, err
		}
	}
	return inputs, nil
}

// member is one value inside the request body: a property of the request schema, a
// property of a nested object, or a parameter the description carries in the body.
type member struct {
	preferred  string
	path       []string
	schema     *api.Schema
	summary    string
	required   bool
	deprecated bool
	// depth counts the object levels above this member.
	depth int
}

// collect appends the flag, or the flags, for one member of the request value.
//
// A nested object is flattened as well as kept whole, so a caller who needs one
// member does not have to reproduce a document they cannot see. A repeated object
// is not flattened: no flag addresses --sources.0.id, so a dotted flag there would
// be a lie about which element it sets.
func (b *builder) collect(inputs *[]input, spec member, visited []*api.Schema) error {
	if shapeOf(spec.schema) == shapeObject && flattenable(spec.schema, spec.depth, visited) {
		*inputs = append(*inputs, input{
			preferred:  spec.preferred,
			path:       spec.path,
			schema:     spec.schema,
			summary:    spec.summary,
			required:   spec.required,
			deprecated: spec.deprecated,
			carrier:    carrierJSON,
		})
		nested := append(append([]*api.Schema(nil), visited...), spec.schema)
		for _, property := range spec.schema.Properties {
			name := strings.TrimSpace(property.Name)
			if name == "" {
				return api.Errorf(api.KindInvalid, "a request member has no name")
			}
			if property.Schema != nil && property.Schema.ReadOnly {
				continue
			}
			if err := b.collect(inputs, member{
				preferred:  spec.preferred + "." + flagName(name),
				path:       append(append([]string(nil), spec.path...), name),
				schema:     property.Schema,
				summary:    schemaDescription(property.Schema),
				required:   property.Required || spec.schema.IsRequired(name),
				deprecated: schemaDeprecated(property.Schema),
				depth:      spec.depth + 1,
			}, nested); err != nil {
				return err
			}
		}
		return nil
	}
	*inputs = append(*inputs, input{
		preferred:  spec.preferred,
		path:       spec.path,
		schema:     spec.schema,
		summary:    spec.summary,
		required:   spec.required,
		deprecated: spec.deprecated,
		carrier:    carrierFor(spec.schema),
	})
	return nil
}

// flattenable reports whether an object's members can be addressed by flags.
func flattenable(schema *api.Schema, depth int, visited []*api.Schema) bool {
	if schema == nil || len(schema.Properties) == 0 || depth >= maxFlagDepth {
		return false
	}
	for _, seen := range visited {
		if seen == schema {
			return false
		}
	}
	return true
}

// carrierFor is how one value is spelled on the command line: a typed flag for a
// scalar, a repeatable flag for a list of scalars, and one JSON document for
// anything whose members a flag cannot address.
func carrierFor(schema *api.Schema) carrier {
	switch shapeOf(schema) {
	case shapeBool:
		return carrierBool
	case shapeInteger:
		return carrierInteger
	case shapeNumber:
		return carrierNumber
	case shapeArray:
		if element := elementCarrier(schema.Items); element != carrierJSON {
			return element
		}
		return carrierJSON
	case shapeObject, shapeJSON:
		return carrierJSON
	default:
		return carrierString
	}
}

func elementCarrier(items *api.Schema) carrier {
	switch shapeOf(items) {
	case shapeBool:
		return carrierBools
	case shapeInteger:
		return carrierIntegers
	case shapeNumber:
		return carrierNumbers
	case shapeString:
		return carrierStrings
	default:
		return carrierJSON
	}
}

// assignInputNames gives every input of one operation the name a caller types.
//
// The short name is kept where it is unambiguous. Where two inputs claim one name —
// a query parameter and a request member are both called "id", which a real
// description does all the time — neither keeps it bare: the parameter gains the
// location it travels in and the member gains the body it belongs to. Qualifying
// only the second claimant would make which one is short depend on the order the
// description happened to declare them in.
//
// reserved is the set of names the tree itself owns, which is why it is a
// parameter rather than the package-level set: a tree that reads no configuration
// file does not own --config, and a description may keep that word.
func assignInputNames(inputs []input, reserved map[string]bool) {
	claims := make(map[string]int, len(inputs))
	for _, in := range inputs {
		claims[in.preferred]++
	}
	for i := range inputs {
		in := &inputs[i]
		if claims[in.preferred] == 1 && !reserved[in.preferred] {
			in.flag = in.preferred
			continue
		}
		in.flag = in.qualifiedName()
	}
	// A qualified name can still be claimed twice when one member's path spells
	// another input's name — a body member "query.id" beside a query parameter
	// "id". Registration order breaks the tie, so the second stays reachable
	// instead of pflag refusing to register it.
	taken := make(map[string]bool, len(inputs))
	for i := range inputs {
		name := inputs[i].flag
		if !taken[name] {
			taken[name] = true
			continue
		}
		for suffix := 2; ; suffix++ {
			candidate := name + "-" + strconv.Itoa(suffix)
			if !taken[candidate] {
				inputs[i].flag = candidate
				taken[candidate] = true
				break
			}
		}
	}
}

func (in input) qualifiedName() string {
	switch {
	case in.whole:
		return "body"
	case in.parameter != "":
		location := api.NormalizeIdentifier(in.location)
		if location == "" {
			location = "parameter"
		}
		return location + "." + in.preferred
	default:
		return "body." + strings.Join(in.path, ".")
	}
}

// register declares the flag, with the help the caller reads before typing it.
func (in input) register(command *cobra.Command) {
	usage := in.usage()
	switch in.carrier {
	case carrierBool:
		command.Flags().Bool(in.flag, in.boolDefault(), usage)
	case carrierInteger:
		command.Flags().Int64(in.flag, in.integerDefault(), usage)
	case carrierNumber:
		command.Flags().Float64(in.flag, in.numberDefault(), usage)
	case carrierJSON:
		command.Flags().String(in.flag, "", usage)
	default:
		if in.carrier.repeated() {
			command.Flags().StringArray(in.flag, in.listDefault(), usage)
			return
		}
		command.Flags().String(in.flag, in.stringDefault(), usage)
	}
}

// declaredDefault is the default the description declares for a value, and nil
// when it declares none.
//
// A flag's default is the description's and nothing else: a generator that
// invented one would state a value the description never made, which is the same
// defect as inventing a sentence for an undocumented member. What the default is
// not is what a call sends — a flag a caller did not set sends nothing, so an
// operation's declared default stays the server's to apply. The default is what
// the help shows, and what the configuration layer resolves a file and the
// environment against.
func (in input) declaredDefault() any {
	if in.schema == nil {
		return nil
	}
	return in.schema.Default
}

func (in input) boolDefault() bool {
	value, _ := in.declaredDefault().(bool)
	return value
}

func (in input) integerDefault() int64 {
	switch value := in.declaredDefault().(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		// A description parsed from a document states a whole number as a float.
		if value == math.Trunc(value) {
			return int64(value)
		}
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return parsed
		}
	case string:
		if parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
			return parsed
		}
	}
	return 0
}

func (in input) numberDefault() float64 {
	switch value := in.declaredDefault().(type) {
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case float64:
		return value
	case json.Number:
		if parsed, err := value.Float64(); err == nil {
			return parsed
		}
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
			return parsed
		}
	}
	return 0
}

func (in input) stringDefault() string {
	value, _ := in.declaredDefault().(string)
	return value
}

func (in input) listDefault() []string {
	elements, ok := in.declaredDefault().([]any)
	if !ok {
		if elements, ok := in.declaredDefault().([]string); ok {
			return elements
		}
		return nil
	}
	values := make([]string, 0, len(elements))
	for _, element := range elements {
		values = append(values, elementText(element))
	}
	return values
}

// elementText renders one declared element as the word a flag parses, without
// deciding for the flag what type the element is: that is the flag's own parser
// and the description's schema, not this.
func elementText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

// usage is a flag's help: the description's own comment, then the facts a caller
// needs to type it correctly.
//
// A member the description does not document falls back to its type, which reads as
// a gap in the description rather than as documentation. Inventing a sentence
// there would hide the gap, and the gap is worth fixing in the description.
func (in input) usage() string {
	kind := in.typeWord()
	text := flatten(in.summary)
	if text == "" {
		text = kind
	}
	notes := make([]string, 0, 6)
	if in.required {
		notes = append(notes, "required")
	}
	if in.deprecated {
		notes = append(notes, "deprecated")
	}
	if list := enumList(in.schema); list != "" && in.carrier == carrierString {
		notes = append(notes, "one of: "+list)
	} else if list := enumList(itemsOf(in.schema)); list != "" && in.carrier == carrierStrings {
		notes = append(notes, "one of: "+list)
	}
	if in.carrier.repeated() {
		notes = append(notes, "repeatable")
	}
	if text != kind && in.carrier == carrierJSON {
		notes = append(notes, kind)
	}
	if location := api.NormalizeIdentifier(in.location); location != "" {
		notes = append(notes, location)
	}
	if len(notes) > 0 {
		text += " (" + strings.Join(notes, "; ") + ")"
	}
	return text
}

func (in input) typeWord() string {
	switch in.carrier {
	case carrierBool:
		return "boolean"
	case carrierInteger:
		return "integer"
	case carrierNumber:
		return "number"
	case carrierStrings:
		return "list of strings"
	case carrierBools:
		return "list of booleans"
	case carrierIntegers:
		return "list of integers"
	case carrierNumbers:
		return "list of numbers"
	case carrierJSON:
		switch shapeOf(in.schema) {
		case shapeObject:
			return "JSON object"
		case shapeArray:
			return "JSON array"
		default:
			return "JSON value"
		}
	default:
		return "string"
	}
}

func enumList(schema *api.Schema) string {
	if schema == nil {
		return ""
	}
	return strings.Join(schema.Enum, ", ")
}

// itemsOf is an array schema's element schema, and nil for anything else. The
// element schema is where an array's own facts live: its enum, its type, whether one
// value is a document.
func itemsOf(schema *api.Schema) *api.Schema {
	if schema == nil || shapeOf(schema) != shapeArray {
		return nil
	}
	return schema.Items
}

// read returns the flag's value in the shape the call carries it.
func (in input) read(command *cobra.Command) (any, error) {
	switch in.carrier {
	case carrierBool:
		return command.Flags().GetBool(in.flag)
	case carrierInteger:
		return command.Flags().GetInt64(in.flag)
	case carrierNumber:
		return command.Flags().GetFloat64(in.flag)
	case carrierJSON:
		raw, err := command.Flags().GetString(in.flag)
		if err != nil {
			return nil, err
		}
		return decodeJSON(raw, shapeOf(in.schema))
	case carrierStrings, carrierBools, carrierIntegers, carrierNumbers:
		raw, err := command.Flags().GetStringArray(in.flag)
		if err != nil {
			return nil, err
		}
		values := make([]any, 0, len(raw))
		for _, element := range raw {
			value, err := in.readElement(element)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	default:
		raw, err := command.Flags().GetString(in.flag)
		if err != nil {
			return nil, err
		}
		if err := checkEnum(in.schema, raw); err != nil {
			return nil, err
		}
		return raw, nil
	}
}

func (in input) readElement(raw string) (any, error) {
	switch in.carrier {
	case carrierBools:
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not a boolean", raw)
		}
		return value, nil
	case carrierIntegers:
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", raw)
		}
		return value, nil
	case carrierNumbers:
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", raw)
		}
		return value, nil
	default:
		if in.schema != nil {
			if err := checkEnum(in.schema.Items, raw); err != nil {
				return nil, err
			}
		}
		return raw, nil
	}
}

// decodeJSON reads a JSON document a caller typed.
//
// Numbers are kept as their literal text rather than as a float64, so an identifier
// written as 123456789012345678 comes back out as the number the caller typed
// instead of one a float happened to round it to.
func decodeJSON(raw string, expected shape) (any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("expected a JSON value, got an empty flag")
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, fmt.Errorf("is not valid JSON")
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	switch expected {
	case shapeObject:
		if _, ok := value.(map[string]any); !ok {
			return nil, fmt.Errorf("expected a JSON object, got %s", jsonWord(value))
		}
	case shapeArray:
		if _, ok := value.([]any); !ok {
			return nil, fmt.Errorf("expected a JSON array, got %s", jsonWord(value))
		}
	}
	return value, nil
}

func jsonWord(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case json.Number:
		return "a number"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	default:
		return "a value"
	}
}

// checkEnum refuses a value the description does not list for the member.
//
// The local check is worth having because the refusal names the allowed values in
// the same breath, whereas the server's refusal arrives after a round trip and in
// the transport's own words. Only string values are checked: another type's enum
// is a list of spellings, and comparing a caller's "3.0" against a declared "3"
// would refuse a value the description accepts.
func checkEnum(schema *api.Schema, raw string) error {
	if schema == nil || len(schema.Enum) == 0 {
		return nil
	}
	for _, allowed := range schema.Enum {
		if allowed == raw {
			return nil
		}
	}
	return fmt.Errorf("%q is not one of %s", raw, enumList(schema))
}

// flagName is the word a member's flag is registered under. The description's own
// spelling is kept — a caller who reads the contract sees the same word — but
// whitespace becomes a hyphen, because a shell splits a flag name at it.
func flagName(name string) string {
	trimmed := strings.TrimSpace(name)
	word := make([]rune, 0, len(trimmed))
	for _, r := range trimmed {
		switch r {
		case ' ', '\t', '\n', '\r':
			if len(word) > 0 && word[len(word)-1] != '-' {
				word = append(word, '-')
			}
		default:
			word = append(word, r)
		}
	}
	return strings.Trim(string(word), "-")
}

// wholeBodySummary documents the one flag the adapter adds rather than takes from
// the description: the request value as a whole. The description's own comment for
// the value is used when it has one, because it is the better sentence.
func wholeBodySummary(schema *api.Schema) string {
	if summary := schemaDescription(schema); summary != "" {
		return summary
	}
	return "The whole request value."
}

// schemaDescription is a schema's comment, and empty for a value the description
// declared no shape for. A nil schema is a real state: a parameter or property may
// state nothing about its value at all.
func schemaDescription(schema *api.Schema) string {
	if schema == nil {
		return ""
	}
	return schema.Description
}

func schemaDeprecated(schema *api.Schema) bool {
	return schema != nil && schema.Deprecated
}
