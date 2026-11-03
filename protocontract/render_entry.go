package protocontract

import (
	"strings"

	api "github.com/Manu343726/hammer"
)

// File is one file of a rendered target representation.
//
// A target may be described by more than one file, and one of them is marked
// primary: the file a client that understands only one document starts from.
type File struct {
	// Name is the file's name, such as contract.proto.
	Name string
	// Content is the file's bytes.
	Content []byte
	// MediaType is the media type of the content.
	MediaType string
	// Primary marks the file a client should start from.
	Primary bool
}

// Rendered is a target representation of a standard description.
type Rendered struct {
	// Files are the target's files, in a defined order.
	Files []File
	// MediaType is the media type of the primary file.
	MediaType string
	// FileExtension is the extension of the primary file.
	FileExtension string
	// Warnings are the non-fatal findings of the translation. A value this
	// package derived rather than read from the description, and every fact the
	// description stated that protobuf cannot, is reported here, so a consumer
	// learns what a rendered contract does not say instead of assuming it says
	// everything.
	Warnings []string
}

// RenderOptions configures a translation.
type RenderOptions struct {
	// MediaType selects which artifact a caller starts from: proto3 source by
	// default, or the compiled FileDescriptorSet when the caller names its media
	// type. A consumer that feeds a parser wants the descriptors, and handing it
	// source marked primary would answer a different question than the one asked.
	MediaType string
	// Options are target-specific switches, as key/value pairs. A switch this
	// package does not know is ignored, because a package cannot know every
	// target's vocabulary.
	//
	// The switches this package reads are:
	//
	//	filename                the primary file's name, contract.proto by default
	//	package                 declare every service in this protobuf package, and
	//	                        join the service names a description carries into it
	//	go-package              declare option go_package, for a contract that feeds
	//	                        a code generator
	//	include-descriptor-set  "false" omits the compiled FileDescriptorSet
	Options map[string]string
}

// Render writes a protobuf service contract for a standard description.
//
// It reads only the standard description. Which format that description was
// parsed from is not this function's concern, and that is the point: an OpenAPI
// document parsed into the standard model and published as a protobuf contract
// goes through exactly this code path, with no branch on the origin format.
//
// The result is proto3 source plus the FileDescriptorSet compiled from it. The
// descriptor set is not a convenience: it is what a caller feeds back to
// [Descriptor.FromDescriptorSet], and it is compiled from the source by this
// function rather than by a build the caller has to run. A renderer whose own
// output does not compile is a renderer that silently publishes an unusable
// artifact, so the compile is part of rendering and its failure is this
// function's error.
func Render(described api.API, options RenderOptions) (Rendered, error) {
	settings := newRenderOptions(options)
	files, warnings, err := renderContract(described, settings)
	if err != nil {
		return Rendered{}, err
	}
	rendered := Rendered{Files: files, Warnings: warnings, FileExtension: FileExtensionContract}
	// One file is the one a client starts from, even when a contract spans several
	// packages: marking all of them primary would leave a consumer that reads a
	// single file with no way to choose.
	for index := range rendered.Files {
		if rendered.Files[index].MediaType != settings.primary {
			continue
		}
		rendered.Files[index].Primary = true
		rendered.MediaType = rendered.Files[index].MediaType
		if rendered.MediaType == MediaTypeDescriptorSet {
			rendered.FileExtension = FileExtensionDescriptorSet
		}
		break
	}
	return rendered, nil
}

// wantsDescriptorSet reports whether a caller asked for the compiled form of the
// contract as the artifact to start from.
func wantsDescriptorSet(mediaType string) bool {
	switch api.NormalizeIdentifier(mediaType) {
	case string(MediaTypeDescriptorSet), "application/proto", "protoset", "descriptor-set", "descriptor_set", "pb":
		return true
	default:
		return false
	}
}

// RenderDescriptorSet returns the compiled form of a rendered contract, which is
// what a parser and a code generator consume.
//
// It is the same compile [Render] performs, exposed for a caller that wants the
// descriptors and not the source: deriving one from the other here is what keeps
// the two from disagreeing.
func RenderDescriptorSet(described api.API, options RenderOptions) ([]byte, error) {
	settings := RenderOptions{MediaType: options.MediaType, Options: make(map[string]string, len(options.Options)+1)}
	for key, value := range options.Options {
		settings.Options[key] = value
	}
	// A caller that asked for the descriptors asked for them even if it also said
	// not to publish them, so the switch is set rather than reported as a conflict.
	settings.Options["include-descriptor-set"] = "true"

	rendered, err := Render(described, settings)
	if err != nil {
		return nil, err
	}
	for _, file := range rendered.Files {
		if file.MediaType == MediaTypeDescriptorSet {
			return file.Content, nil
		}
	}
	return nil, api.Errorf(api.KindInternal, "the rendered contract carries no descriptor set")
}

// newRenderOptions reads the target switches a caller supplied.
func newRenderOptions(options RenderOptions) renderOptions {
	values := options.Options
	resolved := renderOptions{
		filename:          strings.TrimSpace(values["filename"]),
		packageName:       strings.TrimSpace(values["package"]),
		goPackage:         strings.TrimSpace(values["go-package"]),
		includeDescriptor: !isFalse(values["include-descriptor-set"]),
		primary:           MediaTypeContract,
	}
	if wantsDescriptorSet(options.MediaType) {
		// A caller that asked for the compiled form asked for the file a client
		// starts from, so the contract becomes the secondary artifact instead of the
		// requested one being absent.
		resolved.primary = MediaTypeDescriptorSet
		resolved.includeDescriptor = true
	}
	if resolved.filename == "" {
		resolved.filename = defaultContractFile
	}
	if !strings.HasSuffix(resolved.filename, "."+FileExtensionContract) {
		resolved.filename += "." + FileExtensionContract
	}
	return resolved
}

// isFalse reports whether a switch is present and off. An absent switch keeps the
// default, because a caller that said nothing asked for the default.
func isFalse(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "no", "off":
		return true
	default:
		return false
	}
}
