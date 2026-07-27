package mcp

import (
	"encoding/json"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var ErrResultTooLarge = errors.New("MCP result exceeds configured output limit")

type resultEnvelope struct {
	Content           []any `json:"content"`
	StructuredContent any   `json:"structured_content,omitempty"`
}

func convertResult(result *sdk.CallToolResult, maxBytes int64) (json.RawMessage, error) {
	if result == nil {
		return nil, errors.New("MCP server returned an empty result")
	}
	if result.IsError {
		return nil, errors.New("MCP tool returned an error")
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
