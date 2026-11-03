package providers_test

import (
	api "github.com/Manu343726/hammer"
	testcontractv1 "github.com/Manu343726/hammer/internal/testcontract/testcontractv1"
	"github.com/Manu343726/hammer/mcp"
	"github.com/Manu343726/hammer/openapi"
	"github.com/Manu343726/hammer/protocontract"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

// The fixtures the conformance suite reads.
//
// They are documents rather than Go value literals on purpose. A description built
// by hand says whatever the test wanted it to say, and a round trip over one proves
// only that this library agrees with itself about a shape it invented. Each fixture
// here is input a caller could hand this library: an OpenAPI document, a protobuf
// descriptor set compiled from a checked-in contract, and an MCP tool manifest.

// openAPIDocument is written in the shape this library's OpenAPI writer restates
// exactly: one success response per operation whose prose is the operation's own
// summary, and no prose of its own on a tag.
//
// The writer publishes one success response per operation and describes it with the
// operation's summary, so that is the shape a document it wrote has, and a document
// written that way is one it can read back. The richer shape — several responses,
// component pointers, a tag with prose — is what openAPIFullDocument holds, and the
// matrix measures what publishing that costs.
const openAPIDocument = `{
  "openapi": "3.1.0",
  "info": {"title": "Pet Store", "version": "1.4.0", "description": "A sample API."},
  "servers": [{"url": "https://api.example.test/v1"}],
  "tags": [{"name": "pets"}],
  "paths": {
    "/pets": {
      "get": {
        "operationId": "listPets",
        "summary": "Lists every pet",
        "tags": ["pets"],
        "parameters": [
          {"name": "limit", "in": "query", "schema": {"type": "integer", "format": "int32"}}
        ],
        "responses": {
          "200": {
            "description": "Lists every pet",
            "content": {"application/json": {"schema": {
              "type": "object",
              "required": ["name"],
              "properties": {
                "name": {"type": "string"},
                "status": {"type": "string", "enum": ["available", "sold"]}
              }
            }}}
          }
        }
      }
    },
    "/pets/{petId}": {
      "parameters": [
        {"name": "petId", "in": "path", "required": true, "schema": {"type": "string"}}
      ],
      "get": {
        "operationId": "getPet",
        "summary": "Fetches one pet",
        "tags": ["pets"],
        "responses": {
          "200": {
            "description": "Fetches one pet",
            "content": {"application/json": {"schema": {
              "type": "object",
              "properties": {"name": {"type": "string"}}
            }}}
          }
        }
      }
    }
  }
}`

// openAPIFullDocument is the document the matrix reads: it declares what a real
// document declares — two tagged operations, a path and a query parameter, a shared
// component reached by pointer, an enum, a nested object, and two documented
// statuses — so a target that cannot carry a fact has something to lose.
const openAPIFullDocument = `{
  "openapi": "3.1.0",
  "info": {"title": "Pet Store", "version": "1.4.0", "description": "A sample API"},
  "servers": [{"url": "https://api.example.test/v1"}],
  "tags": [{"name": "pets", "description": "Pet operations"}],
  "paths": {
    "/pets/{petId}": {
      "parameters": [{"name": "petId", "in": "path", "required": true, "schema": {"type": "string"}}],
      "get": {
        "operationId": "getPetById",
        "summary": "Fetch one pet",
        "tags": ["pets"],
        "parameters": [{"name": "verbose", "in": "query", "schema": {"type": "boolean"}}],
        "responses": {
          "200": {"description": "The pet", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Pet"}}}},
          "404": {"description": "Not found"}
        }
      }
    },
    "/pets": {
      "post": {
        "operationId": "createPet",
        "summary": "Create a pet",
        "tags": ["pets"],
        "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/NewPet"}}}},
        "responses": {"201": {"description": "Created", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Pet"}}}}}
      }
    }
  },
  "components": {
    "schemas": {
      "Pet": {
        "type": "object",
        "required": ["id", "name"],
        "properties": {
          "id": {"type": "integer", "format": "int64"},
          "name": {"type": "string"},
          "status": {"type": "string", "enum": ["available", "pending", "sold"]},
          "tags": {"type": "array", "items": {"type": "string"}},
          "owner": {"$ref": "#/components/schemas/Owner"}
        }
      },
      "NewPet": {
        "type": "object",
        "required": ["name"],
        "properties": {"name": {"type": "string"}, "tag": {"type": "string"}}
      },
      "Owner": {"type": "object", "properties": {"name": {"type": "string"}}}
    }
  }
}`

// manifestDocument is a tool manifest as a publisher writes one: two tools, an
// argument schema, and the protocol's annotations. It states no server version,
// because a manifest this library writes states none.
const manifestDocument = `{
  "protocolVersion": "2025-06-18",
  "server": {"name": "filesystem", "description": "Reads and writes files on this machine."},
  "tools": [
    {
      "name": "read_file",
      "description": "Reads one file and answers its contents.",
      "inputSchema": {
        "type": "object",
        "properties": {"path": {"type": "string", "description": "Path of the file to read."}},
        "required": ["path"]
      },
      "annotations": {"readOnlyHint": true}
    },
    {
      "name": "write_file",
      "description": "Writes a file, replacing what was there.",
      "inputSchema": {
        "type": "object",
        "properties": {"path": {"type": "string"}, "content": {"type": "string"}},
        "required": ["path", "content"]
      },
      "annotations": {"destructiveHint": true}
    }
  ]
}`

// contractDescriptorSet is the compiled artifact of the checked-in fixture
// contract: what a build produces and what a caller hands a protobuf reader.
func contractDescriptorSet(t testingT) []byte {
	t.Helper()
	encoded, err := proto.Marshal(&descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{
			protodesc.ToFileDescriptorProto(testcontractv1.File_testcontract_proto),
		},
	})
	if err != nil {
		t.Fatalf("compile the fixture contract: %v", err)
	}
	return encoded
}

// testingT is the part of *testing.T these fixtures use, so they can be read from a
// test or a subtest without either one being special.
type testingT interface {
	Helper()
	Fatalf(string, ...any)
}

// readOpenAPI reads an OpenAPI document the way a caller does.
func readOpenAPI(t testingT, document string) api.API {
	t.Helper()
	described, _, err := openapi.Parse([]byte(document), openapi.ParseOptions{})
	if err != nil {
		t.Fatalf("read the OpenAPI document: %v", err)
	}
	return described
}

// readContract reads the fixture contract the way a caller does.
func readContract(t testingT) api.API {
	t.Helper()
	described, _, err := protocontract.NewDescriptor(protocontract.Descriptor{}).FromDescriptorSet(contractDescriptorSet(t))
	if err != nil {
		t.Fatalf("read the fixture contract: %v", err)
	}
	return described
}

// readManifest reads a tool manifest the way a caller does.
func readManifest(t testingT, document string) api.API {
	t.Helper()
	described, _, err := mcp.DescribeDocument([]byte(document), mcp.DescribeOptions{})
	if err != nil {
		t.Fatalf("read the tool manifest: %v", err)
	}
	return described
}
