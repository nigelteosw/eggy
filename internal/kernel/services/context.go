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

// Redact masks active secrets and generic credential-shaped substrings in
// content that must be persisted or displayed rather than rejected outright,
// such as implementation-run output that legitimately echoes shell output.
func (g *SecretGuard) Redact(content string) string {
	for _, secret := range g.active {
		content = strings.ReplaceAll(content, secret, "[redacted]")
	}
	for _, pattern := range credentialContentPatterns {
		content = pattern.ReplaceAllString(content, "[redacted]")
	}
	return content
}

const memoryToolDescription = `Curate durable memory across sessions. file "memory" holds reusable knowledge and conventions; file "user" holds stable owner preferences and profile facts.
Actions: "add" appends a new entry (needs text); "replace" rewrites an existing entry (needs old_text and text); "remove" deletes one (needs old_text).
old_text matches an entry by substring and must identify exactly one. Both files are already in your context, so there is no read action.
Store only durable, verified facts. Never store credentials, transient chat, or unsupported assumptions.`

var memoryToolSchema = json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["add","replace","remove"]},"file":{"type":"string","enum":["memory","user"]},"text":{"type":"string","minLength":1},"old_text":{"type":"string","minLength":1}},"required":["action","file"],"additionalProperties":false}`)

type memoryTool struct {
	store ports.ContextStore
	guard *SecretGuard
}

// NewContextTools returns the agent's durable-memory tool surface: one tool
// over the two writable documents. SOUL.md and HEARTBEAT.md are deliberately
// absent — they are owner-editable identity and policy, injected into the
// prompt but never rewritten by the agent.
func NewContextTools(store ports.ContextStore, guard *SecretGuard) []ports.Tool {
	if guard == nil {
		guard = NewSecretGuard(nil)
	}
	return []ports.Tool{memoryTool{store: store, guard: guard}}
}

func (t memoryTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: "memory", Description: memoryToolDescription, Schema: memoryToolSchema}
}

func (t memoryTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Action  string `json:"action"`
		File    string `json:"file"`
		Text    string `json:"text"`
		OldText string `json:"old_text"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	document, err := writableDocument(input.File)
	if err != nil {
		return nil, err
	}
	if input.Action != "remove" {
		if strings.TrimSpace(input.Text) == "" {
			return nil, errors.New("text is required")
		}
		if err := t.guard.Validate("", input.Text); err != nil {
			return nil, err
		}
	}
	if input.Action != "add" && strings.TrimSpace(input.OldText) == "" {
		return nil, errors.New("old_text is required")
	}

	switch input.Action {
	case "add":
		err = t.store.AddEntry(ctx, document, input.Text)
	case "replace":
		err = t.store.ReplaceEntry(ctx, document, input.OldText, input.Text)
	case "remove":
		err = t.store.RemoveEntry(ctx, document, input.OldText)
	default:
		return nil, errors.New("action must be add, replace, or remove")
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(`{"updated":true}`), nil
}

func writableDocument(file string) (ports.ContextDocument, error) {
	switch file {
	case "memory":
		return ports.ContextMemory, nil
	case "user":
		return ports.ContextUser, nil
	default:
		return "", errors.New("file must be memory or user")
	}
}
