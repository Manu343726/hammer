package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// serviceDescriptor is the step between what reflection answered and the one service the caller asked
// about, and its three refusals are the difference between an error naming the deployment's problem and a
// nil service that panics in the caller's first use of it. Two of them cannot be provoked through a live
// endpoint — an endpoint that served a file set that does not build, or that answered about a symbol its
// own files do not hold, is a broken deployment rather than a broken caller — so they are asserted here,
// against the same input the reflection stream hands it.

// covFileDescriptor is one file, as reflection sends it.
func covFileDescriptor(name, pkg string, message string) *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Name:        proto.String(name),
		Package:     proto.String(pkg),
		Syntax:      proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String(message)}},
	}
}

// A file set whose imports do not resolve is a reflection answer that cannot be rebuilt: an endpoint that
// dropped a dependency from what it sent. Reporting it names the symbol that was asked for, which is the
// only thing that tells whoever is debugging which deployment sent it.
func TestServiceDescriptorRefusesAFileSetItCannotBuild(t *testing.T) {
	file := covFileDescriptor("cov/dangling.proto", "cov.dangling.v1", "Holder")
	file.Dependency = []string{"cov/absent.proto"}

	_, err := serviceDescriptor([]*descriptorpb.FileDescriptorProto{file}, "cov.dangling.v1.Holder")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "build descriptors for")
	assert.Contains(t, err.Error(), "cov.dangling.v1.Holder")
}

// A file set that builds but does not hold the symbol is a reflection answer about something else — a
// stale cache or a proxy that rewrote the request. Without this refusal the caller would receive a nil
// descriptor and fail at its first method call, where nothing points at the endpoint any more.
func TestServiceDescriptorReportsASymbolItsFilesDoNotHold(t *testing.T) {
	files := []*descriptorpb.FileDescriptorProto{covFileDescriptor("cov/holder.proto", "cov.holder.v1", "Holder")}

	_, err := serviceDescriptor(files, "cov.holder.v1.Missing")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "find service cov.holder.v1.Missing")
}

// A symbol that exists and is not a service — a message name that a caller passed where a service name was
// asked for — has to be refused by name, because the alternative is a descriptor that type-asserts to nil
// and a panic further in.
func TestServiceDescriptorRefusesASymbolThatIsNotAService(t *testing.T) {
	files := []*descriptorpb.FileDescriptorProto{covFileDescriptor("cov/holder.proto", "cov.holder.v1", "Holder")}

	_, err := serviceDescriptor(files, "cov.holder.v1.Holder")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a service")
}

// cloneSchema is what keeps a caller from editing the cache, and a nil schema is the answer for a service
// that was not described. Cloning nothing has to be nothing rather than a panic, and a schema without
// documentation must not arrive with an empty documentation invented for it: a caller checks for nil to
// decide whether to fall back to another source.
func TestCloningNothingAnswersNothing(t *testing.T) {
	assert.Nil(t, cloneSchema(nil))

	clone := cloneSchema(&ServiceSchema{Name: "cov.v1.Undocumented"})
	require.NotNil(t, clone)
	assert.Equal(t, "cov.v1.Undocumented", clone.Name)
	assert.Nil(t, clone.Documentation)
}
