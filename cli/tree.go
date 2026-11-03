package cli

import (
	"strings"

	api "github.com/Manu343726/hammer"
	"github.com/spf13/cobra"
)

// servicePlan is what one service contributes to the tree: either a namespace
// command holding its operations, or its operations on their own.
//
// It is two shapes rather than one command because a flattened service has to be
// spliced into the root. Hiding the service command does not work: cobra does not
// list the children of a hidden command either, so the operations would disappear
// from help and from shell completion, which is the exact problem being fixed.
type servicePlan struct {
	service api.Service
	// flattened states that the service adds no level, so its operations attach to
	// the root.
	flattened bool
	// command is the service's own command, nil when it is flattened.
	command *cobra.Command
	// operations are the service's operations with the names they were given.
	operations []operationPlan
}

type operationPlan struct {
	operation api.Operation
	name      string
}

// plans names every command of the tree before any of them is built.
//
// The root's command list is one namespace, and it holds both the services that
// keep a level and the operations of a service that does not. They are named
// together for that reason — a service command and a flattened operation are
// siblings, so a name claimed by one must not be handed to the other.
func (b *builder) plans(rootName string) []servicePlan {
	plans := make([]servicePlan, len(b.description.Services))
	ladders := make([][]string, 0, len(b.description.Services))
	owner := make([]int, 0, len(b.description.Services))
	member := make([]int, 0, len(b.description.Services))
	for i, service := range b.description.Services {
		plans[i] = servicePlan{
			service:    service,
			flattened:  redundantService(rootName, service, service.Operations),
			operations: make([]operationPlan, len(service.Operations)),
		}
		for j, operation := range service.Operations {
			plans[i].operations[j].operation = operation
		}
		if !plans[i].flattened {
			ladders = append(ladders, serviceLadder(b.description, service))
			owner = append(owner, i)
			member = append(member, -1)
			continue
		}
		for j, operation := range service.Operations {
			ladders = append(ladders, operationLadder(service, operation))
			owner = append(owner, i)
			member = append(member, j)
		}
	}
	for index, name := range assignCommandNames(ladders) {
		if member[index] < 0 {
			plans[owner[index]].command = serviceCommand(plans[owner[index]].service, name)
			continue
		}
		plans[owner[index]].operations[member[index]].name = name
	}
	for i := range plans {
		if plans[i].flattened {
			continue
		}
		ladders := make([][]string, len(plans[i].operations))
		for j, operation := range plans[i].operations {
			ladders[j] = operationLadder(plans[i].service, operation.operation)
		}
		for j, name := range assignCommandNames(ladders) {
			plans[i].operations[j].name = name
		}
	}
	return plans
}

func serviceCommand(service api.Service, name string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: firstSentence(service.Description),
		Long:  serviceLong(service),
		Args:  cobra.NoArgs,
		RunE:  printHelp,
	}
}

// serviceLong leads with the service's identifier, because that is what a reader
// who found the command by guessing needs to find the contract that defines it.
func serviceLong(service api.Service) string {
	var sections []string
	if identifier := strings.TrimSpace(service.ID); identifier != "" {
		sections = append(sections, identifier)
	}
	if text := unwrap(service.Description); text != "" {
		sections = append(sections, text)
	}
	if len(service.SideEffects) > 0 {
		sections = append(sections, "Declared side effects: "+strings.Join(service.SideEffects, ", ")+".")
	}
	return strings.Join(sections, "\n\n")
}

// operationShort is the one line a parent's command list shows.
//
// It is a sentence rather than a line of the source comment, because a summary cut
// where the author wrapped the text reads as if the description ran out of words.
func operationShort(operation api.Operation) string {
	summary := firstSentence(operation.Summary)
	if summary == "" {
		summary = firstSentence(operation.Description)
	}
	if summary == "" {
		summary = wireLine(operation)
	}
	if operation.Deprecated {
		summary = strings.TrimSpace(summary + " (deprecated)")
	}
	if operation.Streaming.Streaming() {
		summary = strings.TrimSpace(summary + " (streaming)")
	}
	return summary
}

// operationLong states what the operation is and how it is carried, so the help of
// a command stands on its own rather than sending the reader back to the document.
func operationLong(operation api.Operation) string {
	var sections []string
	if identifier := strings.TrimSpace(operation.ID); identifier != "" {
		sections = append(sections, identifier)
	}
	if wire := wireLine(operation); wire != "" {
		sections = append(sections, wire)
	}
	if text := unwrap(operation.Description); text != "" {
		sections = append(sections, text)
	} else if summary := unwrap(operation.Summary); summary != "" {
		sections = append(sections, summary)
	}
	if len(operation.SideEffects) > 0 {
		sections = append(sections, "Declared side effects: "+strings.Join(operation.SideEffects, ", ")+".")
	}
	if operation.Deprecated {
		sections = append(sections, "This operation is deprecated and should not be used for new work.")
	}
	if note := streamingNote(operation.Streaming); note != "" {
		sections = append(sections, note)
	}
	return strings.Join(sections, "\n\n")
}

// wireLine is the method and path, when the description states them.
func wireLine(operation api.Operation) string {
	method := strings.ToUpper(strings.TrimSpace(operation.Method))
	path := strings.TrimSpace(operation.Path)
	switch {
	case method != "" && path != "":
		return method + " " + path
	case method != "":
		return method
	default:
		return path
	}
}

func streamingNote(streaming api.Streaming) string {
	var directions []string
	if streaming.Client {
		directions = append(directions, "requests")
	}
	if streaming.Server {
		directions = append(directions, "responses")
	}
	if len(directions) == 0 {
		return ""
	}
	return "This operation streams " + strings.Join(directions, " and ") +
		", and a command line invokes unary operations only."
}

func printHelp(command *cobra.Command, _ []string) error { return command.Help() }
