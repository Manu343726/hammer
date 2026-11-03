package docs_test

import (
	"fmt"
	"sync"
	"testing"

	shareddocs "github.com/Manu343726/hammer/docs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// A catalog is the lookup every help page, every generated command and every agent tool description
// is built from. What is tested here is what a caller gets back: the prose and the annotations of a
// contract, whether a service that was never registered is distinguishable from one that is
// undocumented, and that two catalogs in one process cannot see each other's entries. The last one
// is the defect that makes a test flaky in a way nobody can explain: a package-wide registry means
// one package's fixture documents another package's contract.

const (
	covSharedPath = "cov/shared.proto"
	covFullPath   = "cov/full.proto"
)

// covSharedSource is a second file, imported by the main fixture, because a field whose message type
// lives in another file takes a different path through the flattening than one declared beside it.
const covSharedSource = `syntax = "proto3";

package cov.shared.v1;

// Shared is a document declared in another file.
message Shared {
  // The document's identifier.
  string id = 1;
}
`

// covFullSource is the fixture every extraction test in this file describes. It holds one of each
// shape the model can report: a service with a comment and three annotations, a method with a comment
// and one without, an annotation-only method comment, a server-streaming method, a scalar with an
// explicit default, an enum field, a field whose type lives in another file, a field whose type is
// declared inside the request, and a field with no comment at all.
const covFullSource = `syntax = "proto2";

package cov.full.v1;

import "cov/shared.proto";

enum Kind {
  KIND_UNSPECIFIED = 0;
  KIND_FAST = 1;
}

// DocumentedService is described from this comment.
//
// @cov.side-effects read_only
// @cov.side-effects write
// @cov.deprecated
service DocumentedService {
  // Stream streams what Unannotated would return.
  //
  // @cov.audit
  rpc Stream(StreamRequest) returns (stream StreamResponse);

  // @cov.side-effects write_only
  rpc Unannotated(PlainRequest) returns (PlainResponse);

  rpc NoComment(PlainRequest) returns (PlainResponse);
}

// StreamRequest is what a stream is asked for.
message StreamRequest {
  // How many times to retry.
  optional int32 retries = 1 [default = 3];

  // What kind of thing to stream.
  optional Kind kind = 2 [default = KIND_FAST];

  // A document declared in another file.
  optional cov.shared.v1.Shared shared = 3;

  // Options for one stream.
  optional Nested nested = 4;

  optional string bare = 5;

  // Nested is declared inside the request: its documentation is only reachable
  // when the walk goes into the message that declares it.
  message Nested {
    // How deep to go.
    optional int32 depth = 1;
  }
}

// StreamResponse is one streamed document.
message StreamResponse {
  optional string id = 1;
}

service UndocumentedService {
  rpc Run(PlainRequest) returns (PlainResponse);
}

// PlainRequest is the body of a method with nothing to say.
message PlainRequest {
  optional string id = 1;
}

// PlainResponse is the answer to it.
message PlainResponse {
  optional string id = 1;
}
`

// covFixtureOnce guards the registration because a proto source is registered once per process: a
// second registration is refused, which is the behaviour another test in this package pins.
var covFixtureOnce sync.Once

// covFullFixture returns the descriptor compiled from covFullSource.
func covFullFixture(t *testing.T) protoreflect.FileDescriptor {
	t.Helper()
	covFixtureOnce.Do(func() {
		for path, source := range map[string]string{covSharedPath: covSharedSource, covFullPath: covFullSource} {
			if err := shareddocs.RegisterProtoSource(path, []byte(source)); err != nil {
				panic(fmt.Sprintf("the fixture at %s cannot be registered: %v", path, err))
			}
		}
	})
	file, err := shareddocs.CompileProtoSource(covFullPath)
	require.NoError(t, err, "the fixture must compile")
	return file
}

// covStrippedFixture returns the same contract as a descriptor that carries no source info, which is
// what a generated .pb.go or a --include_source_info-less descriptor set gives a caller.
func covStrippedFixture(t *testing.T) protoreflect.FileDescriptor {
	t.Helper()
	compiled := covFullFixture(t)

	shared := protodesc.ToFileDescriptorProto(compiled.Imports().Get(0).FileDescriptor)
	shared.SourceCodeInfo = nil
	full := protodesc.ToFileDescriptorProto(compiled)
	full.SourceCodeInfo = nil

	files, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{shared, full},
	})
	require.NoError(t, err)
	file, err := files.FindFileByPath(covFullPath)
	require.NoError(t, err)
	return file
}

// covService extracts one service of the fixture, failing the test rather than returning an empty
// documentation a later assertion could quietly pass on.
func covService(t *testing.T, file protoreflect.FileDescriptor, name string) shareddocs.Service {
	t.Helper()
	service := file.Services().ByName(protoreflect.Name(name))
	require.NotNil(t, service, "the fixture declares no service named %q", name)
	documentation := shareddocs.ExtractServiceDocumentation(service)
	require.NotNil(t, documentation)
	return *documentation
}

// A descriptor that arrived without its comments still documents, as long as the process has the
// .proto it was generated from. This is the whole point of the source registry: every generated
// .pb.go carries the structure and none of the prose, so a reader that trusted the generated
// descriptor would serve help with descriptions and annotations stripped — silently, one field at a
// time. The assertion is that the prose is there *because* the source was registered, so removing the
// preference shows up here rather than in every help page.
func TestADescriptorWithoutCommentsIsDocumentedFromTheRegisteredSource(t *testing.T) {
	documentation := covService(t, covStrippedFixture(t), "DocumentedService")

	assert.Equal(t, "cov.full.v1.DocumentedService", documentation.Name)
	assert.Contains(t, documentation.Description, "described from this comment",
		"the prose was taken from the descriptor, which has none, instead of from the registered source")
	require.Len(t, documentation.Methods, 3)
	assert.Contains(t, documentation.Methods[0].Description, "Stream streams",
		"a method's comment was lost with the source info")
	require.NotEmpty(t, documentation.Methods[0].Parameters)
	assert.Equal(t, "How many times to retry.", documentation.Methods[0].Parameters[0].Description,
		"a field's comment was lost with the source info")
}

// Annotations are read from the same comments the prose is, so they survive the same trip. A service
// that declares a side effect and arrives with an empty annotation set is an operation a policy
// cannot classify — the state that makes a call ungrantable rather than misclassified.
func TestAnnotationsDeclaredByTheContractReachTheCaller(t *testing.T) {
	documentation := covService(t, covFullFixture(t), "DocumentedService")

	values, ok := documentation.Annotations.Lookup("cov.side-effects")
	require.True(t, ok, "the service's annotation was lost")
	assert.Equal(t, []string{"read_only"}, values)

	// A valueless flag is a declaration too, and dropping it would make "deprecated" look like
	// "nothing was said".
	_, ok = documentation.Annotations.Lookup("cov.deprecated")
	assert.True(t, ok, "a valueless annotation was dropped")

	require.Len(t, documentation.Methods, 3)
	_, ok = documentation.Methods[0].Annotations.Lookup("cov.audit")
	assert.True(t, ok, "a method's annotation was lost")
}

// A method whose comment is nothing but an annotation has no description, and that is not the same
// as having lost one: the annotation line must be extracted and the prose must be empty, never the
// annotation text repeated as prose.
func TestAMethodWhoseCommentIsOnlyAnnotationsHasNoDescription(t *testing.T) {
	documentation := covService(t, covFullFixture(t), "DocumentedService")
	require.Len(t, documentation.Methods, 3)

	unannotated := documentation.Methods[1]
	require.Equal(t, "Unannotated", unannotated.Name)
	assert.Empty(t, unannotated.Description, "the annotation line leaked into the description")
	values, ok := unannotated.Annotations.Lookup("cov.side-effects")
	require.True(t, ok)
	assert.Equal(t, []string{"write_only"}, values)
}

// A service or a field with no comment is documented as having nothing to say, and the difference
// between "no comment" and "no contract" is the difference between an undocumented method and a
// missing one. An extraction that failed instead would make an undocumented contract unusable.
func TestAServiceAndAFieldWithNoCommentAreDocumentedAndEmpty(t *testing.T) {
	file := covFullFixture(t)

	noComment := covService(t, file, "DocumentedService").Methods[2]
	require.Equal(t, "NoComment", noComment.Name)
	assert.Empty(t, noComment.Description)
	assert.Empty(t, noComment.Annotations, "an unannotated method must not inherit annotations it never declared")

	// The request's last field carries no comment of its own.
	documentation := covService(t, file, "DocumentedService")
	parameters := documentation.Methods[0].Parameters
	require.NotEmpty(t, parameters)
	bare := parameters[len(parameters)-1]
	require.Equal(t, "bare", bare.Name)
	assert.Empty(t, bare.Description, "a field with no comment was given one from a neighbouring field")
	assert.Equal(t, "string", bare.TypeName)

	// A service with no comment is described as having nothing to say, and it is described at all: the
	// structure is still there for a caller to call, which is the difference between an undocumented
	// service and a missing one.
	undocumented := covService(t, file, "UndocumentedService")
	assert.Empty(t, undocumented.Description)
	assert.Empty(t, undocumented.Annotations)
	require.Len(t, undocumented.Methods, 1)
	assert.Equal(t, "Run", undocumented.Methods[0].Name)
	assert.Empty(t, undocumented.Methods[0].Description)
	assert.Equal(t, "cov.full.v1.PlainRequest", undocumented.Methods[0].InputType)
}

// A parameter's type, default and shape are what a caller builds a request from. A field reported
// with the wrong type name is a value that does not encode, and a repeated field reported as singular
// silently loses everything after the first value.
func TestFieldTypesDefaultsAndNestingAreReported(t *testing.T) {
	documentation := covService(t, covFullFixture(t), "DocumentedService")
	require.Len(t, documentation.Methods, 3)
	parameters := documentation.Methods[0].Parameters
	require.Len(t, parameters, 5)

	retries := parameters[0]
	assert.Equal(t, "int32", retries.TypeName)
	assert.Equal(t, "3", retries.DefaultValue, "an explicit default is what a caller leaves the flag at")
	assert.True(t, retries.Required, "a proto2 optional field has presence, so a caller has to decide it")

	// An enum field is reported by the enum's name rather than by the wire kind: "enum" is not a type
	// a caller can build a value of.
	kind := parameters[1]
	assert.Equal(t, "cov.full.v1.Kind", kind.TypeName)
	// Its default is rendered as the number the value carries, not as the constant's name. That is what
	// a flag's default text is built from, so it is asserted rather than left to be discovered: a
	// caller sees "1" where the contract said KIND_FAST.
	assert.Equal(t, "1", kind.DefaultValue)

	// A message type declared in another file is reported by name and not flattened: this process has
	// the other file, but the parameter does not, so sub-fields reached from here would be attributed
	// to the wrong message's source paths.
	shared := parameters[2]
	assert.Equal(t, "cov.shared.v1.Shared", shared.TypeName)
	assert.Empty(t, shared.Fields, "a field from another file was flattened into this one's parameters")

	// A message declared inside the request is flattened, and its own comment comes with it: a
	// sub-field reached but not described is a flag with no help.
	nested := parameters[3]
	require.Equal(t, "cov.full.v1.StreamRequest.Nested", nested.TypeName)
	require.Len(t, nested.Fields, 1)
	assert.Equal(t, "depth", nested.Fields[0].Name)
	assert.Equal(t, "How deep to go.", nested.Fields[0].Description)

	// A server-streaming method is reported as one, because a caller that treats it as unary reads
	// one message and drops the rest of the stream.
	assert.True(t, documentation.Methods[0].ServerStreaming)
	assert.False(t, documentation.Methods[0].ClientStreaming)
	assert.False(t, documentation.Methods[1].ServerStreaming)
}

// Two catalogs must not see each other. A package-wide registry is the shape that makes one package's
// test fixture document another package's contract, and the resulting failure is a test that passes or
// fails depending on which package ran first.
func TestTwoCatalogsDoNotSeeEachOther(t *testing.T) {
	first := shareddocs.NewCatalog()
	second := shareddocs.NewCatalog()

	require.NoError(t, first.Add(shareddocs.Service{Name: "cov.first.Service", Description: "in the first"}))
	require.NoError(t, second.Add(shareddocs.Service{Name: "cov.second.Service", Description: "in the second"}))

	_, err := first.Get("cov.second.Service")
	assert.ErrorIs(t, err, shareddocs.ErrNotFound, "a catalog answered with an entry registered in another one")

	require.Len(t, first.List(), 1)
	assert.Equal(t, "cov.first.Service", first.List()[0].Name)
	require.Len(t, second.List(), 1)
	assert.Equal(t, "cov.second.Service", second.List()[0].Name)
}

// The catalog is read while it is written: a host registers every contract it links while the first
// requests are already being described. Without the lock this is a data race on a map, which is what
// -race reports as a corrupted read rather than as a wrong answer.
func TestConcurrentRegistrationAndLookupIsSafe(t *testing.T) {
	catalog := shareddocs.NewCatalog()
	var wait sync.WaitGroup

	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("cov.concurrent.Service%d", i)
		wait.Add(2)
		go func() {
			defer wait.Done()
			assert.NoError(t, catalog.Add(shareddocs.Service{Name: name, Description: name}))
		}()
		go func() {
			defer wait.Done()
			// A miss is a legitimate answer while another goroutine is still registering, so the
			// error is not the assertion; the assertion is that neither answer is a race.
			_, _ = catalog.Get(name)
			_ = catalog.List()
		}()
	}
	wait.Wait()

	assert.Len(t, catalog.List(), 8)
}

// List is the snapshot a host decides what exists from, so it has to be ordered and it has to be a
// copy: a caller that sorts or edits the result must not reorder or corrupt the catalog behind it.
func TestListIsOrderedAndIndependentOfTheCatalog(t *testing.T) {
	catalog := shareddocs.NewCatalog()
	for _, name := range []string{"cov.z.Service", "cov.a.Service", "cov.m.Service"} {
		require.NoError(t, catalog.Add(shareddocs.Service{Name: name}))
	}

	listed := catalog.List()
	require.Len(t, listed, 3)
	assert.Equal(t, []string{"cov.a.Service", "cov.m.Service", "cov.z.Service"},
		[]string{listed[0].Name, listed[1].Name, listed[2].Name}, "the listing is not deterministic")

	listed[0].Name = "corrupted"
	assert.Equal(t, "cov.a.Service", catalog.List()[0].Name, "the catalog handed out its own entry")
}

// A descriptor set is how a package ships its contract without a generated registration file. Adding
// one documents every service in it, and the source info the set carries is where the prose comes from
// — so a set generated without --include_source_info produces structure and no descriptions, which is
// exactly the failure this path exists to make visible.
func TestADescriptorSetDocumentsEveryServiceItCarries(t *testing.T) {
	compiled := covFullFixture(t)
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
		protodesc.ToFileDescriptorProto(compiled.Imports().Get(0).FileDescriptor),
		protodesc.ToFileDescriptorProto(compiled),
	}}
	encoded, err := proto.Marshal(set)
	require.NoError(t, err)

	catalog := shareddocs.NewCatalog()
	require.NoError(t, catalog.AddDescriptorSet(encoded))

	documented, err := catalog.Get("cov.full.v1.DocumentedService")
	require.NoError(t, err)
	assert.Contains(t, documented.Description, "described from this comment")
	require.Len(t, documented.Methods, 3)

	// An empty set is a build that produced nothing, which a caller has to be told rather than handed
	// as an empty catalog it would report as a package with no contracts.
	assert.ErrorIs(t, shareddocs.NewCatalog().AddDescriptorSet(nil), shareddocs.ErrInvalidDescriptor)

	// Bytes that are not a descriptor set at all are a wrong file, not an empty one, and the two need
	// different answers from whoever generated it.
	assert.Error(t, shareddocs.NewCatalog().AddDescriptorSet([]byte("not a descriptor set")))
}

// A set whose files do not fit together is a build that produced an unusable contract: a file that
// imports something the set does not carry cannot be described, and answering with an empty error
// would leave the caller with a catalog that silently holds fewer services than the set declared.
func TestADescriptorSetWhoseImportsAreMissingIsRefused(t *testing.T) {
	encoded, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:       proto.String("cov/dangling.proto"),
		Package:    proto.String("cov.dangling.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"cov/absent.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Holder"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("thing"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".cov.absent.v1.Thing"),
			}},
		}},
	}}})
	require.NoError(t, err)

	err = shareddocs.NewCatalog().AddDescriptorSet(encoded)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "build descriptor set")
}

// RegisterEmbeddedFile is the call a generated registration file makes from init. It has to reach the
// default catalog, because that is the catalog a reader falls back to when nothing else gave it
// documentation.
func TestRegisteringAnEmbeddedFileReachesTheDefaultCatalog(t *testing.T) {
	compiled := covFullFixture(t)
	encoded, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
		protodesc.ToFileDescriptorProto(compiled.Imports().Get(0).FileDescriptor),
		protodesc.ToFileDescriptorProto(compiled),
	}})
	require.NoError(t, err)

	require.NoError(t, shareddocs.RegisterEmbeddedFile(encoded))
	documented, err := shareddocs.DefaultCatalog().Get("cov.full.v1.DocumentedService")
	require.NoError(t, err, "a registered descriptor set did not reach the default catalog")
	assert.Contains(t, documented.Description, "described from this comment")
}

// AddGlobalRegistry documents every service linked into the process. In this test binary nothing links
// a service, so what is asserted is that the walk completes over the descriptors the protobuf runtime
// itself links — the case that would otherwise fail on the first file with no services.
func TestAddGlobalRegistryWalksEveryLinkedFile(t *testing.T) {
	catalog := shareddocs.NewCatalog()

	require.NoError(t, catalog.AddGlobalRegistry(), "the linked descriptors could not be documented")
	_, err := catalog.Get("cov.full.v1.DocumentedService")
	assert.ErrorIs(t, err, shareddocs.ErrNotFound, "the walk reached a source that is registered but not linked")
}

// A service the catalog does not hold is an error and not an empty documentation: "nothing is known"
// and "nothing was said" are different answers, and a caller that conflated them would render an
// undocumented contract instead of falling back to another source.
func TestAServiceThatWasNeverRegisteredIsNotFound(t *testing.T) {
	catalog := shareddocs.NewCatalog()

	_, err := catalog.Get("cov.full.v1.NotRegistered")
	require.ErrorIs(t, err, shareddocs.ErrNotFound)
	assert.Contains(t, err.Error(), "cov.full.v1.NotRegistered", "the error must name what was looked for")
}

// covNamelessService presents a service whose fully-qualified name is empty. protodesc refuses to build
// one, so a descriptor the protobuf runtime produced can never look like this; it is the shape a
// hand-parsed or hand-built descriptor can have, and the catalog has to refuse it rather than store an
// entry no caller can look up.
type covNamelessService struct{ protoreflect.ServiceDescriptor }

func (covNamelessService) FullName() protoreflect.FullName { return "" }

// covServiceList replaces the services a file reports, keeping the real descriptor behind them so the
// extraction still has something to read.
type covServiceList struct {
	protoreflect.ServiceDescriptors
}

func (list covServiceList) Get(int) protoreflect.ServiceDescriptor {
	return covNamelessService{list.ServiceDescriptors.Get(0)}
}

// covFileWithServices is a real file descriptor reporting an altered service list.
type covFileWithServices struct{ protoreflect.FileDescriptor }

func (file covFileWithServices) Services() protoreflect.ServiceDescriptors {
	return covServiceList{file.FileDescriptor.Services()}
}

// A file is added as a whole, so a service in it that cannot be stored fails the file rather than
// being skipped: a contract that half registered is one whose caller cannot tell which of its methods
// exist.
func TestAFileWhoseServiceCannotBeDocumentedIsRefused(t *testing.T) {
	err := shareddocs.NewCatalog().AddFile(covFileWithServices{covFullFixture(t)})

	require.ErrorIs(t, err, shareddocs.ErrInvalidDescriptor)
}

// covFilelessService presents a service whose parent file is missing. Nothing the protobuf runtime
// builds looks like this either, but the extraction takes an interface and a caller can hand it one
// from a reflection response whose file it did not keep — so the answer has to be "nothing is
// documented" rather than a nil dereference.
type covFilelessService struct{ protoreflect.ServiceDescriptor }

func (covFilelessService) ParentFile() protoreflect.FileDescriptor { return nil }

func TestExtractingAServiceWhoseFileIsMissingAnswersNothingRatherThanPanicking(t *testing.T) {
	service := covFullFixture(t).Services().ByName("DocumentedService")
	require.NotNil(t, service)

	documentation := shareddocs.ExtractServiceDocumentation(covFilelessService{service})

	require.NotNil(t, documentation)
	assert.Equal(t, "cov.full.v1.DocumentedService", documentation.Name,
		"the structure is still readable even when the file it came from is not")
	assert.Empty(t, documentation.Description)
	require.Len(t, documentation.Methods, 3, "the methods are reachable through the service itself")
	assert.Empty(t, documentation.Methods[0].Description)
	assert.Empty(t, documentation.Methods[0].Parameters,
		"a method whose input type lives in a missing file has no parameters to report")
}
