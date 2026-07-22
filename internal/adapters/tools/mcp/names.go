package mcp

import (
	"errors"
	"strings"
	"unicode"
)

func normalizeToolName(server, remote string) (string, error) {
	normalize := func(value string) string {
		var result strings.Builder
		for _, character := range value {
			if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '_' {
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
