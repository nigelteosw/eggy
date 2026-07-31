package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var ErrResultTooLarge = errors.New("MCP result exceeds configured output limit")

// maxErrorTextBytes bounds how much of a server's error the model is shown.
// An error explains itself in a sentence or two; a server that answers a
// failure with a wall of text should not be able to spend a turn's context on
// it.
const maxErrorTextBytes = 2048

// errorText recovers what the server actually said. An MCP failure arrives as
// an ordinary result with IsError set and the explanation in its text content,
// so discarding that content leaves the model with nothing to act on: it
// cannot tell a bad argument from an expired credential, and its only recourse
// is to call the same tool again. Every retry storm and invented diagnosis in
// this position traces back to throwing this string away.
func errorText(result *sdk.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok && strings.TrimSpace(text.Text) != "" {
			parts = append(parts, strings.TrimSpace(text.Text))
		}
	}
	if len(parts) == 0 {
		if result.StructuredContent != nil {
			if encoded, err := json.Marshal(result.StructuredContent); err == nil {
				parts = append(parts, string(encoded))
			}
		}
		if len(parts) == 0 {
			return "the server reported a failure with no message"
		}
	}
	joined := strings.Join(parts, "; ")
	if len(joined) > maxErrorTextBytes {
		joined = joined[:maxErrorTextBytes] + "… (truncated)"
	}
	return joined
}

type resultEnvelope struct {
	Content           []any `json:"content"`
	StructuredContent any   `json:"structured_content,omitempty"`
}

func convertResult(result *sdk.CallToolResult, maxBytes int64) (json.RawMessage, error) {
	if result == nil {
		return nil, errors.New("MCP server returned an empty result")
	}
	if result.IsError {
		return nil, fmt.Errorf("MCP tool returned an error: %s", errorText(result))
	}
	envelope := resultEnvelope{StructuredContent: result.StructuredContent}
	for _, content := range result.Content {
		switch value := content.(type) {
		case *sdk.TextContent:
			envelope.Content = append(envelope.Content, map[string]any{"type": "text", "text": value.Text})
		case *sdk.ImageContent:
			envelope.Content = append(envelope.Content, map[string]any{"type": "image", "mime_type": value.MIMEType, "size": len(value.Data)})
		case *sdk.AudioContent:
			envelope.Content = append(envelope.Content, map[string]any{"type": "audio", "mime_type": value.MIMEType, "size": len(value.Data)})
		case *sdk.ResourceLink:
			envelope.Content = append(envelope.Content, map[string]any{"type": "resource_link", "uri": value.URI, "name": value.Name, "mime_type": value.MIMEType, "size": value.Size})
		case *sdk.EmbeddedResource:
			metadata := map[string]any{"type": "resource"}
			if value.Resource != nil {
				metadata["uri"] = value.Resource.URI
				metadata["mime_type"] = value.Resource.MIMEType
				metadata["size"] = len(value.Resource.Text) + len(value.Resource.Blob)
			}
			envelope.Content = append(envelope.Content, metadata)
		default:
			envelope.Content = append(envelope.Content, map[string]any{"type": "unsupported"})
		}
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && int64(len(encoded)) > maxBytes {
		return nil, ErrResultTooLarge
	}
	return encoded, nil
}
