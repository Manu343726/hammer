# hammer

**Need to fit your thing? Just hit it with the hammer.**

hammer models an API once — services, operations, schemas, servers, side effects — and
converts it to and from every format it has to live in:

| Format | Read from | Written as |
| --- | --- | --- |
| `openapi` | an OpenAPI 3.x document, JSON or YAML | an OpenAPI 3.x document, JSON or YAML |
| `grpc` | a `FileDescriptorSet`, or a live endpoint's reflection | proto3 source, and the compiled descriptor set |
| `connect` | a live Connect endpoint's reflection | the same contract; one IDL, two wire protocols |
| `mcp` | a live MCP server's tools, or a published manifest | MCP tool definitions, or a manifest |
| `cli` | a cobra command tree, or an existing binary's `--help` | a cobra command tree |

Every format is readable *and* writable, and reading a rendered document back gives the
description it came from. That is the contract this library exists to keep, and the test
suite holds it: a description is parsed, rendered into each target, parsed again, and
compared.

```go
document, _ := os.ReadFile("petstore.yaml")

described, warnings, _ := openapi.Parse(document, openapi.ParseOptions{})

root, _ := cli.Generate(described,
    openapi.NewInvoker(openapi.InvokerOptions{}),
    cli.Options{CommandName: "petstore"})

os.Exit(cli.Execute(root))
// $ petstore pets get-pet --petId 7
```

No daemon. No code-generation step. No protobuf requirement on the formats that do not
need one. Plain Go packages, one description in the middle.

## Install

```sh
go get github.com/Manu343726/hammer
```

Go 1.27 or later.

## The description

The root package holds the only thing the formats share:

```
API        one addressable API contract
 Service   a group of operations
  Operation one callable operation — the unit of exposure, of permission, of a tool
   Schema    the request and response value shapes
```

An operation has a derived, stable identifier, so a policy, a tool name and a generated
command can all name the same thing without depending on the language the description
was written in. A description carries what a contract states and nothing it does not:
an operation whose document says nothing about consequences is *unknown*, not read-only.

Failures are classified, not phrased. `api.ErrorKind` says what went wrong —
`invalid_argument`, `not_found`, `unavailable` — because a package that knows nothing
about any transport cannot know which status code that is. Whoever exposes the operation
maps the kind to its own codes.

## Packages

| Package | What it is |
| --- | --- |
| `hammer` | the description, the provider index, and the classified errors |
| `openapi` | reads, renders, serves and calls APIs described by OpenAPI documents |
| `protocontract` | reads protobuf contracts from a descriptor set or reflection, renders them back, calls their operations |
| `mcp` | reads MCP servers and manifests, renders tool definitions, invokes tools |
| `cli` | builds a cobra command tree from a description, and reads a tree or a help page back into one |
| `discovery` | a reflection client: what a live endpoint says about itself |
| `docs` | the documentation a contract carries, read from its own `.proto` |
| `providers` | the index of what this library can read and write, and the conformance suite |

## Four things you can do in a few lines

**An OpenAPI document, driven from a terminal** — see the example above.

**A protobuf contract, published as MCP tools**

```go
described, warnings, err := protocontract.NewDescriptor(protocontract.Descriptor{}).
    FromDescriptorSet(descriptorSetBytes)

manifest, err := mcp.Manifest(described, mcp.RenderOptions{})
os.WriteFile("mcp-tools.json", manifest, 0o644)
```

**A live MCP server, published as OpenAPI**

```go
described, warnings, err := mcp.Describe(ctx, "https://example.test/mcp", mcp.DescribeOptions{})

rendered, err := openapi.Render(described, openapi.RenderOptions{})
for _, file := range rendered.Files {
    os.WriteFile(file.Name, file.Content, 0o644)
}
```

**A binary you only have the help page of**

```go
// Reads the tool's own --help output, recursively, into a description.
described, err := cli.DescribeBinary(ctx, "/usr/local/bin/docker", cli.DescribeOptions{})

// …and it can be published anywhere the rest of this README talks about.
manifest, err := mcp.Manifest(described, mcp.RenderOptions{})
```

The same description also comes out of a live endpoint:

```go
described, warnings, err := protocontract.NewDescriptor(protocontract.Descriptor{}).
    FromEndpoint(ctx, "https://localhost:9105")   // gRPC or Connect, over reflection

source, err := protocontract.Render(described, protocontract.RenderOptions{})
```

## Round trips, and what is lost

A conversion that silently drops what it cannot carry is a conversion you cannot trust.
So every renderer reports it:

```go
rendered, err := protocontract.Render(described, protocontract.RenderOptions{})
for _, warning := range rendered.Warnings {
    log.Println("not carried into the contract:", warning)
}
```

A protobuf contract has nowhere to put an HTTP path, a JSON pointer `$ref`, or a
`oneOf` that must not become a required field. Those are named. The things that
round-trip — operation identity, request and response shapes, requiredness where the
target can state it, side effects, streaming — round-trip exactly, and the suite asserts
it against a real contract rather than a fixture written to pass.

## Three rules the code is held to

- **No transport in a format package.** A package reports a classified failure; the
  caller decides what that is over HTTP, gRPC, or a terminal exit code.
- **One translation per target.** There is exactly one function that turns a description
  into MCP tools, and everything that renders tools calls it. Two translations that
  agreed by accident stop agreeing the first time one changes.
- **Open identifiers.** A format and a transport are strings, not enumerations. A format
  this library has never heard of is a value it can still index, describe and resolve.

## Development

```sh
make check     # build, vet, test — what CI runs
make site      # build the project site into public/
```

The suite is held to a coverage floor in CI — **98.8% of the library's statements**
today. The statements that remain are defensive branches whose condition cannot occur: a
descriptor the protobuf runtime has already validated, an error return from a function
that cannot fail, a terminal that has to actually be a terminal. They are listed in the
run summary rather than faked with a test that would only prove a mock was called.

`providers` holds the conformance suite: every format is read from a real document,
rendered into every target, read back, and compared. Each of the twelve cells either
round-trips exactly or declares what it cannot carry — and the loss is asserted to still
*be* a loss, so a cell that quietly starts carrying a fact breaks the table instead of
hiding it.

Every run publishes an Allure report to the project site, and the run summary links to it:

- site: <https://manu343726.github.io/hammer/>
- reports: <https://manu343726.github.io/hammer/allure/latest/>


## License

MIT. See [LICENSE](LICENSE).
