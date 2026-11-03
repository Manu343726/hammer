package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
)

// covNotes is the description both examples use: one service, one operation, one
// required request member, and one declared location, which is the smallest
// description that shows a call being assembled and made.
func covNotes() api.API {
	return api.API{
		ID:   "notes",
		Name: "notes",
		DeclaredServers: []api.DeclaredServer{{
			URL: "https://notes.example.com",
		}},
		Services: []api.Service{{
			Name: "pages",
			Operations: []api.Operation{{
				Name:    "getPage",
				Method:  "GET",
				Path:    "/pages/{id}",
				Summary: "Reads one page.",
				Request: api.ObjectSchema(
					api.Property{Name: "id", Required: true, Schema: api.StringSchema()},
				),
			}},
		}},
	}
}

// A description becomes a command tree, and running the tree hands one call to the
// invoker — which is the whole contract between this package and a transport. The
// invoker prints what it was asked so the example shows the assembled arguments,
// and the body it returns is what the command writes.
func ExampleGenerate() {
	invoker := api.InvokerFunc(func(_ context.Context, call api.Call) (api.Result, error) {
		var arguments struct {
			Body struct {
				ID string `json:"id"`
			} `json:"body"`
		}
		if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
			return api.Result{}, err
		}
		fmt.Printf("called %s with id=%s\n", call.Operation.ID, arguments.Body.ID)
		return api.Result{Body: json.RawMessage(`{"title":"A note","id":"42"}`)}, nil
	})

	root, err := cli.Generate(covNotes(), invoker, cli.Options{})
	if err != nil {
		log.Fatal(err)
	}
	root.SetArgs([]string{"pages", "get-page", "--id", "42"})
	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}

	// Output:
	// called notes/pages/getPage with id=42
	// {
	//   "title": "A note",
	//   "id": "42"
	// }
}

// A tree is also a description: the same command line read back gives the
// operations it offers and the names the description gave them, which is the
// inverse of what Generate wrote.
func ExampleDescribe() {
	root, err := cli.Generate(covNotes(), api.InvokerFunc(
		func(context.Context, api.Call) (api.Result, error) { return api.Result{}, nil },
	), cli.Options{})
	if err != nil {
		log.Fatal(err)
	}

	described, err := cli.Describe(root, cli.DescribeOptions{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(described.ID)
	for _, identifier := range described.OperationIDs() {
		fmt.Println(identifier)
	}

	// Output:
	// notes
	// notes/pages/getPage
}
