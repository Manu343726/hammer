package protocontract

import (
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A protobuf contract is one IDL and two wire protocols, and the format identity
// has to say so. A caller announces a service as Connect or as gRPC depending on
// what it speaks; a parser that only answered yes to one of them would be deployed
// and unreachable for the other, and the caller would have no way to tell that from
// a contract it could not read.

func TestFormatsAndTargetsCoverBothProtocols(t *testing.T) {
	assert.True(t, HandlesFormat(Format), "gRPC is a format this package reads")
	assert.True(t, HandlesFormat(FormatConnect), "Connect is the same contract over another protocol")
	assert.True(t, HandlesFormat(" CONNECT "), "an announced identifier is normalized before it is compared")
	assert.True(t, HandlesFormat("GRPC"))
	assert.False(t, HandlesFormat("openapi"), "a format this package does not read is not claimed")

	assert.True(t, HandlesTarget(Format))
	assert.True(t, HandlesTarget(FormatConnect))
	assert.False(t, HandlesTarget("openapi"))

	assert.Equal(t, []api.Format{Format, FormatConnect}, Formats())
	assert.Equal(t, []string{Format, FormatConnect}, Targets(),
		"one contract is the artifact for either protocol, so both are targets")
}

func TestDescriptorClaimsBothFormats(t *testing.T) {
	reader := NewDescriptor(Descriptor{})
	assert.Equal(t, []api.Format{Format, FormatConnect}, reader.Formats())
	require.Len(t, reader.FormatDescriptors(), 2)
	assert.Equal(t, string(Format), reader.FormatDescriptors()[0].ID)
	assert.Equal(t, string(FormatConnect), reader.FormatDescriptors()[1].ID)
}

func TestConnectDescriptorDescribesTheSameContract(t *testing.T) {
	grpc := FormatDescriptor()
	connect := ConnectFormatDescriptor()

	assert.Equal(t, string(FormatConnect), connect.ID)
	assert.Equal(t, "Connect", connect.Name)
	assert.Equal(t, grpc.SpecificationVersion, connect.SpecificationVersion, "one contract has one specification version")
	assert.Equal(t, "*.proto", connect.Metadata["artifact"])
	assert.Equal(t, string(Format), connect.Metadata["contract-format"],
		"the Connect identifier points at the gRPC one, because it is the same description language")
	assert.Contains(t, connect.Description, "same protobuf service contract")
	require.NoError(t, connect.Validate())
}

func TestTransportDescriptorsSayTheyCarryOneContract(t *testing.T) {
	// The relationship belongs in the descriptors rather than in prose a caller
	// never reads: a consumer that indexed these transports has to be able to tell
	// that choosing between them does not change the contract it was given.
	transports := TransportDescriptors()
	require.Len(t, transports, 3)
	for _, transport := range transports {
		require.NoError(t, transport.Validate())
		assert.Equal(t, string(Format), transport.Metadata["contract-format"], transport.ID)
		assert.Equal(t, "*.proto", transport.Metadata["artifact"], transport.ID)
		assert.Contains(t, transport.Description, "same protobuf contract", transport.ID)
	}

	// Metadata is per descriptor, not shared: one mutated descriptor must not
	// silently rewrite what the catalog says about another.
	transports[0].Metadata["artifact"] = "something-else"
	assert.Equal(t, "*.proto", TransportDescriptors()[0].Metadata["artifact"])
}
