package providers

import (
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests are in the package rather than beside it because they hold the
// registry's own guards, and the guards are reached by a set of contributions this
// library does not have: no package here contributes nothing, claims a format
// another one claims, or states an identifier that is empty. A guard only a future
// package can reach is a guard nobody has ever run, so the set is substituted here
// and the registry is asked the questions the guards answer.

// covContributions replaces the set of packages that make a claim for the duration
// of one test, restoring the real set afterwards: a test that left its own set
// behind would answer for every test that ran after it.
func covContributions(t *testing.T, set ...contribution) {
	t.Helper()
	original := contributions
	contributions = func() []contribution { return set }
	t.Cleanup(func() { contributions = original })
}

// covClaims builds a contribution that claims exactly the formats, targets and
// transports it is given, with one descriptor per claim so a substituted set is
// shaped like a real one.
func covClaims(id string, formats []api.Format, targets []string, transports []api.Transport) contribution {
	return contribution{
		id: id,
		formats: func() []api.Format {
			return append([]api.Format(nil), formats...)
		},
		targets: func() []string {
			return append([]string(nil), targets...)
		},
		transports: func() []api.TransportDescriptor {
			descriptors := make([]api.TransportDescriptor, 0, len(transports))
			for _, transport := range transports {
				descriptors = append(descriptors, api.TransportDescriptor{ID: string(transport)})
			}
			return descriptors
		},
		formatDescriptors: func() []api.FormatDescriptor {
			descriptors := make([]api.FormatDescriptor, 0, len(formats))
			for _, format := range formats {
				descriptors = append(descriptors, api.FormatDescriptor{ID: string(format)})
			}
			return descriptors
		},
	}
}

// TestCovRegistryRecordsNothingForAContributionThatClaimsNothing is the first
// guard: a package that implements nothing is not a provider.
//
// It is not a hypothetical. A package can be added to the set before it grows its
// first format, and a record for it would advertise a provider with no format, no
// target and no transport — a caller that indexed it would offer a capability that
// no code behind it implements.
func TestCovRegistryRecordsNothingForAContributionThatClaimsNothing(t *testing.T) {
	covContributions(t, contribution{id: "not-yet"})

	assert.Empty(t, Providers(), "a package that implements no role contributes no record")
	assert.Empty(t, Formats())
	assert.Empty(t, Targets())
	assert.Empty(t, Transports())
}

// TestCovRegistryDropsAClaimWithNoIdentifier is the second guard: an empty
// identifier is not a capability.
//
// A blank format would be indexed under the empty key and answer "yes" to
// HandlesFormat("") for every caller that passed nothing, which is the one answer a
// caller cannot act on.
func TestCovRegistryDropsAClaimWithNoIdentifier(t *testing.T) {
	covContributions(t, covClaims("blank", []api.Format{"", "real"}, []string{"", "real"}, []api.Transport{"", "real"}))

	formats := Formats()
	require.Len(t, formats, 1, "the blank format is dropped rather than indexed")
	assert.Equal(t, "real", formats[0].ID)

	assert.Equal(t, []string{"real"}, Targets(), "the blank target is dropped rather than indexed")
	transports := Transports()
	require.Len(t, transports, 1)
	assert.Equal(t, "real", transports[0].ID)
}

// TestCovRegistryDropsAnIdentifierTwoPackagesClaim is the third guard: one
// identifier is one entry, whatever number of packages states it.
//
// The registry does not merge the two claims — it keeps the first — because merging
// two descriptors states a media type neither package published. That choice is the
// guard, and a caller that indexed both would see the same format twice with two
// different descriptions and no way to tell which is true.
func TestCovRegistryDropsAnIdentifierTwoPackagesClaim(t *testing.T) {
	first := contribution{
		id:      "first",
		formats: func() []api.Format { return []api.Format{"shared"} },
		targets: func() []string { return []string{"shared"} },
		transports: func() []api.TransportDescriptor {
			return []api.TransportDescriptor{{ID: "shared", Name: "from the first package"}}
		},
		formatDescriptors: func() []api.FormatDescriptor {
			return []api.FormatDescriptor{{ID: "shared", Name: "from the first package", MediaTypes: []string{"application/first"}}}
		},
	}
	// The second claim differs only in case, which is the same identifier: a format
	// named by one package "shared" and by another "Shared" is one format.
	second := contribution{
		id:      "second",
		formats: func() []api.Format { return []api.Format{"Shared"} },
		targets: func() []string { return []string{" Shared "} },
		transports: func() []api.TransportDescriptor {
			return []api.TransportDescriptor{{ID: "SHARED", Name: "from the second package"}}
		},
		formatDescriptors: func() []api.FormatDescriptor {
			return []api.FormatDescriptor{{ID: "Shared", Name: "from the second package", MediaTypes: []string{"application/second"}}}
		},
	}
	covContributions(t, first, second)

	formats := Formats()
	require.Len(t, formats, 1, "a format two packages claim is one entry")
	assert.Equal(t, "from the first package", formats[0].Name, "the first claim is the one kept, not merged with the other")
	assert.Equal(t, []string{"application/first"}, formats[0].MediaTypes)

	assert.Equal(t, []string{"shared"}, Targets(), "a target two packages render is one entry")
	transports := Transports()
	require.Len(t, transports, 1, "a transport two packages reach is one entry")
	assert.Equal(t, "from the first package", transports[0].Name, "the first claim is the one kept, not merged with the other")
}

// TestCovRegistryRecordsOneProviderPerRole holds the shape of a record to what the
// model says: a package that reads, writes and calls contributes three records, and
// a package that does one of them contributes one.
//
// It is asserted here because the records are computed from the set rather than
// written down, so a package that grows a role has to appear with it and a role it
// does not implement has to stay empty rather than borrowed.
func TestCovRegistryRecordsOneProviderPerRole(t *testing.T) {
	covContributions(t,
		covClaims("three", []api.Format{"a"}, []string{"b"}, []api.Transport{"c"}),
		covClaims("one", []api.Format{"d"}, nil, nil),
	)

	records := Providers()
	require.Len(t, records, 4, "three roles from one package, one from the other")

	roles := make(map[string]api.ProviderRole, len(records))
	for _, record := range records {
		roles[record.ID] = record.Role
		assert.Empty(t, record.Endpoint, "a package is called, not dialled: no endpoint is invented")
	}
	assert.Equal(t, api.ProviderParser, roles["three-parser"])
	assert.Equal(t, api.ProviderAdapter, roles["three-adapter"])
	assert.Equal(t, api.ProviderInvoker, roles["three-invoker"])
	assert.Equal(t, api.ProviderParser, roles["one-parser"])
	assert.NotContains(t, roles, "one-adapter", "a role a package does not implement is absent, not empty")
	assert.NotContains(t, roles, "one-invoker")
}

// TestCovRegistryHelpersAnswerForAContributionThatStatesNothing covers the helpers
// the registry is built from: a contribution may state no descriptors at all, and
// asking it must answer "none" rather than fail.
func TestCovRegistryHelpersAnswerForAContributionThatStatesNothing(t *testing.T) {
	bare := contribution{id: "bare"}
	assert.Nil(t, descriptorsOf(bare), "a contribution that states no descriptor states none")

	assert.Empty(t, versionOf(nil), "no descriptor states no version")
	assert.Empty(t, versionOf([]api.FormatDescriptor{{ID: "a"}, {ID: "b"}}), "a descriptor set that states no version states none")
	assert.Equal(t, "2", versionOf([]api.FormatDescriptor{{ID: "a"}, {ID: "b", Version: "2"}}),
		"the first version stated wins, so a package that published two of them is not reported as the later one")

	// Two descriptors of one transport are one transport: a record that claimed the
	// same one twice would read as a library that speaks two protocols where it
	// speaks one.
	assert.Equal(t, []api.Transport{"grpc"}, transportIDs([]api.TransportDescriptor{
		{ID: "grpc"},
		{ID: "GRPC"},
		{ID: ""},
	}))
}

// TestCovHandlesAnswersNoToWhatItCannotDo holds the two questions a caller asks
// before it imports anything. An empty identifier is not a format: answering "yes"
// to it would report a capability for a caller that passed nothing at all.
func TestCovHandlesAnswersNoToWhatItCannotDo(t *testing.T) {
	assert.False(t, HandlesFormat(""), "an empty format is not one this library reads")
	assert.False(t, HandlesFormat("   "), "nor is one that is only space")
	assert.False(t, HandlesFormat("no-such-format"), "a format no provider claims is not read")

	assert.False(t, HandlesTarget(""), "an empty target is not one this library renders")
	assert.False(t, HandlesTarget("no-such-target"))

	// The questions are asked the way a caller would ask them, which includes a
	// spelling that differs from the claim only in case and space.
	assert.True(t, HandlesFormat("OpenAPI"))
	assert.True(t, HandlesFormat(" openapi "))
	assert.True(t, HandlesTarget("MCP"))
	for _, target := range Targets() {
		assert.True(t, HandlesTarget(target), "every target the registry lists is one this library renders")
	}
	for _, descriptor := range Formats() {
		assert.True(t, HandlesFormat(api.Format(descriptor.ID)), "every format the registry lists is one this library reads")
	}
}
