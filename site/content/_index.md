---
title: "Need to fit your thing?<br>Just hit it with the *hammer*."
lede: >-
  hammer models an API once — services, operations, schemas, servers — and
  converts it to and from every format it has to live in. Read a live MCP
  server, publish it as an OpenAPI document. Read an OpenAPI document, drive it
  from a terminal. Read a protobuf contract, publish the same operations as
  agent tools.
---
## The same description, in every direction

Every format is readable and writable. That is the whole contract: whatever a
description came from, it can be published as anything else it claims, and
reading the result back gives the same description.

| Format | Read from | Written as |
| --- | --- | --- |
| `openapi` | an OpenAPI 3.x document, JSON or YAML | an OpenAPI 3.x document, JSON or YAML |
| `grpc` | a FileDescriptorSet, or a live endpoint's reflection | proto3 source, and the compiled descriptor set |
| `connect` | a live Connect endpoint's reflection | the same contract; one IDL, two wire protocols |
| `mcp` | a live MCP server's tools, or a published manifest | MCP tool definitions, or a manifest |
| `cli` | a cobra command tree, or an existing binary's `--help` | a cobra command tree |

Conversion is honest about what it cannot carry. A protobuf contract has
nowhere to put an HTTP path, so the renderer says so — in a warning the caller
can print, not in a silent drop.

## Four things you can do in a few lines {#examples}

No daemon, no code-generation step, and no protobuf requirement on the formats
that do not need one.

### An OpenAPI document, as a working terminal command

```go
document, err := os.ReadFile("petstore.yaml")
described, warnings, err := openapi.Parse(document, openapi.ParseOptions{})

root, err := cli.Generate(described,
    openapi.NewInvoker(openapi.InvokerOptions{}),
    cli.Options{CommandName: "petstore"})

os.Exit(cli.Execute(root))
// petstore pets get-pet --petId 7
```

### A protobuf contract, published as MCP tools

```go
described, warnings, err := protocontract.NewDescriptor(protocontract.Descriptor{}).
    FromDescriptorSet(descriptorSetBytes)

manifest, err := mcp.Manifest(described, mcp.RenderOptions{})
os.WriteFile("mcp-tools.json", manifest, 0o644)
```

### A live MCP server, published as OpenAPI

```go
described, warnings, err := mcp.Describe(ctx, "https://example.test/mcp", mcp.DescribeOptions{})

rendered, err := openapi.Render(described, openapi.RenderOptions{})
for _, file := range rendered.Files {
    os.WriteFile(file.Name, file.Content, 0o644)
}
```

### A binary you only have the help page of

```go
// Reads the tool's own --help output, recursively, into a description.
described, err := cli.DescribeBinary(ctx, "/usr/local/bin/docker", cli.DescribeOptions{})

// …and from there it can be published anywhere the rest of this page talks about.
manifest, err := mcp.Manifest(described, mcp.RenderOptions{})
```

## What it is made of {#design}

Each format is a package with no opinion about how it will be used. The
description is the only thing they share, so a new format is a new package and
nothing else changes.

| Package | What it holds |
| --- | --- |
| `hammer` | The description itself: APIs, services, operations, schemas, servers, side effects, and a classified error kind instead of a transport's status code. |
| `openapi` | Reads, renders, serves and calls HTTP APIs described by OpenAPI documents. |
| `protocontract` | Reads protobuf contracts from a descriptor set or reflection, renders them back, and calls their operations. |
| `mcp` | Reads MCP servers and manifests into the description, renders tool definitions out of it, and invokes tools. |
| `cli` | Builds a cobra command tree from a description, and reads a command tree or a help page back into one. |
| `discovery` · `docs` | Reflection and documentation support: what a live endpoint says about itself, and the prose a contract carries. |

### Three rules the code is held to

- **No transport in a format package.** A package says what went wrong as a
  classified error; whoever exposes it maps it to a status code.
- **One translation per target.** There is exactly one function that turns a
  description into MCP tools, and everything that renders them calls it.
- **Open identifiers.** A format or a transport is a string, not an enum, so a
  format this library has never heard of is a value it can still index and
  resolve.

## Testing

The suite is held to a coverage floor in CI, and every run publishes a report.

- [The Allure report for the latest run](
  {{< relurl "allure/latest/" >}})
- [Every report this site keeps]({{< relurl "allure/" >}})
- [The Go documentation]({{< param "godoc" >}})
