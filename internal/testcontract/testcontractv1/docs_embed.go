package testcontractv1

import (
	_ "embed"

	shareddocs "github.com/Manu343726/hammer/docs"
)

// The fixture's contract, embedded as source for the same reason a real package embeds its own:
// a generated descriptor carries the structure and none of the prose, and the prose is where
// both the documentation and the @toolbox.side-effects annotations live.
//
// Registering it is what makes the fixture worth having. A test that asserted a description
// carries its declared documentation, or that an annotation reaches a caller over reflection,
// would otherwise be asserting against a comment that only exists in the test's own expectation.
//
// It lives in this package rather than its parent so that importing the contract — which every
// test that describes it already does — is enough to register it. A registration one level up
// would be present or absent depending on an import nobody can see from the test.
//
//go:embed testcontract.proto
var testcontractProtoSource []byte

func init() {
	// Panics on failure: a fixture that cannot compile its own contract is broken at build time,
	// and a test run that continued would report documentation missing as if it were a defect in
	// the reader under test.
	shareddocs.RegisterEmbeddedProtoSource("testcontract.proto", testcontractProtoSource)
}
