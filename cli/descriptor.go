package cli

import (
	api "github.com/Manu343726/hammer"
)

// This file states what this package claims, in the one form a consumer can ask
// for it.
//
// The claim is stated here rather than in the package that indexes providers
// because a claim kept outside the code it describes is a claim that drifts: a
// target renamed here and not there leaves a consumer rendering something this
// package no longer produces. Every other provider in this library answers the
// same questions the same way, and an index that has to special-case one of them
// is the index that gets that one wrong.

// Target is the representation this package renders into.
//
// It is the same identifier as Format on purpose: a command line is both what a
// caller hands over as help text and what this package writes out as a tree, so
// naming the two differently would describe one representation twice.
const Target = "cli"

// Formats returns the format identifiers this package claims.
func Formats() []api.Format { return []api.Format{Format} }

// Targets returns the representations this package renders into.
func Targets() []string { return []string{Target} }

// HandlesFormat reports whether this package reads a format.
func HandlesFormat(format api.Format) bool {
	return api.NormalizeIdentifier(string(format)) == string(Format)
}

// HandlesTarget reports whether this package renders into a target.
func HandlesTarget(target string) bool { return api.NormalizeIdentifier(target) == Target }

// FormatDescriptor describes the format for an index, so the index can answer
// "can this library read a command line?" without importing this package to find
// out.
//
// The specification version is empty because there is no specification: cobra's
// help layout is the only grammar a page is read through, and it is a
// convention this package reads rather than a document it was given.
func FormatDescriptor() api.FormatDescriptor {
	return api.FormatDescriptor{
		ID:          string(Format),
		Name:        "Command line",
		Description: "A command line's own help pages, one page per command, read into a description of the surface it offers.",
		MediaTypes:  []string{"text/plain"},
	}
}

// TargetDescriptor describes the representation this package renders, so an index
// can say what the library is able to publish and not only what it can read.
func TargetDescriptor() api.FormatDescriptor {
	return api.FormatDescriptor{
		ID:          Target,
		Name:        "Command line tree",
		Description: "A cobra command tree: one command per operation and one flag per declared input, driven against whatever invoker the caller supplies.",
		MediaTypes:  []string{"text/plain"},
	}
}

// There is deliberately no TransportDescriptors here, although every other
// provider in this library has one.
//
// A transport descriptor says this package can call a server over something, and
// it cannot: the tree hands each call to an api.Invoker the caller supplied, and
// this package never learns what that invoker speaks. Claiming one would put a
// transport in the index that nothing here reaches, and a consumer choosing an
// invoker on that claim would be choosing at random.
