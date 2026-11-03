package protocontract_test

import (
	"strings"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/protocontract"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rendering is the half of the format that has to decide what to lose. The
// package's promise is that every fact protobuf cannot state is reported as a
// warning rather than dropped silently, so each test below is written from the
// finding it must produce: a description that states the fact, a render, and an
// assertion that the warning names the loss.
//
// "It compiled" is not the whole property under test — a green compile would hide a
// description that lost a fact — so each render is checked twice: for the warning
// that names the loss, and (through Render, which compiles) for an artifact a parser
// still accepts.

// covService wraps operations in one service with a package-qualified name.
func covService(name string, operations ...api.Operation) api.Service {
	return api.Service{Name: name, Operations: operations}
}

// covRender renders a description and fails the test when it cannot.
//
// Render compiles the source it writes, so a successful call is also the proof that
// the artifact these tests read is one a parser accepts: a warning about a dropped
// path is worth nothing if the file carrying it cannot be compiled.
func covRender(t *testing.T, described api.API, options protocontract.RenderOptions) protocontract.Rendered {
	t.Helper()
	rendered, err := protocontract.Render(described, options)
	require.NoError(t, err)
	return rendered
}

// covSource returns the rendered contract's source.
func covSource(t *testing.T, rendered protocontract.Rendered) string {
	t.Helper()
	require.NotEmpty(t, rendered.Files)
	require.Equal(t, protocontract.MediaTypeContract, rendered.Files[0].MediaType)
	return string(rendered.Files[0].Content)
}

// covWarn fails unless some warning contains the fragment, and returns it.
func covWarn(t *testing.T, warnings []string, fragment string) string {
	t.Helper()
	for _, warning := range warnings {
		if strings.Contains(warning, fragment) {
			return warning
		}
	}
	t.Fatalf("no warning mentions %q; warnings: %v", fragment, warnings)
	return ""
}

func TestCoverRenderRefusesDescriptionsItCannotDeclare(t *testing.T) {
	// A service without a name cannot be declared, and neither can an operation
	// without one. Inventing a name would publish a contract whose identifiers do
	// not match the description's, so both are refused.
	_, err := protocontract.Render(api.API{
		ID: "nameless", Format: protocontract.Format,
		Services: []api.Service{covService("", api.Operation{Name: "Get"})},
	}, protocontract.RenderOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "unnamed service")

	_, err = protocontract.Render(api.API{
		ID: "nameless", Format: protocontract.Format,
		Services: []api.Service{covService("cov.nameless.v1.NamelessService", api.Operation{})},
	}, protocontract.RenderOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "unnamed operation")
}

func TestCoverRenderRefusesASourceNoCompilerWouldAccept(t *testing.T) {
	// The compile is part of rendering, so an option the renderer writes without
	// escaping is reported as a failure instead of being published as an artifact a
	// consumer's parser rejects.
	_, err := protocontract.Render(api.API{
		ID: "quoted", Name: "quoted", Format: protocontract.Format,
		Services: []api.Service{covService("cov.quoted.v1.QuotedService", api.Operation{Name: "Get"})},
	}, protocontract.RenderOptions{Options: map[string]string{
		"go-package": `example.test/quoted"; message Injected {`,
	}})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "does not compile")
}

func TestCoverRenderRefusesADescriptionWithNoService(t *testing.T) {
	// A file with no service compiles and describes nothing, so a caller that
	// rendered one would publish an artifact no reader can register.
	_, err := protocontract.Render(api.API{ID: "empty", Name: "empty", Format: protocontract.Format}, protocontract.RenderOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "declares no service")

	// The same refusal reaches a caller that asked for the descriptors directly:
	// the two entry points share one translation.
	_, err = protocontract.RenderDescriptorSet(api.API{ID: "empty", Format: protocontract.Format}, protocontract.RenderOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
}

func TestCoverRenderWritesTheIdentityItHas(t *testing.T) {
	// The file's prose is the top of the contract, and it has to be written once:
	// a title that repeats the description would read back as two paragraphs of
	// documentation where the description stated one.
	described := api.API{
		ID: "same", Name: "same", Title: "Same words", Description: "Same words", Format: protocontract.Format,
		Services: []api.Service{covService("cov.same.v1.SameService", api.Operation{Name: "Get"})},
	}
	source := covSource(t, covRender(t, described, protocontract.RenderOptions{}))
	assert.Equal(t, 1, strings.Count(source, "// Same words"))
	assert.True(t, strings.HasPrefix(source, "// Same words\n"), "the header is above the syntax statement:\n%s", source)

	// A description with no title or name still opens with something: the
	// identifier is the last identity it declared, and an unlabelled file would
	// leave a reader with no idea what it is looking at.
	untitled := api.API{
		ID: "only-id", Format: protocontract.Format,
		Services: []api.Service{covService("cov.untitled.v1.UntitledService", api.Operation{Name: "Get"})},
	}
	assert.Contains(t, covSource(t, covRender(t, untitled, protocontract.RenderOptions{})), "// only-id")
}

func TestCoverRenderKeepsServicesWithoutAPackageApartFromPackagedOnes(t *testing.T) {
	// A service named without a package, as a tag group is, cannot go into the same
	// file as a packaged one: declaring a package there would prefix it and rename
	// it. Its file therefore needs a directory even though it has no package.
	described := api.API{
		ID: "merged", Name: "merged", Format: protocontract.Format,
		Services: []api.Service{
			covService("alpha.v1.AlphaService", api.Operation{Name: "Do"}),
			covService("BetaService", api.Operation{Name: "Do"}),
		},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})
	require.Len(t, rendered.Files, 3, "two contracts and one descriptor set")

	names := make([]string, 0, 2)
	for _, file := range rendered.Files {
		if file.MediaType == protocontract.MediaTypeContract {
			names = append(names, file.Name)
		}
	}
	assert.Equal(t, []string{"contract.proto", "unpackaged/contract.proto"}, names)
	assert.Contains(t, covSource(t, rendered), "package alpha.v1;")
	assert.NotContains(t, string(rendered.Files[1].Content), "package ;",
		"a file with no package declares none")
}

func TestCoverRenderHonoursTheTargetSwitches(t *testing.T) {
	// A code generator needs the package and the go_package it was told to use, and
	// a caller that names a file without the extension is asking for that name, not
	// for a file with no extension.
	described := api.API{
		ID: "catalog", Name: "catalog", Format: protocontract.Format,
		Services: []api.Service{covService("catalog.v1.ItemService", api.Operation{Name: "Get"})},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{Options: map[string]string{
		"filename":   "items",
		"package":    "forced.v1",
		"go-package": "example.test/forced/v1;forcedv1",
	}})

	assert.Equal(t, "items.proto", rendered.Files[0].Name, "the extension is added, not assumed")
	source := covSource(t, rendered)
	assert.Contains(t, source, "package forced.v1;", "the caller's package wins over the service's own")
	assert.Contains(t, source, `option go_package = "example.test/forced/v1;forcedv1";`)
	assert.Contains(t, source, "service ItemService {")
	assert.Equal(t, "items.pb", rendered.Files[1].Name, "the descriptors are named after the contract they came from")
}

func TestCoverRenderReportsNamesProtobufCannotDeclare(t *testing.T) {
	// A description read from another format carries names that are not protobuf
	// names. They are sanitized rather than dropped — the declaration has to exist —
	// and the loss is reported, because the identifier a consumer saw is not the one
	// it will read back.
	described := api.API{
		ID: "renamed", Name: "renamed", Format: "openapi",
		Services: []api.Service{covService("cov bad.v1.My Service", api.Operation{Name: "Do It"})},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})
	covWarn(t, rendered.Warnings, "the description's service name will not survive a re-parse")
	covWarn(t, rendered.Warnings, "the description's operation name will not survive a re-parse")

	source := covSource(t, rendered)
	assert.Contains(t, source, "package cov_bad.v1;")
	assert.Contains(t, source, "service My_Service {")
	assert.Contains(t, source, "rpc Do_It(")
}

func TestCoverRenderReportsDeclarationsThatCollide(t *testing.T) {
	// Two services of one package, or two operations of one service, cannot share a
	// name. Renaming one is the only way to declare both, and a consumer that
	// called the second by the name the description gave it has to be told.
	collidingServices := api.API{
		ID: "dupes", Name: "dupes", Format: protocontract.Format,
		Services: []api.Service{
			covService("cov.dupes.v1.DupService", api.Operation{Name: "Get"}),
			covService("cov.dupes.v1.DupService", api.Operation{Name: "Get"}),
		},
	}
	rendered := covRender(t, collidingServices, protocontract.RenderOptions{})
	covWarn(t, rendered.Warnings, "collides with another declaration")
	assert.Contains(t, covSource(t, rendered), "service DupService_2 {")

	// The same rule inside one service, applied repeatedly: the free name the scope
	// hands out has to be free for the third declaration too.
	collidingOperations := api.API{
		ID: "dupes", Name: "dupes", Format: protocontract.Format,
		Services: []api.Service{covService("cov.dupes.v1.DupService",
			api.Operation{Name: "Get"}, api.Operation{Name: "Get"}, api.Operation{Name: "Get"})},
	}
	rendered = covRender(t, collidingOperations, protocontract.RenderOptions{})
	covWarn(t, rendered.Warnings, "is already declared in")
	source := covSource(t, rendered)
	assert.Contains(t, source, "rpc Get_2(")
	assert.Contains(t, source, "rpc Get_3(")
}

func TestCoverRenderReportsAReferenceItHadToRename(t *testing.T) {
	// A reference names a message, and that name is what the contract's other
	// references use. When the name is already taken by another declaration the
	// shape is renamed, and a consumer holding the original reference has to learn
	// that it now reads back as a different message.
	item := api.ObjectSchema(api.Property{Name: "sku", Schema: api.StringSchema()})
	item.Ref = "cov.stock.v1.Item"
	described := api.API{
		ID: "stock", Name: "stock", Format: protocontract.Format,
		Services: []api.Service{covService("cov.stock.v1.Item", api.Operation{Name: "Get", Request: item.Clone()})},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})
	warning := covWarn(t, rendered.Warnings, "is already declared in package")
	assert.Contains(t, warning, "cov.stock.v1.Item")
	assert.Contains(t, covSource(t, rendered), "message Item_2 {")
}

func TestCoverRenderWrapsValuesAFieldCannotCarry(t *testing.T) {
	// protobuf has no repeated-of-repeated and no map of a repeated value, and no
	// field at all for a free-form object. The value is wrapped in a message rather
	// than flattened, because flattening would change what the description said.
	request := api.ObjectSchema(
		api.Property{Name: "grid", Schema: api.ArraySchema(api.ArraySchema(api.StringSchema()))},
		api.Property{Name: "index", Schema: &api.Schema{Type: api.TypeObject, AdditionalProperties: api.ArraySchema(api.StringSchema())}},
		api.Property{Name: "meta", Schema: &api.Schema{AdditionalPropertiesAllowed: true}},
	)
	described := api.API{
		ID: "shapes", Name: "shapes", Format: protocontract.Format,
		Services: []api.Service{covService("cov.shapes.v1.ShapeService", api.Operation{Name: "Send", Request: request})},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})
	covWarn(t, rendered.Warnings, "protobuf declares every field")

	source := covSource(t, rendered)
	assert.Contains(t, source, "repeated SendRequest.GridEntry grid = 1")
	assert.Contains(t, source, "map<string, SendRequest.IndexValueEntry> index = 2")
	assert.Contains(t, source, "SendRequest.Meta meta = 3")

	// The wrapper keeps the value the field could not carry, rather than flattening
	// it: a repeated string inside the entry is still a repeated string.
	assert.Contains(t, source, "message GridEntry {\n    repeated string value = 1")

	// An array that declared no element schema is still an array: reporting the
	// missing element and defaulting it keeps the field the description stated.
	untyped := api.API{
		ID: "shapes", Name: "shapes", Format: protocontract.Format,
		Services: []api.Service{covService("cov.shapes.v1.ShapeService",
			api.Operation{Name: "Send", Request: api.ObjectSchema(api.Property{Name: "loose", Schema: &api.Schema{Type: api.TypeArray}})})},
	}
	rendered = covRender(t, untyped, protocontract.RenderOptions{})
	warning := covWarn(t, rendered.Warnings, "declares no element schema")
	assert.Contains(t, warning, "loose")
	assert.Contains(t, covSource(t, rendered), "repeated string loose = 1")
}

func TestCoverRenderReportsAlternativesItCannotExpress(t *testing.T) {
	// protobuf has oneof, but a oneof is not a union this reader can express: every
	// member is an independently named field and the reader reports them all
	// present. Rendering every alternative would claim each is required, so the
	// first is rendered and the rest are reported.
	oneOf := &api.Schema{OneOf: []*api.Schema{api.StringSchema(), &api.Schema{Type: api.TypeInteger}, api.StringSchema()}}
	anyOf := &api.Schema{AnyOf: []*api.Schema{api.StringSchema(), api.StringSchema()}}
	withMembers := api.ObjectSchema(api.Property{Name: "name", Schema: api.StringSchema()})
	withMembers.OneOf = []*api.Schema{api.StringSchema(), api.StringSchema()}

	described := api.API{
		ID: "unions", Name: "unions", Format: protocontract.Format,
		Services: []api.Service{covService("cov.unions.v1.UnionService", api.Operation{
			Name: "Send",
			Request: api.ObjectSchema(
				api.Property{Name: "either", Schema: oneOf},
				api.Property{Name: "whatever", Schema: anyOf},
				api.Property{Name: "shape", Schema: withMembers},
			),
		})},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})
	covWarn(t, rendered.Warnings, "declares 3 alternatives; protobuf has no union")
	covWarn(t, rendered.Warnings, "declares 2 alternatives; protobuf has no union")
	covWarn(t, rendered.Warnings, "declares both members and alternatives")

	source := covSource(t, rendered)
	assert.Contains(t, source, "string either = 1", "the first alternative is what the field carries")
	assert.Contains(t, source, "string whatever = 2")
	assert.Contains(t, source, "Shape shape = 3", "the members were rendered as a message")
}

func TestCoverRenderReportsTypesAndRefinementsProtobufHasNoSpellingFor(t *testing.T) {
	// A format is what tells a caller that a string is a date or an integer an
	// identifier. protobuf spells a few of them; the rest are carried by the wider
	// type and reported, because a consumer that reads the contract has to know the
	// refinement was dropped.
	described := api.API{
		ID: "types", Name: "types", Format: protocontract.Format,
		Services: []api.Service{covService("cov.types.v1.TypeService", api.Operation{
			Name: "Send",
			Request: api.ObjectSchema(
				api.Property{Name: "count", Schema: &api.Schema{Type: api.TypeInteger, Format: "uint32"}},
				api.Property{Name: "ratio", Schema: &api.Schema{Type: api.TypeNumber, Format: "double"}},
				api.Property{Name: "stamp", Schema: &api.Schema{Type: api.TypeString, Format: "date-time"}},
				api.Property{Name: "epoch", Schema: &api.Schema{Type: api.TypeInteger, Format: "unix-time"}},
				api.Property{Name: "nothing", Schema: &api.Schema{Type: api.TypeNull}},
				api.Property{Name: "opaque", Schema: &api.Schema{}},
				api.Property{Name: "untyped", Schema: nil},
			),
		})},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})
	covWarn(t, rendered.Warnings, `format "date-time" of a string value`)
	covWarn(t, rendered.Warnings, `format "unix-time" of a integer value`)
	covWarn(t, rendered.Warnings, "is JSON null")
	covWarn(t, rendered.Warnings, "declares no type protobuf can carry")

	source := covSource(t, rendered)
	assert.Contains(t, source, "uint64 count = 1", "an unsigned integer keeps its range")
	assert.Contains(t, source, "double ratio = 2")
	assert.Contains(t, source, "string stamp = 3")
	assert.Contains(t, source, "int64 epoch = 4")
	assert.Contains(t, source, "string nothing = 5")
	assert.Contains(t, source, "string opaque = 6")
	assert.Contains(t, source, "string untyped = 7", "a member with no schema is still a member")
}

func TestCoverRenderReportsThePresenceAndConstraintsItCannotWrite(t *testing.T) {
	// proto3 has no presence for a repeated or map field, so a required property of
	// either kind reads back as not required — the reader would otherwise report a
	// contract that asks for less than the description did.
	//
	// Bounds, patterns, defaults and the deprecated option are written where protobuf
	// can carry them and reported where the reader ignores them, because a consumer
	// that trusted the contract would believe the constraint survived.
	described := api.API{
		ID: "strict", Name: "strict", Format: protocontract.Format,
		Services: []api.Service{covService("cov.strict.v1.StrictService", api.Operation{
			Name: "Send",
			Request: api.ObjectSchema(
				api.Property{Name: "tags", Schema: api.ArraySchema(api.StringSchema()), Required: true},
				api.Property{Name: "labels", Schema: &api.Schema{Type: api.TypeObject, AdditionalProperties: api.StringSchema()}, Required: true},
				api.Property{Name: "code", Schema: &api.Schema{Type: api.TypeString, Pattern: "^[a-z]+$", Deprecated: true}},
			),
		})},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})
	covWarn(t, rendered.Warnings, "a repeated or map field has no presence in proto3")
	covWarn(t, rendered.Warnings, "bounds, lengths, patterns, defaults, and read-only marks")
	covWarn(t, rendered.Warnings, "deprecated option was written")

	source := covSource(t, rendered)
	assert.Contains(t, source, `string code = 3 [json_name = "code", deprecated = true];`)
	assert.NotContains(t, source, "optional repeated")
}

func TestCoverRenderReportsEnumLiteralsProtobufCannotDeclare(t *testing.T) {
	// An enum's values are declared in the scope that contains the enum, so a
	// literal has to be a name protobuf accepts and has to be free in that scope.
	// Renaming a literal rather than dropping it keeps what the value means, and the
	// rename is reported because the literal a caller sends is not the one it reads.
	described := api.API{
		ID: "states", Name: "states", Format: protocontract.Format,
		Services: []api.Service{covService("cov.states.v1.StateService", api.Operation{
			Name: "Send",
			Request: api.ObjectSchema(api.Property{Name: "state", Schema: &api.Schema{
				Type: api.TypeString,
				// A duplicate literal, an unspellable one, and one that starts a number.
				Enum: []string{"", "OPEN", "OPEN", "1st"},
			}}),
		})},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})
	warning := covWarn(t, rendered.Warnings, "is not a name protobuf can declare here")

	source := covSource(t, rendered)
	assert.Contains(t, source, "enum State {")
	assert.Contains(t, source, "UNSPECIFIED = 0;", warning)
	assert.Contains(t, source, "OPEN = 1;")
	assert.Contains(t, source, "VALUE_1st = 2;")
	assert.Equal(t, 1, strings.Count(source, "OPEN ="), "the duplicate literal is declared once")
}

func TestCoverRenderReportsTheLossesThatAreCountedOnceForTheAPI(t *testing.T) {
	// Security is a description-wide fact and deprecation a per-declaration one, and
	// a contract of fifty operations must not bury either under fifty findings. The
	// counts say how many operations stated the fact the contract cannot carry.
	described := api.API{
		ID: "secure", Name: "secure", Format: protocontract.Format,
		SecuritySchemes: []api.SecurityScheme{{Name: "token", Type: "http", Scheme: "bearer"}},
		Services: []api.Service{covService("cov.secure.v1.SecureService",
			api.Operation{
				Name:        "Read",
				Security:    []api.SecurityRequirement{{Scheme: "token"}},
				Deprecated:  true,
				Path:        "/secure/{id}",
				Method:      "get",
				Responses:   []api.Response{{Status: "200", Description: "the value"}},
				Parameters:  []api.Parameter{{Name: "id", In: api.ParameterInPath, Required: true, Schema: api.StringSchema()}},
				SideEffects: []api.SideEffect{api.SideEffectReadOnly},
			},
			api.Operation{Name: "Write", SideEffects: []api.SideEffect{api.SideEffectCreate}},
		)},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})
	covWarn(t, rendered.Warnings, "the description declares security")
	covWarn(t, rendered.Warnings, "protobuf's deprecated option was written for")
	covWarn(t, rendered.Warnings, "the request path declared by 1 operation was dropped")
	covWarn(t, rendered.Warnings, "the transport method declared by 1 operation was dropped")
	covWarn(t, rendered.Warnings, "the parameters of 1 operation were folded")
	covWarn(t, rendered.Warnings, "1 operation documented responses by status")
}

func TestCoverRenderSpellsAFieldNameProtobufCanDeclare(t *testing.T) {
	// A property name is the description's spelling, and it travels in json_name. The
	// field itself has to be a snake_case identifier, so an unspellable name is
	// substituted and the description's own name is what a reader reads back.
	described := api.API{
		ID: "names", Name: "names", Format: protocontract.Format,
		Services: []api.Service{covService("cov.names.v1.NameService", api.Operation{
			Name: "Send",
			Request: api.ObjectSchema(
				api.Property{Name: "1st", Schema: api.StringSchema()},
				api.Property{Name: "---", Schema: api.StringSchema()},
				api.Property{Name: "a-b", Schema: api.StringSchema()},
				api.Property{Name: "", Schema: api.StringSchema()},
			),
		})},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})
	source := covSource(t, rendered)

	assert.Contains(t, source, `string field_1st = 1 [json_name = "1st"];`,
		"a name that starts with a digit is prefixed, because a protobuf field name cannot")
	assert.Contains(t, source, `string value = 2 [json_name = "---"];`,
		"a name with nothing readable in it still needs a field")
	assert.Contains(t, source, `string a_b = 3 [json_name = "a-b"];`)
	assert.Contains(t, source, `string field_4 = 4 [json_name = "field_4"];`,
		"a member with no name is named after its position")
}

func TestCoverRenderSpellsEnumLiteralsProtobufCanDeclare(t *testing.T) {
	// A literal is what a caller sends, so it is carried in the enum's value name as
	// far as protobuf allows and reported when it had to be respelled.
	described := api.API{
		ID: "literals", Name: "literals", Format: protocontract.Format,
		Services: []api.Service{covService("cov.literals.v1.LiteralService", api.Operation{
			Name: "Send",
			Request: api.ObjectSchema(api.Property{Name: "flag", Schema: &api.Schema{
				Type: api.TypeString,
				Enum: []string{"a-b", "---", "OK"},
			}}),
		})},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})
	covWarn(t, rendered.Warnings, "is not a name protobuf can declare here")

	source := covSource(t, rendered)
	assert.Contains(t, source, "a_b = 0;", "punctuation in a literal becomes an underscore")
	assert.Contains(t, source, "UNSPECIFIED = 1;", "a literal with nothing readable left is still declared")
	assert.Contains(t, source, "OK = 2;")
}

func TestCoverRenderSanitizesAServiceNameItCannotDeclare(t *testing.T) {
	// A service name that has nothing protobuf can spell is replaced rather than
	// dropped: the operations still have to be declared, and the replacement is
	// reported because the identifiers derived from it change with it.
	described := api.API{
		ID: "sanitized", Name: "sanitized", Format: "openapi",
		Services: []api.Service{covService("!!!", api.Operation{Name: "Do"})},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})
	covWarn(t, rendered.Warnings, "is not a protobuf name")
	assert.Contains(t, covSource(t, rendered), "service value {")

	// A name that starts with a digit keeps its digits, prefixed so it is an
	// identifier, and a package with an empty segment loses the segment rather than
	// producing a package declaration a compiler rejects.
	numeric := api.API{
		ID: "sanitized", Name: "sanitized", Format: "openapi",
		Services: []api.Service{covService("cov.names.v1.1abc", api.Operation{Name: "Do"})},
	}
	rendered = covRender(t, numeric, protocontract.RenderOptions{})
	covWarn(t, rendered.Warnings, "is not a protobuf name")
	assert.Contains(t, covSource(t, rendered), "service _1abc {")

	spaced := api.API{
		ID: "sanitized", Name: "sanitized", Format: "openapi",
		Services: []api.Service{covService("cov..names.v1.PkgService", api.Operation{Name: "Do"})},
	}
	rendered = covRender(t, spaced, protocontract.RenderOptions{})
	covWarn(t, rendered.Warnings, "is not a protobuf name")
	assert.Contains(t, covSource(t, rendered), "package cov.names.v1;")
}

func TestCoverRenderReportsTagsAndReferencesFromAnotherDialect(t *testing.T) {
	// Tags are how an HTTP document groups operations, and a protobuf contract groups
	// them by service only: the grouping is kept where protobuf can express it and
	// reported where it cannot.
	//
	// A JSON Schema pointer is not a protobuf name either. The shape it resolved to
	// is what a field needs, so the last segment names a message, and a consumer
	// holding the pointer has to know the reference now reads back as that message.
	pet := api.ObjectSchema(
		api.Property{Name: "name", Schema: &api.Schema{
			Type: api.TypeString, MinLength: api.Int64(1), MaxLength: api.Int64(64), Default: "rex",
		}},
		api.Property{Name: "id", Schema: &api.Schema{
			Type: api.TypeInteger, Minimum: api.Float64(1), Maximum: api.Float64(100), ReadOnly: true,
		}},
	)
	pet.Ref = "#/components/schemas/Pet"
	described := api.API{
		ID: "pets", Name: "pets", Format: "openapi", Tags: []string{"pets"},
		Services: []api.Service{{
			Name: "pets",
			Operations: []api.Operation{{
				Name:     "getPet",
				Tags:     []string{"pets"},
				Response: pet,
			}},
		}},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})
	covWarn(t, rendered.Warnings, "declares tags for itself or its operations")
	covWarn(t, rendered.Warnings, `schema reference "#/components/schemas/Pet" is not a name in package`)
	covWarn(t, rendered.Warnings, "bounds, lengths, patterns, defaults, and read-only marks")

	source := covSource(t, rendered)
	assert.Contains(t, source, "message Pet {")
	assert.Contains(t, source, "rpc getPet(GetPetRequest) returns (Pet);")
}

func TestCoverRenderKeepsAnOperationClassificationItCannotInherit(t *testing.T) {
	// protobuf makes a method inherit its service's classification, so a service
	// line is only legal when every method agrees with it. A method that declares
	// its own effects has to say so itself, or it would read back classified as the
	// service is — which is a different promise than the one it made.
	described := api.API{
		ID: "mixed", Name: "mixed", Format: protocontract.Format,
		Services: []api.Service{{
			Name:        "cov.mixed.v1.MixedService",
			SideEffects: []api.SideEffect{api.SideEffectReadOnly},
			Operations: []api.Operation{
				{Name: "Read", SideEffects: []api.SideEffect{api.SideEffectReadOnly}},
				// Same length, one different element.
				{Name: "Touch", SideEffects: []api.SideEffect{api.SideEffectUpdate}},
				// One more element than the service declares.
				{Name: "Destroy", SideEffects: []api.SideEffect{api.SideEffectDelete, api.SideEffectIrreversible}},
			},
		}},
	}
	rendered := covRender(t, described, protocontract.RenderOptions{})

	source := covSource(t, rendered)
	assert.Equal(t, 3, strings.Count(source, "@toolbox.side-effects"),
		"the service line plus one per method that disagrees with it:\n%s", source)
	assert.NotContains(t, strings.Join(rendered.Warnings, "\n"), "inherit",
		"no method here depends on staying unclassified, so the service line is legal")
}
