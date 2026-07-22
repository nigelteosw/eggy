package mcp

import (
	"errors"
	"strings"
)

func normalizeToolName(server, remote string) (string, error) {
	normalize := func(value string) string {
		var result strings.Builder
		for _, character := range value {
			if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' {
				result.WriteRune(character)
			} else {
				result.WriteByte('_')
			}
		}
		return strings.Trim(result.String(), "_")
	}
	name := normalize(server) + "__" + normalize(remote)
	if name == "__" || strings.HasPrefix(name, "__") || strings.HasSuffix(name, "__") {
		return "", errors.New("MCP tool name is empty after normalization")
	}
	if len(name) > 128 {
		return "", errors.New("MCP tool name exceeds 128 bytes")
	}
	return name, nil
}
