package services

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

var (
	credentialSectionPattern  = regexp.MustCompile(`(?i)(credential|password|secret|api[ _-]?key|token|private[ _-]?key)`)
	credentialContentPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)github_pat_[A-Za-z0-9_]+`),
		regexp.MustCompile(`(?i)\bgh[pousr]_[A-Za-z0-9_]+`),
		regexp.MustCompile(`(?i)\bbearer\s+\S+`),
		regexp.MustCompile(`(?i)\b(password|api[ _-]?key|token|secret)\s*[:=]\s*\S+`),
		regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	}
)

type SecretGuard struct{ active []string }

func NewSecretGuard(activeSecrets []string) *SecretGuard {
	active := make([]string, 0, len(activeSecrets))
	for _, secret := range activeSecrets {
		if strings.TrimSpace(secret) != "" {
			active = append(active, secret)
		}
	}
	return &SecretGuard{active: active}
}

func (g *SecretGuard) Validate(section, content string) error {
	if credentialSectionPattern.MatchString(section) {
		return errors.New("context write rejected: section may contain a secret")
	}
	for _, pattern := range credentialContentPatterns {
		if pattern.MatchString(content) {
			return errors.New("context write rejected: content may contain a secret")
		}
	}
	for _, secret := range g.active {
		if strings.Contains(content, secret) {
			return errors.New("context write rejected: content contains an active secret")
		}
	}
	return nil
}

type contextEditTool struct {
	name        string
	description string
	document    ports.ContextDocument
	replace     bool
	store       ports.ContextStore
	guard       *SecretGuard
}

func NewContextTools(store ports.ContextStore, guard *SecretGuard) []ports.Tool {
	if guard == nil {
		guard = NewSecretGuard(nil)
	}
	return []ports.Tool{
		contextEditTool{name: "user_append", description: "Autonomously append a stable user preference or profile fact; never store credentials or transient claims", document: ports.ContextUser, store: store, guard: guard},
		contextEditTool{name: "user_replace_section", description: "Replace one user profile section with current stable facts; never store credentials", document: ports.ContextUser, replace: true, store: store, guard: guard},
		contextRemoveTool{name: "user_remove_section", description: "Remove one user profile section entirely because it is stale, superseded, or no longer useful", document: ports.ContextUser, store: store},
		contextReadTool{name: "user_read", description: "Read the current USER.md, including any edits made earlier in this turn, before deciding to append, replace, or remove a section", document: ports.ContextUser, store: store},
		contextEditTool{name: "memory_append", description: "Autonomously append durable reusable knowledge; never store credentials, unsupported assumptions, or transient chat", document: ports.ContextMemory, store: store, guard: guard},
		contextEditTool{name: "memory_replace_section", description: "Replace one durable memory section with verified reusable knowledge; never store credentials", document: ports.ContextMemory, replace: true, store: store, guard: guard},
		contextRemoveTool{name: "memory_remove_section", description: "Remove one durable memory section entirely because it is stale, superseded, or no longer useful", document: ports.ContextMemory, store: store},
		contextReadTool{name: "memory_read", description: "Read the current MEMORY.md, including any edits made earlier in this turn, before deciding to append, replace, or remove a section", document: ports.ContextMemory, store: store},
	}
}

func (t contextEditTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: t.name, Description: t.description, Schema: json.RawMessage(`{"type":"object","properties":{"section":{"type":"string","minLength":1},"content":{"type":"string","minLength":1}},"required":["section","content"],"additionalProperties":false}`)}
}

func (t contextEditTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Section string `json:"section"`
		Content string `json:"content"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	if input.Section == "" || input.Content == "" {
		return nil, errors.New("section and content are required")
	}
	if err := t.guard.Validate(input.Section, input.Content); err != nil {
		return nil, err
	}
	var err error
	if t.replace {
		err = t.store.ReplaceSection(ctx, t.document, input.Section, input.Content)
	} else {
		err = t.store.Append(ctx, t.document, input.Section, input.Content)
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(`{"updated":true}`), nil
}

type contextRemoveTool struct {
	name        string
	description string
	document    ports.ContextDocument
	store       ports.ContextStore
}

func (t contextRemoveTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: t.name, Description: t.description, Schema: json.RawMessage(`{"type":"object","properties":{"section":{"type":"string","minLength":1}},"required":["section"],"additionalProperties":false}`)}
}

func (t contextRemoveTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Section string `json:"section"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	if input.Section == "" {
		return nil, errors.New("section is required")
	}
	if err := t.store.RemoveSection(ctx, t.document, input.Section); err != nil {
		return nil, err
	}
	return json.RawMessage(`{"removed":true}`), nil
}

type contextReadTool struct {
	name        string
	description string
	document    ports.ContextDocument
	store       ports.ContextStore
}

func (t contextReadTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: t.name, Description: t.description, Schema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}
}

func (t contextReadTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := decodeStrict(raw, &struct{}{}); err != nil {
		return nil, err
	}
	loaded, err := t.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	var content string
	switch t.document {
	case ports.ContextUser:
		content = loaded.User
	case ports.ContextMemory:
		content = loaded.Memory
	default:
		return nil, errors.New("context document is not readable")
	}
	return json.Marshal(struct {
		Content  string `json:"content"`
		Bytes    int    `json:"bytes"`
		MaxBytes int64  `json:"max_bytes,omitempty"`
	}{Content: content, Bytes: len(content), MaxBytes: loaded.MaxBytes})
}
