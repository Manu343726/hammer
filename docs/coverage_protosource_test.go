package docs_test

import (
	"testing"

	shareddocs "github.com/Manu343726/hammer/docs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

// The source registry is the only thing standing between a generated descriptor — structure with no
// prose — and the documentation a contract declared. What is tested here is its failure behaviour,
// because that is what nothing else checks: a broken .proto, an import nobody has, and a path nobody
// registered all have to say what is wrong instead of answering with a documentation that quietly has
// nothing in it.

func TestASourceNeedsAPathAndContent(t *testing.T) {
	// The path is the key reflection is asked for and the key a generated descriptor arrives under. A
	// registration that accepted an empty one would be documentation nothing can ever find, which is
	// indistinguishable from a contract with no comments at all.
	assert.Error(t, shareddocs.RegisterProtoSource("", []byte(covFullSource)))
	assert.Error(t, shareddocs.RegisterProtoSource("   ", []byte(covFullSource)))
	assert.Error(t, shareddocs.RegisterProtoSource("cov/empty.proto", nil))
	assert.Error(t, shareddocs.RegisterProtoSource("cov/empty.proto", []byte("")))
}

// Asking for a source nobody registered is a wiring mistake — a package that embedded its .proto and
// forgot the registration, or a mismatch between the path protoc gave the file and the path reflection
// reports. Answering with an empty descriptor would make that look like a contract with no
// documentation rather than like a registration that never happened.
func TestCompilingASourceNobodyRegisteredIsRefused(t *testing.T) {
	_, err := shareddocs.CompileProtoSource("cov/never-registered.proto")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cov/never-registered.proto", "the error must name the path it was asked for")
	assert.Contains(t, err.Error(), "registered")
}

// A .proto that does not compile is refused once and then remembered. It is refused rather than
// panicking here because the registered-source path is also how a test or a tool builds a fixture; the
// remembering is what stops a broken contract from being re-parsed on every help request, and a failure
// that was not cached would be a process spending its time on the same syntax error.
func TestASourceThatDoesNotCompileIsRefusedAndTheFailureIsRemembered(t *testing.T) {
	const path = "cov/broken.proto"
	require.NoError(t, shareddocs.RegisterProtoSource(path, []byte("this is not a proto file")))

	_, first := shareddocs.CompileProtoSource(path)
	require.Error(t, first)
	assert.Contains(t, first.Error(), "does not compile")

	_, second := shareddocs.CompileProtoSource(path)
	require.Error(t, second)
	assert.Equal(t, first.Error(), second.Error(), "the second attempt compiled the source again instead of reporting the same failure")
}

// When a registered source cannot be compiled, documentation falls back to the descriptor as it arrived
// rather than answering nothing. A broken registration is a defect in the package that shipped it, and
// the caller is still better served by the structure it can read than by an empty answer that hides
// which contract is at fault.
func TestABrokenSourceFallsBackToTheDescriptorThatArrived(t *testing.T) {
	// The same path as the source registered above, so this is the descriptor the registry would have
	// been used to document.
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    proto.String("cov/broken.proto"),
		Package: proto.String("cov.broken.v1"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: proto.String("BrokenResponse")},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("BrokenService"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("Run"),
				InputType:  proto.String(".cov.broken.v1.BrokenResponse"),
				OutputType: proto.String(".cov.broken.v1.BrokenResponse"),
			}},
		}},
	}, nil)
	require.NoError(t, err)

	documentation := shareddocs.ExtractServiceDocumentation(file.Services().Get(0))

	require.NotNil(t, documentation)
	assert.Equal(t, "cov.broken.v1.BrokenService", documentation.Name)
	assert.Empty(t, documentation.Description, "a source that does not compile cannot describe anything")
	require.Len(t, documentation.Methods, 1, "the structure is still readable")
	assert.Equal(t, "cov.broken.v1.BrokenResponse", documentation.Methods[0].InputType)
}

// An import resolves either from a source this process registered or from the descriptors it already
// linked. The second case is what lets a package document its own contract without embedding every
// .proto it imports, and the first is what a package does for the file it owns.
func TestAnImportResolvesFromTheDescriptorsLinkedIntoTheProcess(t *testing.T) {
	const path = "cov/linked.proto"
	require.NoError(t, shareddocs.RegisterProtoSource(path, []byte(`syntax = "proto3";

package cov.linked.v1;

import "google/protobuf/descriptor.proto";

// LinkedService exists to prove that an import this process links resolves.
service LinkedService {
  // Wrap wraps a descriptor.
  rpc Wrap(Wrapper) returns (Wrapper);
}

// Wrapper holds a descriptor from the protobuf runtime.
message Wrapper {
  // The descriptor.
  google.protobuf.FileDescriptorProto file = 1;
}
`)))

	compiled, err := shareddocs.CompileProtoSource(path)
	require.NoError(t, err, "an import that is linked into the process did not resolve")

	documentation := shareddocs.ExtractServiceDocumentation(compiled.Services().Get(0))
	require.NotNil(t, documentation)
	assert.Contains(t, documentation.Description, "prove that an import this process links")
	require.Len(t, documentation.Methods[0].Parameters, 1)
	// The imported type is reported by its name and not flattened, because its fields belong to a file
	// whose source paths this one does not have.
	assert.Equal(t, "google.protobuf.FileDescriptorProto", documentation.Methods[0].Parameters[0].TypeName)
	assert.Empty(t, documentation.Methods[0].Parameters[0].Fields)
}

// An import that is neither registered nor linked is a contract this process cannot compile, and the
// answer has to be an error naming the file: a compile that silently substituted an empty descriptor
// would produce documentation for a contract the caller never wrote.
func TestAnImportNobodyHasIsReported(t *testing.T) {
	const path = "cov/missing-import.proto"
	require.NoError(t, shareddocs.RegisterProtoSource(path, []byte(`syntax = "proto3";

package cov.missing.v1;

import "cov/absent.proto";

message Holder {
  cov.absent.v1.Thing thing = 1;
}
`)))

	_, err := shareddocs.CompileProtoSource(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cov/missing-import.proto")
	assert.Contains(t, err.Error(), "cov/absent.proto", "the error must name the import that could not be resolved")
}
