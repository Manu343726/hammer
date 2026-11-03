package protocontract

import (
	api "github.com/Manu343726/hammer"
)

const (
	// FormatConnect is the format identifier a contract is claimed under when the
	// caller reads it as Connect rather than as gRPC.
	//
	// It is not a second description language. A protobuf contract is one IDL and
	// the two identifiers name the two wire protocols that carry it, which is why
	// this package renders one artifact for both and never a Connect variant of a
	// service: a caller that announced a contract as Connect and then received a
	// different .proto from it would be reading two descriptions of one API.
	FormatConnect api.Format = "connect"

	// MediaTypeContract is the media type of a rendered contract's source.
	MediaTypeContract = "text/x-proto"
	// MediaTypeDescriptorSet is the media type of a compiled descriptor set.
	MediaTypeDescriptorSet = "application/x-protobuf"
	// FileExtensionContract is the extension of a rendered contract's source.
	FileExtensionContract = "proto"
	// FileExtensionDescriptorSet is the extension of a compiled descriptor set.
	FileExtensionDescriptorSet = "pb"

	// defaultContractFile is the name a rendered contract is published under when
	// the caller names none.
	defaultContractFile = "contract.proto"
)

// Formats returns the format identifiers this package reads and writes.
//
// Both are returned because they are two protocols over one contract: an index of
// the formats this library reads has to answer yes to a caller that announced a
// contract as Connect, and refusing it would leave a caller that speaks that
// protocol with no way in.
func Formats() []api.Format { return []api.Format{Format, FormatConnect} }

// Targets returns the representations this package renders into.
//
// The target is the contract, not the protocol: a rendered .proto is the artifact
// for gRPC and for Connect alike, so claiming two different targets would promise
// two artifacts where there is one.
func Targets() []string { return []string{Format, FormatConnect} }

// HandlesFormat reports whether this package reads a format.
func HandlesFormat(format api.Format) bool {
	normalized := api.NormalizeIdentifier(string(format))
	return normalized == string(Format) || normalized == string(FormatConnect)
}

// HandlesTarget reports whether this package renders into a target.
func HandlesTarget(target string) bool {
	normalized := api.NormalizeIdentifier(target)
	return normalized == Format || normalized == FormatConnect
}

// ConnectFormatDescriptor describes the format for an index.
//
// It describes the same contract as FormatDescriptor, which is why it is derived
// from it: two hand-written descriptors drift, and an index recording one as
// protobuf 3 and the other as nothing would be answering the same question twice
// with different answers.
func ConnectFormatDescriptor() api.FormatDescriptor {
	descriptor := FormatDescriptor()
	descriptor.ID = string(FormatConnect)
	descriptor.Name = "Connect"
	descriptor.Description = "The same protobuf service contract as gRPC, served over the Connect protocol. One contract, two protocols: a rendered .proto is the artifact for either."
	descriptor.Metadata = map[string]string{
		"contract-format": string(Format),
		"artifact":        "*." + FileExtensionContract,
	}
	return descriptor
}
