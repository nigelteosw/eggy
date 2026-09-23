package ports

import (
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ContentType string

const (
	ContentTypeImage ContentType = "image"
	// ContentTypeDocument is a file the model reads whole -- a PDF today.
	// It is a distinct kind, not an image with a different media type,
	// because providers publish file and image support separately and a
	// model may take one without the other.
	ContentTypeDocument ContentType = "document"
)

// ContentPart carries non-text input without teaching the kernel which chat
// surface supplied it or which provider wire format will consume it.
type ContentPart struct {
	Type      ContentType `json:"type"`
	MediaType string      `json:"media_type"`
	Data      []byte      `json:"data"`
	// Filename is the name the document arrived with; empty for an image.
	// Providers show it to the model and the durable record names it.
	Filename string `json:"filename,omitempty"`
}

type Message struct {
	Role       Role          `json:"role"`
	Content    string        `json:"content,omitempty"`
	Parts      []ContentPart `json:"parts,omitempty"`
	Name       string        `json:"name,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
	// ProviderReasoning is opaque reasoning state the producing adapter needs
	// back on this message in the next request of the same turn: OpenRouter's
	// reasoning_details, Anthropic's signed thinking blocks, OpenAI's
	// encrypted reasoning items. Reasoning models refuse or forget their own
	// thinking across tool-call rounds without it. The kernel carries it and
	// never inspects it; it is not the visible text in
	// ModelResponse.ReasoningContent.
	ProviderReasoning json.RawMessage `json:"provider_reasoning,omitempty"`
	// ProviderReasoningOrigin names the adapter or provider that wrote
	// ProviderReasoning, so an adapter replays only what it produced. A blob
	// from another origin is dropped, which is exactly what happened before
	// the field existed.
	ProviderReasoningOrigin string `json:"provider_reasoning_origin,omitempty"`
}
