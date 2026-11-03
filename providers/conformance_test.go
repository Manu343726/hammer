package providers_test

import (
	"sort"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/Manu343726/hammer/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is the conformance suite: the property this library exists for, asserted
// against the formats it claims.
//
// It lives beside the registry rather than in a package of its own because it is
// the registry's evidence. A separate conformance package would import the registry
// and then repeat, in assertions, the claims the registry makes — and the two would
// drift exactly the way a hand-written capability list drifts, which is the defect
// the registry exists to prevent. Here the suite and the claims are one package,
// and `go test ./providers` asks the whole question at once.

// TestIdentityPerProvider asserts the strongest form of the property: a
// description read from real input, published, and read back is the description
// that was published.
//
// It is asserted per provider and not across formats, because it is the claim a
// provider makes about its own format. That a protobuf contract cannot carry an
// HTTP path is not a failure of this property; it is a fact about the format, and
// the matrix below is where it is measured.
func TestIdentityPerProvider(t *testing.T) {
	t.Run("openapi", func(t *testing.T) {
		first := readOpenAPI(t, openAPIDocument)
		second := publish(t, first, "openapi")
		assert.Equal(t, first.Format, second.Format, "publishing in the format it was read as does not change the format")
		assert.Equal(t, first.Source.Kind, second.Source.Kind)
		assertSameSurface(t, first, second, "openapi")
	})

	t.Run("protocontract", func(t *testing.T) {
		first := readContract(t)
		second := publish(t, first, "grpc")
		assert.Equal(t, first.Format, second.Format)
		assert.Equal(t, first.Source.Kind, second.Source.Kind)
		assertSameSurface(t, first, second, "grpc")
	})

	t.Run("mcp", func(t *testing.T) {
		first := readManifest(t, manifestDocument)
		second := publish(t, first, "mcp")
		assert.Equal(t, first.Format, second.Format)
		assert.Equal(t, first.Source.Kind, second.Source.Kind)
		assertSameSurface(t, first, second, "mcp")
	})
}

// TestMatrix is the cross-format claim: a description read from each format this
// library reads, published in each format it writes, and read back.
//
// Every cell states its losses. A loss is recorded in two halves, because there are
// two kinds: the finding a provider reports, which the test asserts rather than
// assuming silence, and the facts the target has no spelling for, which are removed
// from both sides before the descriptions are compared. A loss is also checked for
// being a real loss, so a cell that starts carrying a fact fails until its entry is
// corrected. This table is a measurement of the library, not a hope about it.
func TestMatrix(t *testing.T) {
	sources := map[string]func(t *testing.T) api.API{
		"openapi": func(t *testing.T) api.API { return readOpenAPI(t, openAPIFullDocument) },
		"grpc":    func(t *testing.T) api.API { return readContract(t) },
		"mcp":     func(t *testing.T) api.API { return readManifest(t, manifestDocument) },
	}
	for _, square := range matrix() {
		t.Run(square.source+"-to-"+square.target, func(t *testing.T) {
			described := sources[square.source](t)
			back, findings := publishWithFindings(t, described, square.target)

			for _, expected := range square.reports {
				assertFinding(t, findings, expected)
			}
			assertSameAfter(t, described, back, square.target, square.losses)
		})
	}
}

// TestCLIReadsBackWhatItWrites holds the command line to the same property as the
// document formats, in the shape the format has.
//
// The CLI is not a document format: its write side is a cobra tree that a caller
// runs, and its read side is help text. So the round trip is not
// parse(render(parse(x))). It is DescribeHelp(HelpText(Generate(x))) ==
// Describe(Generate(x)) — the page a tree prints describes exactly the tree that
// printed it. That is the pair that matters, because the two readers are the two
// ways a caller learns a command line's surface, and a tree whose help says
// something else about it has two contracts for one tool. The tree is the honest
// reading, since it is built from the description itself; the text is the reading
// that has to agree with it.
func TestCLIReadsBackWhatItWrites(t *testing.T) {
	described := readOpenAPI(t, openAPIFullDocument)
	root, err := cli.Generate(described, discardInvoker(), cli.Options{CommandName: "petstore"})
	require.NoError(t, err, "an API that can be described can be turned into commands")

	fromTree, err := cli.Describe(root, cli.DescribeOptions{})
	require.NoError(t, err)
	text, err := cli.HelpText(root)
	require.NoError(t, err)
	fromText, err := cli.DescribeHelp(text, cli.DescribeOptions{})
	require.NoError(t, err)

	// The tree is read where it is; the text is read from nowhere in particular,
	// which is the only difference the two readings may have.
	fromText.Source = fromTree.Source
	assert.Equal(t, fromTree, fromText,
		"the help a command tree prints describes the same surface as the tree that printed it")

	// The pair agreeing with each other is worth nothing if both are empty, so the
	// operations of the description have to be in there.
	original := described.OperationIDs()
	roundtripped := fromTree.OperationIDs()
	sort.Strings(original)
	sort.Strings(roundtripped)
	assert.Equal(t, original, roundtripped, "every operation of the description is a command of the tree")
}

// TestEveryAdvertisedCapabilityIsReachable asks the registry what this library
// claims and then does it.
//
// A provider that claims a format nobody can feed, or a target that renders
// nothing, is a lie a consumer acts on: a caller that reads "openapi" from the
// index and cannot parse an OpenAPI document has been told the library does
// something it does not. So each claim is exercised with real input, and the
// index is also checked against the packages it was derived from in both
// directions: no claim without a provider, and no provider without an entry.
func TestEveryAdvertisedCapabilityIsReachable(t *testing.T) {
	described := readOpenAPI(t, openAPIDocument)

	for _, provider := range providers.Providers() {
		provider := provider
		for _, format := range provider.Formats {
			format := format
			t.Run("read-"+provider.ID+"-"+string(format), func(t *testing.T) {
				assert.NotEmpty(t, readAs(t, format).ID,
					"provider %s claims to read %q, so a %q document has to describe something", provider.ID, format, format)
			})
		}
		for _, target := range provider.Targets {
			target := target
			t.Run("write-"+provider.ID+"-"+target, func(t *testing.T) {
				assert.NotZero(t, renderTarget(t, described, target),
					"provider %s claims to render %q, so a description has to come out of it", provider.ID, target)
			})
		}
	}

	// The index is derived from the packages, so the two agree in both directions:
	// no claim in the index that no provider made, and no claim a provider made that
	// the index does not carry.
	for _, descriptor := range providers.Formats() {
		assert.True(t, claimedFormat(t, descriptor.ID), "format %q is indexed but no provider claims to read it", descriptor.ID)
	}
	for _, target := range providers.Targets() {
		assert.True(t, claimedTarget(t, target), "target %q is indexed but no provider claims to render it", target)
	}
	for _, descriptor := range providers.Transports() {
		assert.True(t, claimedTransport(t, descriptor.ID), "transport %q is indexed but no provider claims it", descriptor.ID)
	}
	for _, provider := range providers.Providers() {
		for _, format := range provider.Formats {
			assert.True(t, providers.HandlesFormat(format), "provider %s claims %q and the index does not carry it", provider.ID, format)
		}
		for _, target := range provider.Targets {
			assert.True(t, providers.HandlesTarget(target), "provider %s claims %q and the index does not carry it", provider.ID, target)
		}
	}
}

// TestTheIndexIsDeterministic holds the registry to what it says about itself: the
// same answer on every call, in identifier order, with one record per identifier.
//
// It matters because the entries are computed rather than stored: a caller that
// compares two answers, or diffs an index against a previous run, is relying on the
// computation being the same every time, and a map iteration leaking into the
// result would break that silently.
func TestTheIndexIsDeterministic(t *testing.T) {
	assert.Equal(t, providers.Providers(), providers.Providers(), "asking twice answers the same thing")
	assert.Equal(t, providers.Formats(), providers.Formats())
	assert.Equal(t, providers.Targets(), providers.Targets())
	assert.Equal(t, providers.Transports(), providers.Transports())

	formats := providers.Formats()
	assert.True(t, sort.SliceIsSorted(formats, func(i, j int) bool { return formats[i].ID < formats[j].ID }),
		"formats are ordered by identifier: %v", formats)
	targets := providers.Targets()
	assert.True(t, sort.StringsAreSorted(targets), "targets are ordered: %v", targets)
	transports := providers.Transports()
	assert.True(t, sort.SliceIsSorted(transports, func(i, j int) bool { return transports[i].ID < transports[j].ID }),
		"transports are ordered by identifier: %v", transports)

	seen := make(map[string]bool, 16)
	for _, provider := range providers.Providers() {
		assert.False(t, seen[provider.ID], "provider identifier %q is used twice", provider.ID)
		seen[provider.ID] = true
	}
}

// assertFinding requires one reported finding to contain a phrase, because the
// finding has to say which fact was lost and not only that something was.
func assertFinding(t *testing.T, findings []string, phrase string) {
	t.Helper()
	for _, finding := range findings {
		if contains(finding, phrase) {
			return
		}
	}
	t.Errorf("the provider does not report %q; it reported %v", phrase, findings)
}

// assertSameSurface compares two descriptions of one format, which has to restate
// everything that was stated: the only facts excluded are the ones that belong to
// one document rather than to the API, and each is named where it is excluded.
func assertSameSurface(t *testing.T, want, got api.API, target string) {
	t.Helper()
	left, right := canonical(want, target), canonical(got, target)
	if !assert.Equal(t, left, right, "%s", firstDifference(left, right)) {
		t.Logf("want:\n%s\ngot:\n%s", describeDocument(left), describeDocument(right))
	}
}

// assertSameAfter compares two descriptions on the facts the target can carry.
//
// Each loss is applied to both sides, and then each loss is applied on its own
// minus one: a loss that does not change the comparison is a fact the target now
// carries, and a table that still records it is a table that has stopped being
// true.
func assertSameAfter(t *testing.T, want, got api.API, target string, losses []loss) {
	t.Helper()
	compare := func(skip int) (api.API, api.API) {
		left, right := canonical(want, target), canonical(got, target)
		for index, entry := range losses {
			if index == skip {
				continue
			}
			entry.apply(&left)
			entry.apply(&right)
		}
		// A loss that gathers several services into one leaves them in the order it
		// gathered them, and the comparison is not about that order.
		return sorted(left), sorted(right)
	}
	left, right := compare(-1)
	if !assert.Equal(t, left, right, "the descriptions differ beyond the losses this cell records:\n%s", firstDifference(left, right)) {
		t.Logf("want:\n%s\ngot:\n%s", describeDocument(left), describeDocument(right))
	}
	for index, entry := range losses {
		if other, alsoOther := compare(index); !equal(other, alsoOther) {
			continue
		}
		t.Errorf("this cell records a loss that no longer happens, so the table is stale: %s (%s)",
			entry.name, entry.because)
	}
}

// canonical is the description with the facts that belong to one document rather
// than to the API removed, and the rest in an order that does not depend on the
// format.
//
// Three of those facts are provenance: where the description came from, the format
// it was last read as, and the transport that format names. A description published
// in another format was read as that format, from wherever its artifact was; two
// publications of one API differ in all three without the API having changed.
//
// A schema reference is the fourth: it is a pointer into one document's component
// table, and the value it points at is carried separately. Protobuf's own reader
// says the same thing when it renders a pointer as the message it named.
//
// The order of members and of operations is not a fact either: a JSON object states
// no order, and an MCP manifest carries its schemas as JSON maps, which a reader
// sees in the encoder's order.
func canonical(described api.API, target string) api.API {
	clone := described.Clone()
	clone.Source = api.Source{}
	clone.Format = ""
	clone.Transport = ""
	stripReferences(&clone)
	if foldsParameters(target) {
		// A protobuf operation and an MCP tool each take one request value and have
		// nowhere to say where an argument travels, so both writers fold a
		// description's parameters into its request. The fold is a translation, not
		// a loss, and doing it on both sides is what lets the rest be compared.
		foldParameters(&clone)
	}
	resolveRequiredness(&clone)
	return sorted(clone)
}

// foldsParameters reports whether a target carries an operation's parameters as
// members of its request value.
func foldsParameters(target string) bool {
	return target == "grpc" || target == "connect" || target == "mcp"
}
