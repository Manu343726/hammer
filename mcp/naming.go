package mcp

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

// This file holds the rule a tool name is derived by.
//
// A name is a promise to whatever already learned it, so the derivation is
// written once and every rendered definition comes from it. It keeps the service's
// last name segment and a readable method name, which is short enough for a model
// to type and specific enough to find the operation again.

// Keep generated names below the 128-character MCP limit even when a third-party
// service uses a very long protobuf name.
const maxToolNameLength = 100

// generatedToolName reduces a service and a method to a tool name.
func generatedToolName(serviceName, methodName string) string {
	parts := strings.Split(serviceName, ".")
	short := parts[len(parts)-1]
	short = strings.TrimSuffix(short, "Service")
	name := snakeCase(short)
	if methodName != "" {
		name += "__" + snakeCase(methodName)
	}
	return capToolName(name)
}

func snakeCase(value string) string {
	var builder strings.Builder
	for i, r := range value {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				builder.WriteByte('_')
			}
			builder.WriteRune(r + ('a' - 'A'))
		} else if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

// capToolName shortens a name the protocol would refuse. The digest keeps two long
// names from collapsing into the same truncated one, because a shortened name that
// reached the wrong operation would be worse than an unreadable one.
func capToolName(name string) string {
	if len(name) <= maxToolNameLength {
		return name
	}
	sum := sha1.Sum([]byte(name))
	suffix := hex.EncodeToString(sum[:])[:8]
	return name[:maxToolNameLength-len(suffix)-1] + "_" + suffix
}
