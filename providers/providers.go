// Package providers is the one place that answers what this library can do.
//
// A caller that wants to know whether hammer can read an OpenAPI document, or
// publish a description as a protobuf contract, would otherwise have to import
// every format package and ask each of them in turn. That works until one of them
// grows a format the caller was never told about, or renames a target — and the
// list a caller keeps by hand is wrong from that moment on. A hard-coded list that
// disagrees with the code is the defect this package exists to prevent.
//
// So nothing here is written twice. Every claim below is asked of the package that
// makes it: the formats are openapi.Formats, the targets are mcp.Targets, the
// transports are protocontract.TransportDescriptors. A package that gains a format
// is indexed here without an edit, and a package that stops claiming one stops
// being advertised. There is no list to keep in sync because the packages are the
// list.
//
// It is a static index, not a running registry. Nothing here is reachable over a
// network and nothing is discovered at run time: a caller imports this package and
// calls it, and the answer is the same in every process that links the library.
//
//	formats := providers.Formats()
//	if providers.HandlesFormat("openapi") {
//		described, _, err := openapi.Parse(document, openapi.ParseOptions{})
//		...
//	}
package providers

import (
	"sort"
	"strings"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/Manu343726/hammer/mcp"
	"github.com/Manu343726/hammer/openapi"
	"github.com/Manu343726/hammer/protocontract"
)

// contribution is one package, and the functions that state what it claims.
//
// They are functions rather than values so that this package never holds a copy of
// a claim: a copy is what drifts from the code it was taken from.
type contribution struct {
	// id is what the package is called here, and the prefix of the provider
	// identifiers built from it.
	id string
	// formats and targets are the claims the package advertises. Either may be
	// empty, and a claim the package does not make is never recorded.
	formats    func() []api.Format
	targets    func() []string
	transports func() []api.TransportDescriptor
	// formatDescriptors describe the format claims for a caller that needs more
	// than the identifier: a media type, an extension, a specification version.
	formatDescriptors func() []api.FormatDescriptor
}

// contributions is the set of packages that make a claim, asked every time they
// are needed. Everything the four functions below answer is derived from it, so
// there is exactly one place a package is named and no place a claim is repeated.
func contributions() []contribution {
	return []contribution{
		{
			id:         "openapi",
			formats:    openapi.Formats,
			targets:    openapi.Targets,
			transports: openapi.TransportDescriptors,
			formatDescriptors: func() []api.FormatDescriptor {
				return []api.FormatDescriptor{openapi.FormatDescriptor()}
			},
		},
		{
			id:                "protocontract",
			formats:           protocontract.Formats,
			targets:           protocontract.Targets,
			transports:        protocontract.TransportDescriptors,
			formatDescriptors: protocontract.FormatDescriptors,
		},
		{
			id:         "mcp",
			formats:    mcp.Formats,
			targets:    mcp.Targets,
			transports: mcp.TransportDescriptors,
			formatDescriptors: func() []api.FormatDescriptor {
				return []api.FormatDescriptor{mcp.FormatDescriptor()}
			},
		},
		{
			// This package reaches no server of its own, so it contributes no
			// transport claim: it builds the command that calls, and the caller
			// supplies the invoker that speaks.
			id:      "cli",
			formats: cli.Formats,
			targets: cli.Targets,
			formatDescriptors: func() []api.FormatDescriptor {
				return []api.FormatDescriptor{cli.FormatDescriptor()}
			},
		},
	}
}

// Providers returns one record per contract the library implements.
//
// A record states one role, because the model does: a parser reads documents, an
// adapter renders them, an invoker calls them, and a field belonging to another
// role stays empty rather than borrowed. A package here implements up to three of
// them — openapi reads an OpenAPI document, writes one, and calls what one
// describes — so it contributes a record per contract. A caller that asks "which
// provider parses gRPC" is answered with a record whose Formats are the only thing
// it claims, and never has to guess which half of a mixed record applies.
//
// The fields that describe a reachable service are empty on purpose. There is no
// subsystem hosting these packages, no endpoint serving them and no service name to
// call: a caller imports the package and calls a function. An endpoint recorded
// here would be a string this library invented, and a consumer that dialled it
// would be dialling nothing.
//
// ImplementationVersion is the version the package's own format descriptor states.
// A package that states none reports none, because a number chosen here would be
// the first thing to disagree with the code it claims to describe.
func Providers() []api.Provider {
	result := make([]api.Provider, 0, 11)
	for _, c := range contributions() {
		version := versionOf(descriptorsOf(c))
		if formats := c.formats(); len(formats) > 0 {
			result = append(result, api.Provider{
				ID:                    c.id + "-parser",
				Role:                  api.ProviderParser,
				Formats:               append([]api.Format(nil), formats...),
				ImplementationVersion: version,
			})
		}
		if targets := c.targets(); len(targets) > 0 {
			result = append(result, api.Provider{
				ID:                    c.id + "-adapter",
				Role:                  api.ProviderAdapter,
				Targets:               append([]string(nil), targets...),
				ImplementationVersion: version,
			})
		}
		if c.transports != nil {
			if transports := transportIDs(c.transports()); len(transports) > 0 {
				result = append(result, api.Provider{
					ID:                    c.id + "-invoker",
					Role:                  api.ProviderInvoker,
					Transports:            transports,
					ImplementationVersion: version,
				})
			}
		}
	}
	return result
}

// Formats returns the formats this library can read: the descriptors a caller
// indexes, deduplicated by identifier because one format can be read by one
// provider and described by another, and ordered by identifier so two calls answer
// the same thing in the same order.
func Formats() []api.FormatDescriptor {
	result := make([]api.FormatDescriptor, 0, 8)
	seen := make(map[string]bool, 8)
	for _, c := range contributions() {
		for _, descriptor := range descriptorsOf(c) {
			key := api.NormalizeIdentifier(descriptor.ID)
			if key == "" || seen[key] {
				// A duplicate is dropped rather than merged: the first descriptor
				// is the one the package that reads the format published, and
				// merging two of them would state a media type neither did.
				continue
			}
			seen[key] = true
			result = append(result, descriptor.Clone())
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// Targets returns the representations this library can render into, deduplicated
// by identifier and ordered by it.
func Targets() []string {
	result := make([]string, 0, 8)
	seen := make(map[string]bool, 8)
	for _, c := range contributions() {
		for _, target := range c.targets() {
			key := api.NormalizeIdentifier(target)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, target)
		}
	}
	sort.Strings(result)
	return result
}

// Transports returns the transports this library can reach a server over,
// deduplicated by identifier and ordered by it.
//
// The transport an API is *published* under is not here: this is what the library
// can call, and a format that only writes documents contributes nothing to it.
func Transports() []api.TransportDescriptor {
	result := make([]api.TransportDescriptor, 0, 8)
	seen := make(map[string]bool, 8)
	for _, c := range contributions() {
		if c.transports == nil {
			continue
		}
		for _, descriptor := range c.transports() {
			key := api.NormalizeIdentifier(descriptor.ID)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, descriptor.Clone())
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// HandlesFormat reports whether this library can read a format.
//
// The comparison ignores case and surrounding space because a format identifier is
// chosen by whoever contributes it, and a caller that typed "OpenAPI" is asking
// about the same format a document declared as "openapi".
func HandlesFormat(format api.Format) bool {
	key := api.NormalizeIdentifier(string(format))
	if key == "" {
		return false
	}
	for _, descriptor := range Formats() {
		if api.NormalizeIdentifier(descriptor.ID) == key {
			return true
		}
	}
	return false
}

// HandlesTarget reports whether this library can render into a representation.
func HandlesTarget(target string) bool {
	key := api.NormalizeIdentifier(target)
	if key == "" {
		return false
	}
	for _, candidate := range Targets() {
		if api.NormalizeIdentifier(candidate) == key {
			return true
		}
	}
	return false
}

// descriptorsOf is a contribution's format descriptors, whether it states them as
// one descriptor or as a list.
func descriptorsOf(c contribution) []api.FormatDescriptor {
	if c.formatDescriptors == nil {
		return nil
	}
	return c.formatDescriptors()
}

// versionOf is the version a package's descriptors state, if any of them states
// one. The first non-empty answer wins, because they are descriptors of one
// implementation and a package that published two versions of itself would be
// describing two libraries.
func versionOf(descriptors []api.FormatDescriptor) string {
	for _, descriptor := range descriptors {
		if version := strings.TrimSpace(descriptor.Version); version != "" {
			return version
		}
	}
	return ""
}

// transportIDs is the identifiers of a contribution's transports, deduplicated so a
// record does not claim the same transport twice.
func transportIDs(descriptors []api.TransportDescriptor) []api.Transport {
	result := make([]api.Transport, 0, len(descriptors))
	seen := make(map[string]bool, len(descriptors))
	for _, descriptor := range descriptors {
		key := api.NormalizeIdentifier(descriptor.ID)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, descriptor.ID)
	}
	return result
}
