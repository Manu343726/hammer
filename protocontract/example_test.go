package protocontract_test

import (
	"fmt"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/internal/testcontract/testcontractv1"
	"github.com/Manu343726/hammer/protocontract"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Godoc examples are the documentation a caller copies, so they are compiled and
// run as part of the test suite. Only the deterministic ones carry an output block:
// an example whose output depended on a map iteration would fail for a reason that
// has nothing to do with the code it documents.

func ExampleRender() {
	// A description is rendered into one .proto file plus the FileDescriptorSet
	// compiled from it. The compile is part of rendering, so an artifact a parser
	// cannot read is an error here rather than a surprise for the consumer.
	described := api.API{
		ID: "echo", Name: "echo", Format: protocontract.Format,
		Services: []api.Service{{
			Name:       "echo.v1.EchoService",
			Operations: []api.Operation{{Name: "Echo", SideEffects: []api.SideEffect{api.SideEffectReadOnly}}},
		}},
	}

	rendered, err := protocontract.Render(described, protocontract.RenderOptions{})
	if err != nil {
		panic(err)
	}

	for _, file := range rendered.Files {
		if file.Primary {
			fmt.Println(file.Name, file.MediaType)
		}
	}
	// Output:
	// contract.proto text/x-proto
}

func ExampleDescriptor_FromDescriptorSet() {
	// A compiled contract is read into the standard description, which is the shape
	// the rest of the framework consumes: no caller translates between a protobuf
	// shape and an API.
	described, warnings, err := protocontract.NewDescriptor(protocontract.Descriptor{APIID: "fixture"}).
		FromDescriptorSet(covFixtureDescriptorSet())
	if err != nil {
		panic(err)
	}

	fmt.Println(described.ID, len(described.Services), len(warnings))
	for _, service := range described.Services {
		fmt.Println(service.Name)
	}
	// Output:
	// fixture 3 0
	// testcontract.v1.ApiAdapterService
	// testcontract.v1.ApiInvokerService
	// testcontract.v1.ApiParserService
}

func ExampleFormats() {
	// Two protocol identifiers over one contract. A caller that speaks Connect is not
	// handing this package a different document, so both are claimed.
	for _, format := range protocontract.Formats() {
		fmt.Println(format)
	}
	// Output:
	// grpc
	// connect
}

func TestCoverExamplesDocumentWhatThePackageClaims(t *testing.T) {
	// The examples print facts about the package, and this keeps them true: an
	// example that documents the wrong answer is worse than no example, because it
	// is the one a reader copies.
	assert.Equal(t, []api.Format{protocontract.Format, protocontract.FormatConnect}, protocontract.Formats())
	assert.Equal(t, "testcontract.v1", string(testcontractv1.File_testcontract_proto.Package()))
	assert.NotEmpty(t, covFixtureDescriptorSet())
}

// covFixtureDescriptorSet is the checked-in contract as the bytes a build produces.
// The example depends on the fixture rather than on a hand-built descriptor, so it
// documents the path a deployment takes.
func covFixtureDescriptorSet() []byte {
	document, err := proto.Marshal(&descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(testcontractv1.File_testcontract_proto)},
	})
	if err != nil {
		panic(err)
	}
	return document
}
