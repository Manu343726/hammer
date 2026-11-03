package protocontract_test

import (
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/protocontract"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One contract, two protocols. A caller announces a service as Connect or as gRPC
// depending on what it speaks, and both announcements have to reach a description:
// a reader that claimed only one would be deployed and unreachable for the other,
// and the caller could not tell that from a contract it cannot read.
//
// The claims are held here at the level an index consumes them, because that is
// where the relationship is published: a catalog records formats and transports,
// not Go functions.

func TestCoverFormatHandlesBothProtocols(t *testing.T) {
	for _, format := range []api.Format{protocontract.Format, protocontract.FormatConnect} {
		assert.True(t, protocontract.HandlesFormat(format), "format %q", format)
		assert.True(t, protocontract.HandlesTarget(string(format)), "target %q", format)
	}

	// An announced identifier arrives from configuration, so case and padding are
	// the caller's, not a reason to refuse the contract.
	assert.True(t, protocontract.HandlesFormat(" CONNECT "))
	assert.True(t, protocontract.HandlesFormat("Connect"))
	assert.True(t, protocontract.HandlesTarget(" gRPC "))

	// A format this package does not read is not claimed, because claiming it would
	// make a caller hand over a document this reader would then refuse.
	assert.False(t, protocontract.HandlesFormat("openapi"))
	assert.False(t, protocontract.HandlesTarget("openapi"))
	assert.False(t, protocontract.HandlesFormat(""))
	assert.False(t, protocontract.HandlesTarget(""))
}

func TestCoverFormatSaysWhichFormatsAndTargetsItCovers(t *testing.T) {
	assert.Equal(t, []api.Format{protocontract.Format, protocontract.FormatConnect}, protocontract.Formats(),
		"an index of readable formats has to answer yes to a caller that announced Connect")
	assert.Equal(t, []string{protocontract.Format, protocontract.FormatConnect}, protocontract.Targets(),
		"the target is the contract, which is one artifact for either protocol")

	// The package-level descriptors are what an index registers when no reader
	// instance exists yet, and both identifiers have to be described there.
	descriptors := protocontract.FormatDescriptors()
	require.Len(t, descriptors, 2)
	assert.Equal(t, string(protocontract.Format), descriptors[0].ID)
	assert.Equal(t, string(protocontract.FormatConnect), descriptors[1].ID)
	for _, descriptor := range descriptors {
		require.NoError(t, descriptor.Validate(), descriptor.ID)
		assert.Equal(t, "3", descriptor.SpecificationVersion,
			"%q describes the same description language, so it carries the same specification version", descriptor.ID)
		assert.Equal(t, "*.proto", descriptor.Metadata["artifact"], descriptor.ID)
	}
}

func TestCoverFormatConnectNamesTheContractItShares(t *testing.T) {
	connect := protocontract.ConnectFormatDescriptor()
	assert.Equal(t, string(protocontract.FormatConnect), connect.ID)
	assert.Equal(t, "Connect", connect.Name)
	assert.Equal(t, string(protocontract.Format), connect.Metadata["contract-format"],
		"the Connect identifier points at the contract format, because it is not a second description language")
	assert.Equal(t, protocontract.FileExtensionContract, "proto")

	// The two descriptors differ only in identity, not in what they promise: a
	// consumer that recorded one as protobuf 3 and the other as nothing would be
	// answering the same question twice with different answers.
	grpc := protocontract.FormatDescriptor()
	assert.Equal(t, grpc.SpecificationVersion, connect.SpecificationVersion)
	assert.Equal(t, grpc.FileExtensions, connect.FileExtensions)
	assert.Equal(t, grpc.MediaTypes, connect.MediaTypes)
	assert.NotEqual(t, grpc.Description, connect.Description)
}

func TestCoverFormatTransportDescriptorsCarryOneContract(t *testing.T) {
	// A transport is how a caller reaches a server, never what a service declares.
	// The relationship belongs in the descriptors, because a consumer that indexed
	// them has to be able to tell that switching transports does not change the
	// contract it was given.
	transports := protocontract.TransportDescriptors()
	require.Len(t, transports, 3)

	ids := make([]string, 0, len(transports))
	for _, transport := range transports {
		ids = append(ids, transport.ID)
		require.NoError(t, transport.Validate(), transport.ID)
		assert.Equal(t, string(protocontract.Format), transport.Metadata["contract-format"], transport.ID)
		assert.Equal(t, "*.proto", transport.Metadata["artifact"], transport.ID)
		assert.Contains(t, transport.Description, "same protobuf contract", transport.ID)
	}
	assert.Equal(t, []string{"connectrpc", "grpc", "grpc-web"}, ids)

	// The wire protocols are also the callers' identifiers, and this package calls
	// over exactly what it declares.
	for _, transport := range transports {
		assert.True(t, protocontract.HandlesTransport(api.Transport(transport.ID)), transport.ID)
	}
	assert.False(t, protocontract.HandlesTransport("http"), "a transport this package does not call is not claimed")
	assert.False(t, protocontract.HandlesTransport(""))
}

func TestCoverFormatStringRendersTheIdentifier(t *testing.T) {
	// A message about a format spells the identifier as the description carries it,
	// rather than as a Go value a caller would have to interpret.
	assert.Equal(t, "grpc", protocontract.FormatString(protocontract.Format))
	assert.Equal(t, "connect", protocontract.FormatString(protocontract.FormatConnect))
	assert.Equal(t, "openapi", protocontract.FormatString("openapi"))
	assert.Equal(t, "", protocontract.FormatString(""))
}

func TestCoverParseSlugReadsWordBoundariesFromTheName(t *testing.T) {
	// An identifier is derived from a service name, so the spelling rule is what a
	// human sees in a catalog. Punctuation is a boundary, and a run of it is one
	// boundary: a slug that emitted a dash per character would publish "a--b".
	assert.Equal(t, "graph", protocontract.Slug("cov.parse.v1.GraphService"))
	assert.Equal(t, "graph-apiservice", protocontract.Slug("cov.parse.v1.GraphAPIService"),
		"a word boundary is read from the name's own case, so a run of capitals is one word")
	assert.Equal(t, "http2-gateway", protocontract.Slug("cov.parse.v1.HTTP2GatewayService"),
		"a capital after a digit starts a word")

	// Only the last segment names the service; the package is not part of the
	// identifier, which is what makes two contracts' services comparable.
	assert.Equal(t, "graph", protocontract.Slug("cov.parse.v1.Graph"))

	// A name protobuf would not accept still has to produce an identifier a catalog
	// can store, so a run of punctuation is one separator and leading or trailing
	// separators are trimmed.
	assert.Equal(t, "weird-name", protocontract.Slug("Weird  Name"))
	assert.Equal(t, "api", protocontract.Slug("!!!"),
		"a name with nothing readable in it still yields an identifier")
}
