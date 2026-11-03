package hammer

// ProviderRole is the open identifier of the role a provider plays.
// The documented roles are parser and adapter; a user may add another.
type ProviderRole = string

// Provider roles this library documents. They are conventions, not a closed
// set: a user may add another role together with its own rules.
const (
	// ProviderParser parses a description document into the standard model.
	ProviderParser ProviderRole = "parser"
	// ProviderAdapter renders a standard description into a target
	// representation, such as an OpenAPI document or an MCP tool surface. It is
	// direction-agnostic: its source is the standard description, not the format
	// the description was parsed from.
	ProviderAdapter ProviderRole = "adapter"
	// ProviderInvoker executes the operations of a registered API against a live
	// server.
	ProviderInvoker ProviderRole = "invoker"
)

// Provider describes one package's claim about what it can do: which formats it
// reads, which representations it renders, and which transports it calls over.
//
// A record states one role, so a package that both reads and writes a format
// contributes a record for each. providers builds the records from the claims the
// packages publish, which is why a caller can ask "which formats can this library
// parse?" once instead of importing every provider to find out.
type Provider struct {
	// ID is the stable provider identifier, unique within the index.
	ID string
	// Subsystem names the component hosting the provider, when one does. It is
	// empty for a package that is itself the provider, which is the only case this
	// library has.
	Subsystem string
	// Role states which contract the provider implements, as an open identifier.
	Role ProviderRole
	// Formats are the description formats the provider can parse. It is empty
	// for roles that do not read documents.
	Formats []Format
	// Targets are the representations the provider can render a description
	// into, such as an OpenAPI document, a protobuf contract, or an MCP tool
	// surface. It is empty for roles that do not render.
	Targets []string
	// Transports are the transports the provider can invoke over. It is empty
	// for roles that do not call.
	Transports []Transport
	// Endpoint is where the provider is reachable. It is empty for a package that
	// is called in process, which is what every provider here is.
	Endpoint string
	// ServiceNames are the fully-qualified services the provider serves, for a
	// provider that is served over a protocol. A package that is imported rather
	// than dialled serves none.
	ServiceNames []string
	// Status is the operational state of the provider.
	Status ServerStatus
	// ImplementationVersion is the version the provider's own descriptors state.
	ImplementationVersion string
}

// Clone returns a deep copy of the provider.
func (p Provider) Clone() Provider {
	clone := p
	clone.Formats = append([]Format(nil), p.Formats...)
	clone.Targets = append([]string(nil), p.Targets...)
	clone.Transports = append([]Transport(nil), p.Transports...)
	clone.ServiceNames = append([]string(nil), p.ServiceNames...)
	return clone
}

// HandlesFormat reports whether the provider declares support for a format.
func (p Provider) HandlesFormat(format Format) bool {
	for _, candidate := range p.Formats {
		if candidate == format {
			return true
		}
	}
	return false
}

// HandlesTarget reports whether the provider can render into a representation.
func (p Provider) HandlesTarget(target string) bool {
	for _, candidate := range p.Targets {
		if candidate == target {
			return true
		}
	}
	return false
}

// HandlesTransport reports whether the provider declares support for a
// transport.
func (p Provider) HandlesTransport(transport Transport) bool {
	for _, candidate := range p.Transports {
		if candidate == transport {
			return true
		}
	}
	return false
}

// Serves reports whether the provider advertises a service name.
func (p Provider) Serves(serviceName string) bool {
	for _, candidate := range p.ServiceNames {
		if candidate == serviceName {
			return true
		}
	}
	return false
}

// The contract names a host uses when it publishes these roles as services,
// kept in one place so two hosts do not write the same string twice.
//
// Nothing in this library serves them: a caller imports a package and calls it.
// They are here for the program that wraps these packages in an RPC surface of
// its own, which then has one copy of each name rather than a literal at every
// call site.
const (
	// ParserService names the contract a parser is published under.
	ParserService = "toolbox.api.v1.ApiParserService"
	// AdapterService names the contract an adapter is published under.
	AdapterService = "toolbox.api.v1.ApiAdapterService"
	// InvokerService names the contract an invoker is published under.
	InvokerService = "toolbox.api.v1.ApiInvokerService"
)
