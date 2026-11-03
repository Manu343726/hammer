package docs_test

import (
	"fmt"

	shareddocs "github.com/Manu343726/hammer/docs"
)

// A contract's comments carry two things at once: the prose a reader is shown and the annotations a policy
// reads. Parsing one comment separates them, which is what makes a generated help page free of
// machine-readable lines and a policy able to see them.
func ExampleParseAnnotations() {
	prose, annotations := shareddocs.ParseAnnotations(`Fetch one pet.

@toolbox.side-effects read_only
@toolbox.deprecated`)

	fmt.Println(prose)

	values, declared := annotations.Lookup("toolbox.side-effects")
	fmt.Println(values, declared)
	fmt.Println(annotations.Keys())

	// Output:
	// Fetch one pet.
	// [read_only] true
	// [toolbox.deprecated toolbox.side-effects]
}
