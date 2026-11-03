package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	api "github.com/Manu343726/hammer"
	"github.com/spf13/cobra"
)

// operationRun is what a generated operation command does when a caller runs it.
//
// Everything it needs is decided when the tree is built: the operation, the inputs
// its flags supply, and where a call goes. Running it assembles one call and hands
// it to the invoker, which is the only thing in this package that knows a
// transport exists.
type operationRun struct {
	description api.API
	server      api.Server
	invoker     api.Invoker
	operation   api.Operation
	inputs      []input
}

func (r *operationRun) run(command *cobra.Command, _ []string) error {
	arguments, err := r.arguments(command)
	if err != nil {
		return err
	}
	server, err := r.target(command)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return api.WrapError(api.KindInternal, err, "encode the arguments of %q", r.operation.ID)
	}
	call := api.Call{
		Server:    server,
		API:       r.description,
		Operation: r.operation,
		Arguments: encoded,
	}
	result, err := r.invoker.Invoke(command.Context(), call)
	if err != nil {
		// The operation is named here because an invoker reports what the transport
		// said, and a caller needs to know which call said it. Wrapping keeps the
		// classification the invoker chose.
		return api.WrapError(api.KindOf(err), err, "call %q", r.operation.ID)
	}
	return printResult(command.OutOrStdout(), result)
}

// arguments assembles the JSON object the invoker receives.
//
// A declared parameter is one key of it, and the request value travels under the
// key the model reserves for it: "body". Nothing is sent for a flag the caller did
// not give, because the transport has no way to express "unset" once a value has
// been filled in, and a zero the caller did not ask for is worse than a value the
// server derives.
func (r *operationRun) arguments(command *cobra.Command) (map[string]any, error) {
	arguments := make(map[string]any, len(r.operation.Parameters)+1)
	members := make(map[string]any)
	membersSet := false
	var body any
	bodySet := false
	// The JSON form of a value is applied before the flags that set one member of
	// it, so a dotted flag wins for the member it names and the rest of the
	// document survives. That is the precedence the flag help promises.
	for _, jsonPass := range []bool{true, false} {
		for _, in := range r.inputs {
			if (in.carrier == carrierJSON) != jsonPass {
				continue
			}
			if !command.Flags().Changed(in.flag) {
				continue
			}
			value, err := in.read(command)
			if err != nil {
				return nil, api.WrapError(api.KindInvalid, err, "flag --%s", in.flag)
			}
			switch {
			case in.parameter != "":
				arguments[in.parameter] = value
			case in.whole && !in.document():
				body, bodySet = value, true
			case in.whole:
				object, ok := value.(map[string]any)
				if !ok {
					return nil, api.Errorf(api.KindInvalid,
						"flag --%s: expected a JSON object, got %s", in.flag, jsonWord(value))
				}
				for name, member := range object {
					members[name] = member
				}
				membersSet = true
			default:
				if err := setMember(members, in.path, value); err != nil {
					return nil, api.WrapError(api.KindInvalid, err, "flag --%s", in.flag)
				}
				membersSet = true
			}
		}
	}
	if err := r.checkRequired(command, members); err != nil {
		return nil, err
	}
	switch {
	case bodySet:
		arguments["body"] = body
	case membersSet:
		// An empty object the caller gave explicitly is still a request value, so
		// it is sent: "no body" and "an empty body" are different requests, and
		// only the caller knows which one they meant.
		arguments["body"] = members
	}
	return arguments, nil
}

// document reports whether the input is the whole request value carried as an
// object, in which case its members are composed into the body rather than the body
// being the value itself.
func (in input) document() bool {
	return in.whole && in.carrier == carrierJSON && shapeOf(in.schema) == shapeObject
}

// checkRequired refuses a call the description says cannot succeed.
//
// The refusal is local and names the flag, because the server's refusal would name
// a wire member the caller never typed. A member of a nested object is required
// only when the object it belongs to was supplied: the description says the object
// is optional, so its members cannot be demanded on their own.
func (r *operationRun) checkRequired(command *cobra.Command, members map[string]any) error {
	for _, in := range r.inputs {
		if !in.required || command.Flags().Changed(in.flag) {
			continue
		}
		if in.whole {
			continue
		}
		if in.parameter != "" {
			return api.Errorf(api.KindInvalid, "--%s is required by %q", in.flag, r.operation.Name)
		}
		if len(in.path) > 1 {
			if _, present := memberAt(members, in.path[:len(in.path)-1]); !present {
				continue
			}
		}
		if _, present := memberAt(members, in.path); !present {
			return api.Errorf(api.KindInvalid, "--%s is required by %q", in.flag, r.operation.Name)
		}
	}
	return nil
}

// setMember writes one value at a member path inside the request body, creating the
// objects on the way.
//
// An object the caller supplied as JSON is navigated rather than replaced, so a
// dotted flag adds to the document instead of throwing away the rest of it.
func setMember(root map[string]any, path []string, value any) error {
	current := root
	for i, key := range path[:len(path)-1] {
		existing, ok := current[key]
		if !ok || existing == nil {
			nested := make(map[string]any)
			current[key] = nested
			current = nested
			continue
		}
		nested, ok := existing.(map[string]any)
		if !ok {
			return fmt.Errorf("%s is not a JSON object, so %s cannot be set inside it",
				strings.Join(path[:i+1], "."), strings.Join(path, "."))
		}
		current = nested
	}
	current[path[len(path)-1]] = value
	return nil
}

// memberAt reads a member path inside the request body.
func memberAt(root map[string]any, path []string) (any, bool) {
	current := root
	for i, key := range path {
		value, ok := current[key]
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			return value, true
		}
		nested, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		current = nested
	}
	return nil, false
}

// target is the server a call goes to, after the root's own --server is applied.
func (r *operationRun) target(command *cobra.Command) (api.Server, error) {
	server := r.server
	flag := command.Flags().Lookup("server")
	if flag != nil && flag.Changed {
		value, err := command.Flags().GetString("server")
		if err != nil {
			return api.Server{}, api.WrapError(api.KindInvalid, err, "read --server")
		}
		server.BaseURL = strings.TrimSpace(value)
	}
	if strings.TrimSpace(server.BaseURL) == "" {
		return api.Server{}, api.Errorf(api.KindInvalid,
			"operation %q has no server to be called on: the description declares none, and --server was not given",
			r.operation.ID)
	}
	return server, nil
}
