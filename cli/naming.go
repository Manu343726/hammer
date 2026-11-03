package cli

import (
	"strconv"
	"strings"

	api "github.com/Manu343726/hammer"
)

// A command name is derived from the name its description gives, so two siblings
// can reduce to the same word. That is not an accident to guard against: a
// protobuf contract that declares v1.ServerReflection and v1alpha.ServerReflection
// — one package serving two versions of one contract — produces server-reflection
// twice, and a document with two tags spelled "Pets" and "pets" produces one word.
//
// Cobra resolves a repeated name to whichever command was added first, so a
// collision does not fail. The second command is then unreachable, silently, from
// help and from shell completion — which is worse than a duplicate that is visible.
// So a name more than one sibling claims is qualified with the least that tells
// them apart: the package segment for a contract that has one, the API's own name
// for a document that does not, and the whole identifier when even that collides.

// assignCommandNames picks one name per sibling from that sibling's ladder.
//
// The ladders are resolved together because whether a short name is usable depends
// on the whole set: naming as it went would make the first claimant's name depend
// on the order the description happened to list its services in, and a caller who
// typed a command yesterday must be able to type it today.
func assignCommandNames(ladders [][]string) []string {
	names := make([]string, len(ladders))
	taken := make(map[string]bool, len(ladders))
	deepest := 0
	for _, ladder := range ladders {
		if len(ladder) > deepest {
			deepest = len(ladder)
		}
	}
	// Each round offers every unnamed entry its next rung, and a rung exactly one
	// entry claims is assigned. An entry that shares a rung with another stays
	// unnamed until the two have differentiated — which is why the counting is
	// done per round rather than once against the whole set.
	for depth := 0; depth < deepest; depth++ {
		claims := make(map[string][]int, len(ladders))
		for i, ladder := range ladders {
			if names[i] != "" {
				continue
			}
			rung := ladder[min(depth, len(ladder)-1)]
			claims[rung] = append(claims[rung], i)
		}
		for rung, indexes := range claims {
			if len(indexes) == 1 && !taken[rung] {
				names[indexes[0]] = rung
				taken[rung] = true
			}
		}
	}
	// The last rung is the whole identifier, which is unique for a description the
	// library accepts. A case-only difference — "Pets" and "pets" kebab to the
	// same word — defeats even that, and a numbered suffix keeps both reachable.
	for i, name := range names {
		if name != "" {
			continue
		}
		base := ladders[i][len(ladders[i])-1]
		for suffix := 2; ; suffix++ {
			candidate := base + "-" + strconv.Itoa(suffix)
			if !taken[candidate] {
				names[i] = candidate
				taken[candidate] = true
				break
			}
		}
	}
	return names
}

// serviceLadder is the rungs a service command can be named from, least qualified
// first.
func serviceLadder(description api.API, service api.Service) []string {
	name := strings.TrimSpace(service.Name)
	if name == "" {
		name = strings.TrimSpace(service.ID)
	}
	short := shortName(name)
	ladder := []string{short}
	if segments := packageSegments(name); len(segments) > 0 {
		ladder = append(ladder, commandWord(segments[len(segments)-1])+"."+short)
	} else if qualifier := apiWord(description); qualifier != "" {
		ladder = append(ladder, qualifier+"."+short)
	}
	if identifier := dottedIdentifier(service.ID); identifier != "" {
		ladder = append(ladder, identifier)
	}
	return distinctWords(ladder, "service")
}

// operationLadder is the rungs an operation command can be named from.
//
// An operation name is unique within its service, so the short rung is normally
// enough; the rest of the ladder exists because two names that differ in the
// description can still reduce to one word, and because a flattened service puts
// its operations beside another service's command.
func operationLadder(service api.Service, operation api.Operation) []string {
	name := strings.TrimSpace(operation.Name)
	if name == "" {
		name = strings.TrimSpace(operation.ID)
	}
	short := commandWord(lastSegment(name))
	ladder := []string{short}
	if qualifier := commandWord(lastSegment(service.Name)); qualifier != "" && qualifier != short {
		ladder = append(ladder, qualifier+"."+short)
	}
	if identifier := dottedIdentifier(operation.ID); identifier != "" {
		ladder = append(ladder, identifier)
	}
	return distinctWords(ladder, "operation")
}

// redundantService reports whether a service should not get a level of its own.
//
// A service command is a namespace, and a namespace that costs the caller an extra
// word per invocation while naming nothing they did not already name is noise. Two
// shapes produce one:
//
//   - the service's own name is the root command's name, so every call would read
//     "pets pets get-pet". The root is already named after it.
//   - an operation has the service's own name, so the operation is a child of a
//     command with the same name. Cobra resolves the bare word to the parent and
//     the operation disappears from help while still working if typed in full.
//
// In both cases the operations attach to the root instead. A caller who wants the
// qualified name still has it in the help text, which states the service it came
// from and the full identifier of every operation.
func redundantService(rootName string, service api.Service, operations []api.Operation) bool {
	short := shortName(service.Name)
	if short == "" {
		return false
	}
	if sameCommandName(rootName, short) {
		return true
	}
	for _, operation := range operations {
		if sameCommandName(short, commandWord(operation.Name)) {
			return true
		}
	}
	return false
}

// shortName is a service's own name without the package it sits in and without the
// "Service" suffix a protobuf contract gives it: a caller types the capability, not
// its container.
func shortName(name string) string {
	value := lastSegment(name)
	if trimmed := strings.TrimSuffix(value, "Service"); trimmed != "" {
		value = trimmed
	}
	return commandWord(value)
}

// commandWord renders a name as the word a caller types: lower case, with a hyphen
// where the name changed case or carried punctuation.
//
// An acronym is not split into its letters — "HTTPServer" is "http-server", not
// "h-t-t-p-server" — because a word boundary inside an acronym is a guess that
// reads as a typo. The hyphen before the last capital is where a reader hears the
// new word start.
func commandWord(value string) string {
	runes := []rune(strings.TrimSpace(value))
	word := make([]rune, 0, len(runes))
	for i, r := range runes {
		switch {
		case r >= 'A' && r <= 'Z':
			if beginsWord(runes, i) && len(word) > 0 && word[len(word)-1] != '-' {
				word = append(word, '-')
			}
			word = append(word, r+('a'-'A'))
		case isWordSeparator(r):
			if len(word) > 0 && word[len(word)-1] != '-' {
				word = append(word, '-')
			}
		default:
			word = append(word, r)
		}
	}
	return strings.Trim(string(word), "-")
}

func beginsWord(runes []rune, i int) bool {
	if i == 0 {
		return false
	}
	previous := runes[i-1]
	switch {
	case previous >= 'a' && previous <= 'z', previous >= '0' && previous <= '9':
		return true
	default:
		return previous >= 'A' && previous <= 'Z' &&
			i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
	}
}

func isWordSeparator(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '_', '-', '/', '.', ':':
		return true
	default:
		return false
	}
}

// sameCommandName compares two names the way a caller reads them, ignoring the
// punctuation and case that separate a service from its own command: "apitools"
// and "api-tools" are the same word to somebody typing a command, and a level of
// nesting between them costs a word on every invocation to say nothing.
func sameCommandName(a, b string) bool {
	return normaliseCommandName(a) == normaliseCommandName(b)
}

func normaliseCommandName(name string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// apiWord is the qualifier a document without package segments gets: the API's own
// name, which is what tells two tags of different APIs apart.
func apiWord(description api.API) string {
	for _, candidate := range []string{description.Name, lastSegment(description.ID)} {
		if word := commandWord(candidate); word != "" {
			return word
		}
	}
	return ""
}

// dottedIdentifier renders an identifier such as "petstore/pets/getPet" as the
// qualified command word "petstore.pets.get-pet". It is the last rung of every
// ladder, because an identifier is the one thing a description guarantees unique.
func dottedIdentifier(identifier string) string {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return ""
	}
	segments := strings.Split(identifier, "/")
	words := make([]string, 0, len(segments))
	for _, segment := range segments {
		if word := commandWord(segment); word != "" {
			words = append(words, word)
		}
	}
	return strings.Join(words, ".")
}

// distinctWords drops the empty and repeated rungs of a ladder, so a rung that says
// what the previous one said does not delay the walk. A ladder left empty would
// make the namer unable to offer anything, so the fallback is used instead.
func distinctWords(ladder []string, fallback string) []string {
	distinct := make([]string, 0, len(ladder))
	for _, word := range ladder {
		if word == "" {
			continue
		}
		seen := false
		for _, existing := range distinct {
			if existing == word {
				seen = true
				break
			}
		}
		if !seen {
			distinct = append(distinct, word)
		}
	}
	if len(distinct) == 0 {
		return []string{fallback}
	}
	return distinct
}

func packageSegments(name string) []string {
	parts := strings.Split(strings.TrimSpace(name), ".")
	if len(parts) < 2 {
		return nil
	}
	return parts[:len(parts)-1]
}

func lastSegment(name string) string {
	parts := strings.Split(strings.TrimSpace(name), ".")
	return parts[len(parts)-1]
}
