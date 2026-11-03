package openapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/openapi"
)

// ExampleParse reads a small document into a description. The identifier and the
// operation identifiers are what every later decision refers to, so they are the
// interesting part of the result.
func ExampleParse() {
	document := []byte(`
openapi: "3.1.0"
info: {title: Shop, version: "1.0.0"}
paths:
  /pets:
    get:
      operationId: listPets
      tags: [pets]
      responses:
        "200": {description: ok}
`)

	parsed, warnings, err := openapi.Parse(document, openapi.ParseOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println(parsed.ID)
	fmt.Println(parsed.OperationIDs())
	fmt.Println(len(warnings))

	// Output:
	// shop-1-0-0
	// [shop-1-0-0/pets/listPets]
	// 0
}

// ExampleRender writes the same kind of description back out as an OpenAPI
// document. The renderer reads only the standard model, so a description that
// came from another format renders here without this package knowing where it
// came from.
func ExampleRender() {
	described := api.API{
		ID:      "shop",
		Name:    "shop",
		Title:   "Shop",
		Version: "1.0.0",
		Services: []api.Service{{
			Name: "pets",
			Operations: []api.Operation{{
				Name:    "listPets",
				Method:  "get",
				Path:    "/pets",
				Service: "pets",
			}},
		}},
	}

	rendered, err := openapi.Render(described, openapi.RenderOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println(rendered.Files[0].Name)
	fmt.Println(rendered.MediaType)
	fmt.Println(strings.Contains(string(rendered.Files[0].Content), `"openapi": "3.1.0"`))

	// Output:
	// openapi.json
	// application/json
	// true
}

// ExampleNewInvoker calls a declared operation against a live server. The call
// is built entirely from the operation and the arguments, which is what lets a
// caller invoke an operation it has never seen before.
func ExampleNewInvoker() {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"petId":%q}`, strings.TrimPrefix(r.URL.Path, "/pets/"))
	}))
	defer upstream.Close()

	invoker := openapi.NewInvoker(openapi.InvokerOptions{})
	result, err := invoker.Invoke(context.Background(), api.Call{
		Server: api.Server{BaseURL: upstream.URL},
		Operation: api.Operation{
			Name:   "getPet",
			Method: "get",
			Path:   "/pets/{petId}",
			Parameters: []api.Parameter{{
				Name:     "petId",
				In:       api.ParameterInPath,
				Required: true,
				Schema:   api.StringSchema(),
			}},
		},
		Arguments: []byte(`{"petId":"7"}`),
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Status)
	fmt.Println(string(result.Body))

	// Output:
	// 200
	// {"petId":"7"}
}
