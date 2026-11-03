package hammer

import (
	"context"
	"errors"
	"fmt"
)

// ExampleCall invokes one operation through the function adapter every parser
// package uses, and prints what the caller gets back.
//
// The instructive part is that the arguments are JSON and the server, API and
// operation are values the caller already holds: an invoker never decides which
// operation to call, it is told.
func ExampleCall() {
	invoker := InvokerFunc(func(_ context.Context, call Call) (Result, error) {
		body := fmt.Sprintf(`{"echoed":%s}`, call.Arguments)
		return Result{Status: 200, ContentType: "application/json", Body: []byte(body)}, nil
	})

	result, err := invoker.Invoke(context.Background(), Call{
		Server:    Server{ID: "registry", BaseURL: "http://127.0.0.1:8080"},
		API:       API{ID: "registry"},
		Operation: Operation{ID: "toolbox.registry.v1.RegistryService/Register"},
		Arguments: []byte(`{"name":"hammer"}`),
	})
	if err != nil {
		fmt.Println("invocation failed:", err)
		return
	}
	fmt.Println(result.Status, result.ContentType)
	fmt.Println(string(result.Body))
	// Output:
	// 200 application/json
	// {"echoed":{"name":"hammer"}}
}

// ExampleErrorKind shows the classification a caller reads after a layer it does
// not own has wrapped the failure, which is what a transport maps to a status.
func ExampleErrorKind() {
	err := fmt.Errorf("serving the API: %w", Errorf(KindAlreadyExists, "registry is already served"))

	fmt.Println(KindOf(err))
	fmt.Println(IsKind(err, KindAlreadyExists), IsKind(err, KindDenied))
	// Output:
	// already_exists
	// true false
}

// ExampleErrorf builds a classified failure, and shows that an error nobody
// classified is reported as internal rather than as an empty kind.
func ExampleErrorf() {
	classified := Errorf(KindInvalid, "argument %q is not a declared parameter", "namee")
	fmt.Println(classified.Error())
	fmt.Println(KindOf(classified))

	unclassified := errors.New("something went wrong somewhere")
	fmt.Println(KindOf(unclassified))
	// Output:
	// argument "namee" is not a declared parameter
	// invalid_argument
	// internal
}

// ExampleProvider asks a provider what it claims, which is the question a caller
// that has not imported the package needs answered.
func ExampleProvider() {
	parser := Provider{
		ID:      "protocontract",
		Role:    ProviderParser,
		Formats: []Format{"protobuf", "descriptor_set"},
	}

	fmt.Println(parser.ID, parser.Role)
	fmt.Println(parser.HandlesFormat("protobuf"), parser.HandlesFormat("openapi"))
	// Output:
	// protocontract parser
	// true false
}

// ExampleParsePolicy reads the document form and asks what it permits. A policy
// that says nothing grants nothing, which is why the empty document denies.
func ExampleParsePolicy() {
	policy, err := ParsePolicy("allow knowledge/** read\ndeny registry/**\n")
	if err != nil {
		fmt.Println("the policy document could not be read:", err)
		return
	}

	fmt.Println(policy.Allows(OperationFacts{
		ID:          "knowledge/KnowledgeService/Search",
		SideEffects: []SideEffect{SideEffectReadOnly},
	}))
	fmt.Println(policy.Allows(OperationFacts{ID: "registry/RegistryService/Register"}))

	empty, err := ParsePolicy("")
	if err != nil {
		fmt.Println("the policy document could not be read:", err)
		return
	}
	fmt.Println(empty.Allows(OperationFacts{ID: "knowledge/KnowledgeService/Search"}))
	// Output:
	// true
	// false
	// false
}
