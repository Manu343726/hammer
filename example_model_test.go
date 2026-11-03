package hammer

import (
	"fmt"
)

func ExampleOperationID() {
	// An operation identifier is built from names only, so a policy written
	// against one survives the description being re-parsed from another format.
	fmt.Println(OperationID("shop", "pets", "getPet"))
	// Output: shop/pets/getPet
}

func ExampleNormalizeIdentifier() {
	// A format identifier is chosen by whoever contributes the format, so a
	// comparison normalizes the padding and casing first.
	fmt.Println(NormalizeIdentifier("  OpenAPI-V3 "))
	// Output: openapi-v3
}

func ExampleAPI_Operations() {
	described := API{
		Services: []Service{
			{
				Name: "pets",
				Operations: []Operation{
					{ID: "shop/pets/getPet"},
					{ID: "shop/pets/listPets"},
				},
			},
			{
				Name:       "orders",
				Operations: []Operation{{ID: "shop/orders/createOrder"}},
			},
		},
	}

	// Operations returns one flat sequence in description order, which is what a
	// listing or an exposure check walks.
	for _, operation := range described.Operations() {
		fmt.Println(operation.ID)
	}
	// Output:
	// shop/pets/getPet
	// shop/pets/listPets
	// shop/orders/createOrder
}

func ExampleAPI_Summarize() {
	described := API{
		ID:      "shop",
		Name:    "shop",
		Version: "1.0",
		Title:   "Pet shop",
		Format:  "openapi",
		Services: []Service{
			{Name: "pets", Operations: []Operation{{ID: "shop/pets/getPet"}}},
			{Name: "orders", Operations: []Operation{{ID: "shop/orders/createOrder"}}},
		},
	}

	summary := described.Summarize()
	fmt.Printf("%s (%s): %d services, %d operations\n",
		summary.ID, summary.Format, summary.Services, summary.Operations)
	// Output: shop (openapi): 2 services, 2 operations
}
