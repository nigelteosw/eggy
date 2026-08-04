package telegram

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

const (
	defaultSelectionTTL = 10 * time.Minute
	maxSelectionPrompt  = 1000
	maxSelectionLabel   = 40
	maxSelectionValue   = 500
)

type SelectOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type pendingSelection struct {
	id        string
	expiresAt time.Time
	options   []SelectOption
}

// Selector owns Telegram's transient, single-question selection state. It is
// adapter-local because inline keyboards and callback data are Telegram
// affordances, not kernel concepts.
type Selector struct {
	client *Client
	now    func() time.Time
	ttl    time.Duration

	mu      sync.Mutex
	pending *pendingSelection
}

func NewSelector(client *Client, now func() time.Time, ttl time.Duration) *Selector {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = defaultSelectionTTL
	}
	return &Selector{client: client, now: now, ttl: ttl}
}

func (s *Selector) Tool() ports.Tool {
	return selectTool{selector: s}
}

// Resolve returns and consumes a selected value. Expired, malformed, stale,
// and duplicate callbacks are all rejected.
func (s *Selector) Resolve(callbackData string) (string, bool) {
	parts := strings.Split(callbackData, ":")
	if len(parts) != 3 || parts[0] != "select" {
		return "", false
	}
	index, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil || s.pending.id != parts[1] || !s.now().Before(s.pending.expiresAt) ||
		index < 0 || index >= len(s.pending.options) {
		if s.pending != nil && !s.now().Before(s.pending.expiresAt) {
			s.pending = nil
		}
		return "", false
	}
	value := s.pending.options[index].Value
	s.pending = nil
	return value, true
}

type selectTool struct {
	selector *Selector
}

func (t selectTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "telegram_select",
		Description: "Ask the owner a question in Telegram with 2 to 8 custom choices. Use only for ordinary choices, never for protected-action approval.",
		// Asking the owner a question changes nothing, and putting an approval
		// prompt in front of a prompt would be a question about a question.
		Effect: ports.ReadOnlyTool(),
		Schema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"prompt":{"type":"string","maxLength":1000},
				"options":{
					"type":"array",
					"minItems":2,
					"maxItems":8,
					"items":{
						"type":"object",
						"properties":{
							"label":{"type":"string","maxLength":40},
							"value":{"type":"string","maxLength":500}
						},
						"required":["label","value"],
						"additionalProperties":false
					}
				}
			},
			"required":["prompt","options"],
			"additionalProperties":false
		}`),
	}
}

func (t selectTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if destination.FromContext(ctx).Kind != destination.Telegram {
		return nil, errors.New("telegram_select is only available in a Telegram conversation")
	}
	var input struct {
		Prompt  string         `json:"prompt"`
		Options []SelectOption `json:"options"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("decode telegram_select input: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, err
	}
	if err := validateSelection(input.Prompt, input.Options); err != nil {
		return nil, err
	}

	id, err := selectionID()
	if err != nil {
		return nil, err
	}
	now := t.selector.now()
	t.selector.mu.Lock()
	if t.selector.pending != nil && now.Before(t.selector.pending.expiresAt) {
		t.selector.mu.Unlock()
		return nil, errors.New("a Telegram selection is already awaiting an answer")
	}
	t.selector.pending = &pendingSelection{
		id:        id,
		expiresAt: now.Add(t.selector.ttl),
		options:   append([]SelectOption(nil), input.Options...),
	}
	t.selector.mu.Unlock()

	if err := t.selector.client.DeliverSelection(ctx, input.Prompt, id, input.Options); err != nil {
		t.selector.mu.Lock()
		if t.selector.pending != nil && t.selector.pending.id == id {
			t.selector.pending = nil
		}
		t.selector.mu.Unlock()
		return nil, err
	}
	return json.RawMessage(`{"status":"awaiting_selection"}`), nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("telegram_select input must contain one JSON object")
		}
		return fmt.Errorf("decode telegram_select input: %w", err)
	}
	return nil
}

func validateSelection(prompt string, options []SelectOption) error {
	if strings.TrimSpace(prompt) == "" || utf8.RuneCountInString(prompt) > maxSelectionPrompt {
		return fmt.Errorf("prompt must contain 1 to %d characters", maxSelectionPrompt)
	}
	if len(options) < 2 || len(options) > 8 {
		return errors.New("options must contain 2 to 8 choices")
	}
	labels := make(map[string]struct{}, len(options))
	values := make(map[string]struct{}, len(options))
	for _, option := range options {
		if strings.TrimSpace(option.Label) == "" || utf8.RuneCountInString(option.Label) > maxSelectionLabel {
			return fmt.Errorf("each label must contain 1 to %d characters", maxSelectionLabel)
		}
		if strings.TrimSpace(option.Value) == "" || utf8.RuneCountInString(option.Value) > maxSelectionValue {
			return fmt.Errorf("each value must contain 1 to %d characters", maxSelectionValue)
		}
		if _, exists := labels[option.Label]; exists {
			return errors.New("option labels must be unique")
		}
		if _, exists := values[option.Value]; exists {
			return errors.New("option values must be unique")
		}
		labels[option.Label] = struct{}{}
		values[option.Value] = struct{}{}
	}
	return nil
}

func selectionID() (string, error) {
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("create selection ID: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
