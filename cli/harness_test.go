package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/cli"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// The generator is the only thing that turns a description into a command a person
// types, and every defect it can have is invisible from the code: a flag that
// carries the wrong type only misbehaves once somebody passes one, and a precedence
// rule only shows up when two spellings of the same value disagree. So these tests
// drive the tree the way a caller does — find the command, set flags, run it — and
// assert on the api.Call that reached the invoker rather than on the flags that
// were declared.

// recorder is an invoker that remembers what it was asked to do, so a test can
// assert on the call rather than on the flags that produced it.
type recorder struct {
	calls  []api.Call
	result api.Result
	err    error
}

func (r *recorder) Invoke(_ context.Context, call api.Call) (api.Result, error) {
	r.calls = append(r.calls, call)
	if r.err != nil {
		return api.Result{}, r.err
	}
	return r.result, nil
}

// last is the one call a test expects, failing when the command did not call at all.
func (r *recorder) last(t *testing.T) api.Call {
	t.Helper()
	require.Len(t, r.calls, 1, "the command should have invoked the operation exactly once")
	return r.calls[0]
}

// petstore is a description with every shape the adapter has to turn into a
// command: a path parameter, a request value with required members, a nested
// object, a repeated value, an enumerated value, a read-only member, a streaming
// operation, and a side effect.
func petstore() api.API {
	return api.API{
		ID:          "petstore",
		Name:        "petstore",
		Title:       "Petstore",
		Format:      "openapi",
		Transport:   "http",
		Description: "A small pet store.\nIt exists to be described, not to be called.",
		DeclaredServers: []api.DeclaredServer{{
			URL:         "https://pets.example.com",
			Description: "The live store.",
		}},
		Services: []api.Service{
			{
				Name:        "pets",
				Description: "Operations on pets.",
				Operations: []api.Operation{
					{
						Name:    "getPet",
						Method:  "GET",
						Path:    "/pets/{petId}",
						Summary: "Reads one pet.",
						// A description is wrapped to the width of the document it
						// was written in; the summary must not inherit that wrap.
						Description: "Reads one pet.\nIt is addressed by its identifier, so a caller\nneeds nothing else.",
						Parameters: []api.Parameter{{
							Name:        "petId",
							In:          api.ParameterInPath,
							Required:    true,
							Description: "Identifier of the pet to read.",
							Schema:      api.StringSchema(),
						}},
						Response: api.ObjectSchema(
							api.Property{Name: "id", Schema: api.StringSchema()},
							api.Property{Name: "name", Schema: api.StringSchema()},
						),
					},
					{
						Name:        "createPet",
						Method:      "POST",
						Path:        "/pets",
						Summary:     "Creates a pet.",
						Description: "Creates a pet in the store.",
						SideEffects: []api.SideEffect{api.SideEffectCreate},
						Request: api.ObjectSchema(
							api.Property{
								Name:     "name",
								Required: true,
								Schema: &api.Schema{
									Type:        api.TypeString,
									Description: "What to call the pet.",
								},
							},
							api.Property{
								Name: "tag",
								Schema: &api.Schema{
									Type:        api.TypeArray,
									Items:       api.StringSchema(),
									Description: "Labels the pet carries.",
								},
							},
							api.Property{
								Name: "sizes",
								Schema: &api.Schema{
									Type:  api.TypeArray,
									Items: &api.Schema{Type: api.TypeInteger},
								},
							},
							api.Property{
								Name: "count",
								Schema: &api.Schema{
									Type:        api.TypeInteger,
									Description: "How many to create.",
								},
							},
							api.Property{
								Name: "status",
								Schema: &api.Schema{
									Type:        api.TypeString,
									Enum:        []string{"available", "sold"},
									Description: "Where the pet is in its life.",
								},
							},
							api.Property{
								Name: "prompt",
								Schema: api.ObjectSchema(
									api.Property{
										Name:     "id",
										Required: true,
										Schema: &api.Schema{
											Type:        api.TypeString,
											Description: "Identifier of the introduction.",
										},
									},
									api.Property{
										Name:   "template",
										Schema: api.StringSchema(),
									},
								),
							},
							api.Property{
								Name: "friends",
								Schema: &api.Schema{
									Type: api.TypeArray,
									Items: api.ObjectSchema(
										api.Property{Name: "id", Schema: api.StringSchema()},
									),
								},
							},
							api.Property{
								Name: "createdAt",
								Schema: &api.Schema{
									Type:     api.TypeString,
									ReadOnly: true,
								},
							},
						),
					},
					{
						Name:    "watchPets",
						Method:  "GET",
						Path:    "/pets/events",
						Summary: "Streams pet events.",
						Streaming: api.Streaming{
							Server: true,
						},
					},
					{
						Name:    "listPets",
						Method:  "GET",
						Path:    "/pets",
						Summary: "Lists every pet.",
					},
				},
			},
			{
				Name:        "users",
				Description: "Operations on users.",
				Operations: []api.Operation{{
					Name:   "getUser",
					Method: "GET",
					Path:   "/users/{id}",
					Parameters: []api.Parameter{{
						Name:     "id",
						In:       api.ParameterInPath,
						Required: true,
						Schema:   api.StringSchema(),
					}},
				}},
			},
		},
	}
}

// generate builds a tree over a description, failing the test rather than returning
// an error the caller would have to check at every use.
func generate(t *testing.T, described api.API, invoker api.Invoker, options cli.Options) *cobra.Command {
	t.Helper()
	root, err := cli.Generate(described, invoker, options)
	require.NoError(t, err)
	return root
}

// execute runs one command path with flags and returns what it wrote.
func execute(t *testing.T, root *cobra.Command, args ...string) (string, error) {
	t.Helper()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

// run builds a tree over the petstore description, runs one path, and returns the
// recorded call. It is the shape most of these tests want, and it builds a fresh
// tree each time because a pflag remembers which of its flags were set.
func run(t *testing.T, args ...string) (api.Call, string, error) {
	t.Helper()
	invoker := &recorder{result: api.Result{Body: json.RawMessage(`{"ok":true}`)}}
	root := generate(t, petstore(), invoker, cli.Options{CommandName: "petstore"})
	out, err := execute(t, root, args...)
	if err != nil {
		return api.Call{}, out, err
	}
	return invoker.last(t), out, nil
}

// find walks a command path, failing the test if the path does not exist. A command
// that cannot be reached is the defect under test, so the failure belongs here
// rather than being a nil dereference three assertions later.
func find(t *testing.T, root *cobra.Command, path ...string) *cobra.Command {
	t.Helper()
	current := root
	for _, name := range path {
		next := findChild(current, name)
		require.NotNil(t, next, "no command %q under %q; available: %v", name, current.Name(), commandNames(current))
		current = next
	}
	return current
}

func findChild(parent *cobra.Command, name string) *cobra.Command {
	for _, child := range parent.Commands() {
		if child.Name() == name {
			return child
		}
	}
	return nil
}

func commandNames(command *cobra.Command) []string {
	names := make([]string, 0, len(command.Commands()))
	for _, child := range command.Commands() {
		names = append(names, child.Name())
	}
	return names
}

// arguments decodes the call's arguments, so an assertion reads as the object the
// invoker received rather than as a string comparison.
func arguments(t *testing.T, call api.Call) map[string]any {
	t.Helper()
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(call.Arguments, &decoded), "arguments must be a JSON object: %s", call.Arguments)
	return decoded
}

func flagUsage(command *cobra.Command, name string) string {
	flag := command.Flags().Lookup(name)
	if flag == nil {
		return ""
	}
	return flag.Usage
}
