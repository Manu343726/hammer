package hammer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/bufbuild/protocompile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The contracts in errors.go, catalog.go, registrar.go, policy.go, policytext.go,
// protobuf.go and provider.go, each pinned to the behaviour a consumer relies on
// rather than to the code that happens to implement it today. Every helper here is
// prefixed so it cannot collide with a helper another file in the package adds.

// contractKinds is every classification the standard model states. A kind that a
// caller cannot recover is a kind that cannot be mapped to a transport code, so the
// list is walked rather than sampled.
var contractKinds = []ErrorKind{
	KindInvalid,
	KindNotFound,
	KindFailedPrecondition,
	KindDenied,
	KindAlreadyExists,
	KindUnsupported,
	KindUnavailable,
	KindInternal,
}

// contractProperty returns the schema of one named property of an object schema.
func contractProperty(t *testing.T, schema *Schema, name string) *Schema {
	t.Helper()
	require.NotNil(t, schema)
	for _, property := range schema.Properties {
		if property.Name == name {
			require.NotNil(t, property.Schema, "property %q carries no schema", name)
			return property.Schema
		}
	}
	require.FailNow(t, "the described object has no such property", "no property %q in %s", name, schema.Ref)
	return nil
}

func TestAContractErrorKindSurvivesWrapping(t *testing.T) {
	// A classification is the only thing that lets a transport map a failure to a
	// status code, so it has to be recoverable after every layer a caller wraps it
	// in. A kind that lives only on the value that was returned is lost the moment
	// an adapter annotates it, and the failure silently becomes internal.
	cause := errors.New("connection reset by peer")
	for _, kind := range contractKinds {
		classified := WrapError(kind, cause, "reading %s", "the catalog")

		require.True(t, IsKind(classified, kind), "kind %q is not recoverable from the error it built", kind)
		require.True(t, errors.Is(classified, cause),
			"the cause of a classified error stays reachable, so a caller can still tell what failed")

		wrapped := fmt.Errorf("the adapter refused: %w", classified)
		assert.Equal(t, kind, KindOf(wrapped),
			"kind %q is lost when a caller wraps the error again", kind)

		var extracted *Error
		require.True(t, errors.As(wrapped, &extracted),
			"the classified error is still extractable once the kind has been read")
		assert.Equal(t, kind, extracted.Kind)
		assert.Equal(t, "reading the catalog", extracted.Message)
	}
}

func TestAContractErrorRendersItsParts(t *testing.T) {
	// The rendering is what ends up in a log line, so every combination of the
	// three fields a failure can carry has to say something rather than print an
	// empty string: an error that renders as "" is one nobody can diagnose.
	cause := errors.New("connection reset by peer")

	assert.Equal(t, "asking the catalog: connection reset by peer",
		(&Error{Kind: KindUnavailable, Message: "asking the catalog", Err: cause}).Error(),
		"an error with a message and a cause reports both, message first")
	assert.Equal(t, "asking the catalog",
		(&Error{Kind: KindInvalid, Message: "asking the catalog"}).Error())
	assert.Equal(t, "connection reset by peer",
		(&Error{Kind: KindInternal, Err: cause}).Error(),
		"an error with only a cause reports the cause rather than nothing")
	assert.Equal(t, "not_found",
		(&Error{Kind: KindNotFound}).Error(),
		"an error that only states a kind still names it, which is better than a blank line")

	var absent *Error
	assert.Equal(t, "<nil>", absent.Error(), "a nil error renders rather than panicking in a log statement")
	assert.Nil(t, absent.Unwrap())
	assert.Equal(t, cause, (&Error{Kind: KindInternal, Err: cause}).Unwrap())

	built := Errorf(KindDenied, "the policy refuses %s", "Register")
	assert.Equal(t, KindDenied, built.Kind)
	assert.Equal(t, "the policy refuses Register", built.Message)
	assert.Nil(t, built.Err, "Errorf classifies without inventing a cause")
}

func TestAContractUnclassifiedFailureIsInternal(t *testing.T) {
	// The failure mode this catches: a bare error reaching a transport that reports
	// it as success, or as a kind nobody can act on. "Nothing classified me" has to
	// read as a bug in this library, not as a request the caller should change, and
	// never as an empty kind a caller might interpret as a zero-value decision.
	assert.Equal(t, ErrorKind(""), KindOf(nil), "no error is not a failure, so it has no kind")

	plain := errors.New("something nobody classified")
	assert.Equal(t, KindInternal, KindOf(plain))
	assert.NotEqual(t, ErrorKind(""), KindOf(plain), "an unclassified failure is still classified, as internal")
	assert.True(t, IsKind(plain, KindInternal))
	assert.False(t, IsKind(nil, KindInternal), "a nil error is not a failure of any kind")

	// A classified error that states no kind is unclassified too; reading the
	// struct rather than the chain would return the empty kind.
	assert.Equal(t, KindInternal, KindOf(&Error{Message: "a failure somebody half-classified"}))
	assert.Equal(t, KindInternal, KindOf(fmt.Errorf("wrapped: %w", &Error{Message: "no kind"})))

	// A caller can tell "too slow" from "wrong", which is the whole reason these
	// two were given a kind of their own.
	assert.Equal(t, KindUnavailable, KindOf(context.DeadlineExceeded))
	assert.Equal(t, KindUnavailable, KindOf(fmt.Errorf("describing the API: %w", context.DeadlineExceeded)))
	assert.Equal(t, KindUnavailable, KindOf(context.Canceled))
	assert.Equal(t, KindUnavailable, KindOf(fmt.Errorf("describing the API: %w", context.Canceled)))
}

func TestAContractKindIsNormalizedFromConfiguration(t *testing.T) {
	// A kind written by hand in a configuration file arrives with whatever
	// whitespace and case a person typed. Matching it exactly would classify a
	// failure as internal because somebody wrote "NotFound".
	assert.Equal(t, KindDenied, NormalizeKind(" Permission_Denied\n"))
	assert.Equal(t, KindNotFound, NormalizeKind("NOT_FOUND"))
	assert.Equal(t, ErrorKind(""), NormalizeKind("   "), "an empty kind stays empty rather than becoming a name")
	assert.Equal(t, KindAlreadyExists, NormalizeKind(string(KindAlreadyExists)))
}

// contractServer is a registered server with every field a catalog returns set, so
// a clone that drops one of them is visible.
func contractServer() Server {
	return Server{
		ID:          "registry",
		Name:        "Registry",
		BaseURL:     "http://127.0.0.1:8080",
		Format:      "connectrpc",
		Transport:   "http",
		Description: "the registry server",
		Status:      ServerStatusServing,
		Metadata:    map[string]string{"owner": "platform"},
	}
}

// contractAPI is a registered API with a service and an operation, so a clone that
// stops at the top level is visible.
func contractAPI() API {
	return API{
		ID:        "registry",
		Name:      "Registry",
		Format:    "connectrpc",
		ServerIDs: []string{"registry"},
		Metadata:  map[string]string{"owner": "platform"},
		Services: []Service{{
			ID:   "toolbox.registry.v1.RegistryService",
			Name: "RegistryService",
			Operations: []Operation{{
				ID:          "toolbox.registry.v1.RegistryService/Register",
				Name:        "Register",
				SideEffects: []SideEffect{SideEffectCreate},
			}},
		}},
	}
}

func TestAContractStaticCatalogAnswersWithoutSharingItsState(t *testing.T) {
	// A catalog is read by an adapter that renders and by a caller that decides
	// exposure; if either can mutate what the catalog holds through the slice it
	// was handed, one of them silently changes what the other describes.
	catalog := &StaticCatalog{
		RegisteredServers: []Server{contractServer()},
		RegisteredAPIs:    []API{contractAPI()},
	}

	servers, err := catalog.Servers(context.Background())
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, "registry", servers[0].ID)

	apis, err := catalog.APIs(context.Background())
	require.NoError(t, err)
	require.Len(t, apis, 1)
	assert.Equal(t, "registry", apis[0].ID)
	require.Len(t, apis[0].Services, 1)
	require.Len(t, apis[0].Services[0].Operations, 1)

	// Mutating what came back must not reach the catalog.
	servers[0].Name = "changed"
	servers[0].Metadata["owner"] = "changed"
	apis[0].Services[0].Operations[0].Name = "changed"
	apis[0].Metadata["owner"] = "changed"

	again, err := catalog.Servers(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Registry", again[0].Name)
	assert.Equal(t, "platform", again[0].Metadata["owner"], "a returned server is a copy, not the record itself")

	apisAgain, err := catalog.APIs(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Register", apisAgain[0].Services[0].Operations[0].Name,
		"a returned API is copied down to its operations, because a rendered tool reads them")
	assert.Equal(t, "platform", apisAgain[0].Metadata["owner"])
}

func TestAContractStaticCatalogToleratesHavingNoState(t *testing.T) {
	// A catalog that was never filled in — the zero value, or none at all — is what
	// an adapter built before anything was registered sees. It has to answer "no
	// servers, no APIs" rather than panic, because that is an ordinary startup
	// order rather than a mistake.
	empty := &StaticCatalog{}
	servers, err := empty.Servers(context.Background())
	require.NoError(t, err)
	assert.Empty(t, servers)

	apis, err := empty.APIs(context.Background())
	require.NoError(t, err)
	assert.Empty(t, apis)

	var absent *StaticCatalog
	servers, err = absent.Servers(context.Background())
	assert.NoError(t, err)
	assert.Nil(t, servers, "a nil catalog reports no servers rather than panicking on its first read")

	apis, err = absent.APIs(context.Background())
	assert.NoError(t, err)
	assert.Nil(t, apis)
}

func TestAContractStaticCatalogAnswersForTheExposureItHolds(t *testing.T) {
	// A catalog that carries exposure decisions is authoritative for them: a
	// consumer must not decide differently for an operation the catalog has already
	// ruled on, or the exposed surface becomes the union of two policies.
	decided := Exposure{OperationID: "registry/RegistryService/Register", Allowed: true, Exposed: true, Invokable: true}
	catalog := &StaticCatalog{ExposedOperations: ExposureMap{decided.OperationID: decided}}

	exposures, err := catalog.Exposures(context.Background(), []string{decided.OperationID, "registry/RegistryService/Deregister"})
	require.NoError(t, err)
	require.Contains(t, exposures, decided.OperationID)
	assert.Equal(t, decided, exposures[decided.OperationID])
	assert.NotContains(t, exposures, "registry/RegistryService/Deregister",
		"an operation the catalog has never seen is absent, so the consumer falls back to its own policy")

	// A catalog that holds no decisions answers nothing at all, rather than
	// answering "denied" for everything and shrinking a surface it was never told
	// about.
	var absent *StaticCatalog
	exposures, err = absent.Exposures(context.Background(), []string{decided.OperationID})
	assert.NoError(t, err)
	assert.Nil(t, exposures)

	exposures, err = (&StaticCatalog{}).Exposures(context.Background(), []string{decided.OperationID})
	assert.NoError(t, err)
	assert.Nil(t, exposures)
}

func TestAContractExposureMapAnswersOnlyForNamedOperations(t *testing.T) {
	// The map form is what a configuration file and a program that decided exposure
	// before a catalog existed both use. An identifier that arrives with stray
	// whitespace is the same operation, and a blank one is not an operation at all:
	// looking up "" would otherwise be a lookup of nothing that answers nothing.
	exposures := ExposureMap{
		"registry/RegistryService/Register": {OperationID: "registry/RegistryService/Register", Allowed: true, Exposed: true, Invokable: true},
		"knowledge/KnowledgeService/Search": {OperationID: "knowledge/KnowledgeService/Search", Allowed: false},
	}

	got, err := exposures.Exposures(context.Background(), []string{
		"registry/RegistryService/Register",
		" knowledge/KnowledgeService/Search\n",
		"   ",
		"",
		"registry/RegistryService/Deregister",
	})
	require.NoError(t, err)
	require.Len(t, got, 2, "blank identifiers name no operation and are skipped")
	assert.True(t, got["registry/RegistryService/Register"].Invokable)
	assert.False(t, got["knowledge/KnowledgeService/Search"].Allowed)
	assert.NotContains(t, got, "registry/RegistryService/Deregister")
}

func TestAContractInvokerFuncIsAnInvoker(t *testing.T) {
	// The adapter is how every description format supplies an invoker without
	// declaring a type, so it has to forward the caller's context and the whole
	// call unchanged. Dropping either is a defect no signature catches: the
	// operation would be invoked against the wrong server, or without the deadline
	// the caller set.
	type contractKey struct{}
	ctx := context.WithValue(context.Background(), contractKey{}, "the caller's context")

	arguments := []byte(`{"name":"hammer"}`)
	call := Call{Server: contractServer(), API: contractAPI(), Arguments: arguments}

	var seen context.Context
	var seenCall Call
	invoker := InvokerFunc(func(ctx context.Context, call Call) (Result, error) {
		seen, seenCall = ctx, call
		return Result{Status: 200, ContentType: "application/json", Body: call.Arguments}, nil
	})

	var _ Invoker = invoker
	result, err := invoker.Invoke(ctx, call)
	require.NoError(t, err)
	assert.Equal(t, 200, result.Status)
	require.NotNil(t, seen)
	assert.Equal(t, "the caller's context", seen.Value(contractKey{}),
		"the invoker sees the caller's context, deadline and cancellation included")
	assert.Equal(t, call, seenCall, "the invoker sees the call it was given, arguments included")
	assert.Equal(t, []byte(arguments), []byte(result.Body))

	// The result is the invoker's, error included, and is not swallowed into a
	// successful empty result.
	failing := InvokerFunc(func(context.Context, Call) (Result, error) {
		return Result{}, Errorf(KindUnavailable, "the server is down")
	})
	result, err = failing.Invoke(ctx, call)
	assert.True(t, IsKind(err, KindUnavailable), "an invoker's failure reaches the caller with its kind")
	assert.Equal(t, Result{}, result)

	var _ Catalog = (*StaticCatalog)(nil)
	var _ ExposureSource = (*StaticCatalog)(nil)
	var _ ExposureSource = ExposureMap(nil)
}

// contractFile compiles one proto source into a linked descriptor, so the protobuf
// tests read a real contract — with the synthetic map entries, JSON names and
// presence a linked file has — rather than a descriptor the test wrote twice.
func contractFile(t *testing.T) protoreflect.FileDescriptor {
	t.Helper()
	const path = "contract/v1/contract.proto"
	const source = `syntax = "proto3";

package contract.v1;

enum Color {
  COLOR_UNSPECIFIED = 0;
  COLOR_RED = 1;
  COLOR_BLUE = 2;
}

message Address {
  string city = 1;
  string country = 2;
}

message Person {
  string name = 1;
  int32 age = 2;
  uint64 identifier = 3;
  bytes portrait = 4;
  double score = 5;
  bool active = 6;
  Color favorite_color = 7;
  Address address = 8;
  repeated string tags = 9;
  map<string, Address> places = 10;
  Person friend = 11;
  repeated Address history = 12;
}
`

	compiler := protocompile.Compiler{
		Resolver: &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{path: source})},
	}
	files, err := compiler.Compile(context.Background(), path)
	require.NoError(t, err)
	require.Len(t, files, 1)
	return files[0]
}

func TestAContractProtobufMessageBecomesTheStandardSchema(t *testing.T) {
	// The protobuf side of the standard model: this is the one conversion both a
	// contract reader and a documentation reader use, so every shape a protobuf
	// message can take has to arrive as the type, format and reference an agent
	// tool expects. A shape that is described as the wrong JSON type is a call the
	// tool builds and the server rejects.
	file := contractFile(t)
	person := file.Messages().ByName("Person")
	require.NotNil(t, person)

	schema := SchemaForMessage(person)
	require.NotNil(t, schema)
	assert.Equal(t, TypeObject, schema.Type)
	assert.Equal(t, "contract.v1.Person", schema.Ref, "the reference is the message name a caller reads back")
	assert.Equal(t, []string{"address", "friend"}, schema.Required,
		"a message field is what an agent call must supply, and the names are the protobuf JSON names")

	assert.Equal(t, TypeString, contractProperty(t, schema, "name").Type)
	assert.Equal(t, TypeInteger, contractProperty(t, schema, "age").Type)
	assert.Equal(t, "int64", contractProperty(t, schema, "age").Format)

	identifier := contractProperty(t, schema, "identifier")
	assert.Equal(t, TypeInteger, identifier.Type)
	assert.Equal(t, "uint64", identifier.Format)
	require.NotNil(t, identifier.Minimum, "an unsigned field cannot be negative, and the schema says so")
	assert.Equal(t, 0.0, *identifier.Minimum)

	assert.Equal(t, TypeString, contractProperty(t, schema, "portrait").Type)
	assert.Equal(t, "base64", contractProperty(t, schema, "portrait").Format,
		"bytes travel as base64 in protobuf JSON, not as a JSON array of numbers")
	assert.Equal(t, TypeNumber, contractProperty(t, schema, "score").Type)
	assert.Equal(t, TypeBoolean, contractProperty(t, schema, "active").Type)

	color := contractProperty(t, schema, "favoriteColor")
	assert.Equal(t, TypeString, color.Type, "a protobuf enum is its value name in JSON, not its number")
	assert.Equal(t, []string{"COLOR_UNSPECIFIED", "COLOR_RED", "COLOR_BLUE"}, color.Enum)

	address := contractProperty(t, schema, "address")
	assert.Equal(t, TypeObject, address.Type)
	assert.Equal(t, "contract.v1.Address", address.Ref)
	assert.Empty(t, address.Required, "a message with no presence-bearing field requires nothing")
	assert.Equal(t, TypeString, contractProperty(t, address, "city").Type)

	tags := contractProperty(t, schema, "tags")
	assert.Equal(t, TypeArray, tags.Type)
	require.NotNil(t, tags.Items)
	assert.Equal(t, TypeString, tags.Items.Type)

	places := contractProperty(t, schema, "places")
	assert.Equal(t, TypeObject, places.Type, "a protobuf map is a JSON object")
	require.NotNil(t, places.AdditionalProperties, "the map's value shape is the object's additional properties")
	assert.Equal(t, "contract.v1.Address", places.AdditionalProperties.Ref)

	history := contractProperty(t, schema, "history")
	assert.Equal(t, TypeArray, history.Type)
	require.NotNil(t, history.Items)
	assert.Equal(t, "contract.v1.Address", history.Items.Ref,
		"a repeated message keeps its element reference instead of becoming an array of anything")

	// A message that refers to itself has to stop somewhere. The reference and the
	// description are what make a recursive schema finite instead of an expansion
	// that never ends.
	friend := contractProperty(t, schema, "friend")
	assert.Equal(t, TypeObject, friend.Type)
	assert.Equal(t, "contract.v1.Person", friend.Ref)
	recursive := contractProperty(t, friend, "friend")
	assert.Equal(t, "contract.v1.Person", recursive.Ref)
	assert.Equal(t, "recursive contract.v1.Person", recursive.Description,
		"a recursive field is described by reference, so a schema always terminates")
}

func TestAContractProtobufDescribesANilMessagePermissively(t *testing.T) {
	// A nil descriptor is what a reader produces when a contract names a message it
	// cannot resolve. Describing it as an object that accepts anything keeps the
	// rest of the API describable; panicking would lose the whole surface over one
	// unresolved type.
	var absent protoreflect.MessageDescriptor
	var schema *Schema
	require.NotPanics(t, func() { schema = SchemaForMessage(absent) })
	require.NotNil(t, schema)
	assert.Equal(t, TypeObject, schema.Type)
	assert.True(t, schema.AdditionalPropertiesAllowed,
		"an unknown message is accepted rather than described as nothing")
	assert.Empty(t, schema.Ref)

	// The field-level helpers carry the same guarantee, because a reader walks a
	// descriptor it may not have resolved.
	require.NotPanics(t, func() { assert.NotNil(t, schemaForField(nil, map[protoreflect.FullName]bool{})) })
	require.NotPanics(t, func() { assert.NotNil(t, schemaForValue(nil, map[protoreflect.FullName]bool{})) })
}

func TestAContractProseIsAttachedOnlyWhenThereIsSome(t *testing.T) {
	// The documentation reader attaches a comment it found to the schema it already
	// built. An absent comment must leave the description the descriptor supplied
	// alone rather than blanking it, and a nil schema has to survive being handed a
	// comment.
	schema := &Schema{Type: TypeString, Description: "the name a caller knows the API by"}
	assert.Same(t, schema, DescribeComment(schema, ""), "no comment leaves the description as the descriptor had it")
	assert.Equal(t, "the name a caller knows the API by", schema.Description,
		"the descriptor's own description is still there")

	assert.Same(t, schema, DescribeComment(schema, "the name a caller uses"))
	assert.Equal(t, "the name a caller uses", schema.Description)

	assert.Nil(t, DescribeComment(nil, "a comment nobody asked to attach"))
	assert.Nil(t, DescribeComment(nil, ""))
}

func TestAContractProviderHandlesExactlyWhatItDeclares(t *testing.T) {
	// A provider's whole purpose is to answer "can you read this?" for a caller that
	// has not imported it. An answer of "yes" for something it never declared sends
	// a document to a parser that will fail on it; an answer of "no" for something
	// it did declare makes the provider unusable.
	provider := Provider{
		ID:           "protocontract",
		Role:         ProviderParser,
		Formats:      []Format{"protobuf", "descriptor_set"},
		Targets:      []string{"openapi", "mcp"},
		Transports:   []Transport{"connectrpc", "grpc"},
		ServiceNames: []string{ParserService},
	}

	assert.True(t, provider.HandlesFormat("protobuf"))
	assert.True(t, provider.HandlesFormat("descriptor_set"))
	assert.False(t, provider.HandlesFormat("openapi"), "a representation it renders is not a format it reads")
	assert.False(t, provider.HandlesFormat(""))
	assert.False(t, provider.HandlesFormat("Protobuf"), "a format identifier is matched exactly, not by case-folding")

	assert.True(t, provider.HandlesTarget("openapi"))
	assert.False(t, provider.HandlesTarget("protobuf"), "a format it reads is not a target it renders")
	assert.False(t, provider.HandlesTarget(""))

	assert.True(t, provider.HandlesTransport("connectrpc"))
	assert.False(t, provider.HandlesTransport("http"))
	assert.False(t, provider.HandlesTransport(""))

	assert.True(t, provider.Serves(ParserService))
	assert.False(t, provider.Serves(AdapterService), "a parser does not claim the adapter's contract")
	assert.False(t, provider.Serves(""))

	// A provider that declares nothing handles nothing. The alternative — treating
	// an empty list as "anything" — is how a caller picks the first provider it
	// finds and gets a parser that cannot read the document.
	var silent Provider
	assert.False(t, silent.HandlesFormat("protobuf"))
	assert.False(t, silent.HandlesTarget("openapi"))
	assert.False(t, silent.HandlesTransport("connectrpc"))
	assert.False(t, silent.Serves(ParserService))
}

func TestAContractProviderCloneIsDeep(t *testing.T) {
	// A provider record is published by the package that owns the claim and indexed
	// by a caller; a copy that shares its slices lets one consumer's edit rewrite
	// what the provider says it can do for every other consumer.
	provider := Provider{
		ID:           "protocontract",
		Subsystem:    "apimcp",
		Role:         ProviderParser,
		Formats:      []Format{"protobuf"},
		Targets:      []string{"openapi"},
		Transports:   []Transport{"connectrpc"},
		Endpoint:     "127.0.0.1:9100",
		ServiceNames: []string{ParserService},
		Status:       ServerStatusServing,
	}

	clone := provider.Clone()
	assert.Equal(t, provider, clone, "a clone says the same thing the record did")

	clone.Formats[0] = "changed"
	clone.Targets[0] = "changed"
	clone.Transports[0] = "changed"
	clone.ServiceNames[0] = "changed"

	assert.Equal(t, []Format{"protobuf"}, provider.Formats)
	assert.Equal(t, []string{"openapi"}, provider.Targets)
	assert.Equal(t, []Transport{"connectrpc"}, provider.Transports)
	assert.Equal(t, []string{ParserService}, provider.ServiceNames)

	// A record with nothing in those slices clones to nil rather than to an empty
	// slice somebody could append to and share.
	empty := Provider{ID: "bare"}.Clone()
	assert.Nil(t, empty.Formats)
	assert.Nil(t, empty.Targets)
	assert.Nil(t, empty.Transports)
	assert.Nil(t, empty.ServiceNames)
}

func TestAContractPolicyConsultsTheLongerPatternFirst(t *testing.T) {
	// Two rules can name the same number of segments literally and still be
	// different in reach: "registry/*/*" names exactly one method of one service,
	// while "registry/**" names everything below the API. The total is the
	// tie-break that decides which is the more specific one, and without it the
	// answer falls back to the order the lines were written in — so the same
	// document would grant or refuse the same operation depending on which rule
	// came first.
	policy, err := NewPolicy(
		Rule{Pattern: "registry/**", Decision: DecisionDeny},
		Rule{Pattern: "registry/*/*", Decision: DecisionAllow},
	)
	require.NoError(t, err)
	require.Len(t, policy.Rules(), 2)
	assert.Equal(t, "registry/*/*", policy.Rules()[0].Pattern,
		"the rules come back in the order they are consulted, which is what a person reading them back reads")

	assert.True(t, policy.Allows(OperationFacts{ID: "registry/toolbox.registry.v1.RegistryService/Register"}),
		"the pattern that holds more segments in total is the more specific one, whatever order it was written in")
	assert.False(t, policy.Allows(OperationFacts{ID: "registry/toolbox.registry.v1.RegistryService/Register/Extra"}),
		"the more specific rule does not reach a fourth segment, so the rest of the registry stays refused")

	// The other way round, so the assertion cannot pass on the order alone.
	reversed, err := NewPolicy(
		Rule{Pattern: "registry/*/*", Decision: DecisionAllow},
		Rule{Pattern: "registry/**", Decision: DecisionDeny},
	)
	require.NoError(t, err)
	assert.Equal(t, policy.Rules(), reversed.Rules(), "the consulted order does not depend on the written order")
}

func TestAContractPolicyNeverMatchesAnEmptyIdentifier(t *testing.T) {
	// A policy is asked about identifiers that came from a document, and a document
	// can yield an empty one. AllowAll's pattern is a bare "*", which compiles to a
	// trailing rest: without this guard it would match the empty identifier and
	// grant it, so the one policy written to be maximally permissive would answer
	// yes to a request that names no operation at all.
	policy := AllowAll()
	assert.False(t, policy.Allows(OperationFacts{ID: ""}))
	assert.False(t, policy.Allows(OperationFacts{ID: "   "}), "whitespace is not an identifier either")
	assert.False(t, policy.Matched(OperationFacts{ID: ""}), "nothing matched, so a caller is told nothing matched")
	assert.Equal(t, DecisionDeny, policy.Decide(OperationFacts{ID: ""}))
	assert.True(t, policy.Allows(OperationFacts{ID: "knowledge/KnowledgeService/Search"}),
		"an identifier that names an operation is still granted")
}

func TestAContractPolicyTextIsTheDocumentAReaderParses(t *testing.T) {
	// The text form has two entry points — String for printing and Format for
	// storing — and they must be one rendering, because a policy written to a file
	// and a policy printed to a person are read back by the same parser.
	policy, err := NewPolicy(
		Rule{Pattern: "knowledge/**", Classes: []EffectClass{EffectClassRead, EffectClassWrite}, Decision: DecisionAllow},
		Rule{Pattern: "registry/*", Decision: DecisionDeny},
	)
	require.NoError(t, err)

	rendered := policy.Format()
	assert.Equal(t, rendered, policy.String(), "there is one text form, not one per caller")
	assert.Equal(t, "allow knowledge/** read write\ndeny registry/*\n", rendered,
		"the decision comes first so a document reads as statements, the classes come last so a rule that names none is visibly a rule about everything")

	// A policy with no rules renders as no document, which a parser reads back as
	// the policy that refuses everything — the round trip has to hold for the empty
	// policy too, because that is the one a deployment has before it writes rules.
	assert.Equal(t, "", DenyAll().Format())
	assert.Equal(t, "", DenyAll().String())
	assert.Equal(t, "", Policy{}.String())

	reparsed, err := ParsePolicy(rendered)
	require.NoError(t, err)
	assert.Equal(t, policy.Rules(), reparsed.Rules(), "the document a policy renders is the policy it reads back as")
}

func TestAContractEmptyPolicyIsQueryable(t *testing.T) {
	// A catalog asks a policy how many rules it holds and which they are, to report
	// what a deployment authorized. The answer for a policy with no rules is an
	// empty list rather than nil, so a caller that ranges over it and one that
	// serializes it both get the same answer.
	policy, err := NewPolicy()
	require.NoError(t, err)
	assert.Equal(t, 0, policy.Len())
	assert.NotNil(t, policy.Rules())
	assert.Empty(t, policy.Rules())
	assert.False(t, policy.Allows(OperationFacts{ID: "knowledge/KnowledgeService/Search"}))
}
