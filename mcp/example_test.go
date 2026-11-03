package mcp_test

import (
	"encoding/json"
	"fmt"

	api "github.com/Manu343726/hammer"
	mcp "github.com/Manu343726/hammer/mcp"
)

// covExampleShop is the description every example starts from: one service, one
// operation, and no transport of its own, because what a description was parsed
// from is not what publishing it as a tool depends on.
func covExampleShop() api.API {
	return api.API{
		ID:     "shop",
		Name:   "shop",
		Format: "openapi",
		Services: []api.Service{{
			Name: "shop.pets",
			Operations: []api.Operation{{
				Name:    "getPet",
				Summary: "Fetch one pet",
			}},
		}},
	}
}

func ExampleRender() {
	// A description is translated once, by this function, whoever publishes it: a
	// server answering the calls and a manifest listing them then agree on the name.
	rendered, err := mcp.Render(covExampleShop(), mcp.RenderOptions{})
	if err != nil {
		panic(err)
	}
	for _, tool := range rendered.Tools {
		fmt.Println(tool.Name, "-", tool.Description)
	}
	// Output: pets__get_pet - Fetch one pet
}

func ExampleManifest() {
	// The manifest is Render written out, which is what a publisher writes and what a
	// reader with no server running reads back.
	document, err := mcp.Manifest(covExampleShop(), mcp.RenderOptions{Instructions: "The shop tools."})
	if err != nil {
		panic(err)
	}

	var manifest struct {
		ProtocolVersion string `json:"protocolVersion"`
		Server          struct {
			Name string `json:"name"`
		} `json:"server"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(document, &manifest); err != nil {
		panic(err)
	}
	fmt.Println(manifest.ProtocolVersion, manifest.Server.Name, manifest.Tools[0].Name)
	// Output: 2025-06-18 shop pets__get_pet
}

func ExampleDescribeDocument() {
	// A published manifest is read the same way a live endpoint is, so a deployment
	// can register an API earlier or carried in a configuration file.
	document := []byte(`{
		"server": {"name": "shop"},
		"tools": [{
			"name": "pets__get_pet",
			"description": "Fetch one pet",
			"x-toolbox-operation": "shop/pets/getPet"
		}]
	}`)

	described, _, err := mcp.DescribeDocument(document, mcp.DescribeOptions{})
	if err != nil {
		panic(err)
	}
	operation, found := described.Operation("shop/mcp/getPet")
	if !found {
		panic("the manifest's operation was not described")
	}
	fmt.Println(described.ID, operation.Name, operation.Method)
	// Output: shop getPet tools/call
}
