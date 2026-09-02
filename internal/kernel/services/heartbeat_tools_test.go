package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type watchStore struct {
	content  string
	document ports.ContextDocument
	err      error
}

func (s *watchStore) Load(context.Context) (ports.AgentContext, error) {
	return ports.AgentContext{Watch: s.content}, nil
}
func (s *watchStore) AddEntry(context.Context, ports.ContextDocument, string) error { return nil }
func (s *watchStore) ReplaceEntry(context.Context, ports.ContextDocument, string, string) error {
	return nil
}
func (s *watchStore) RemoveEntry(context.Context, ports.ContextDocument, string) error { return nil }
func (s *watchStore) ReplaceDocument(_ context.Context, document ports.ContextDocument, content string) error {
	if s.err != nil {
		return s.err
	}
	s.document, s.content = document, content
	return nil
}

// next_check is what makes the beat's own sense of timing reach the clock, so
// a beat that skips it must not quietly succeed: the heartbeat would fall back
// to a fixed interval forever with every test still green.
func TestHeartbeatRespondRequiresANextCheck(t *testing.T) {
	for name, payload := range map[string]string{
		"omitted":        `{"notify":false}`,
		"empty":          `{"notify":false,"next_check":""}`,
		"not a duration": `{"notify":false,"next_check":"soon"}`,
		"zero":           `{"notify":false,"next_check":"0s"}`,
		"negative":       `{"notify":false,"next_check":"-5m"}`,
	} {
		t.Run(name, func(t *testing.T) {
			tool := NewHeartbeatTools(&watchStore{}, NewSecretGuard(nil))[0]
			ctx, response := WithHeartbeatResponse(context.Background())
			if _, err := tool.Execute(ctx, json.RawMessage(payload)); err == nil {
				t.Fatal("a beat with no usable next_check succeeded")
			}
			if response.Responded {
				t.Fatal("a rejected call still recorded a response")
			}
		})
	}
}

// The duration the beat asked for is what reaches the clock, unchanged. The
// bounds live in the daemon, not here, so the tool records judgement rather
// than policy.
func TestHeartbeatRespondCarriesTheRequestedNextCheck(t *testing.T) {
	tool := NewHeartbeatTools(&watchStore{}, NewSecretGuard(nil))[0]
	ctx, response := WithHeartbeatResponse(context.Background())

	if _, err := tool.Execute(ctx, json.RawMessage(`{"notify":false,"next_check":"90m"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if response.NextCheck != 90*time.Minute {
		t.Fatalf("NextCheck=%s want 90m", response.NextCheck)
	}
}

func TestHeartbeatRespondRecordsANotification(t *testing.T) {
	store := &watchStore{}
	tool := NewHeartbeatTools(store, NewSecretGuard(nil))[0]
	ctx, response := WithHeartbeatResponse(context.Background())

	if _, err := tool.Execute(ctx, json.RawMessage(`{"notify":true,"notification_text":"PR #18 has been open three days","next_check":"2h"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !response.Responded || !response.Notify {
		t.Fatalf("response=%+v", response)
	}
	if response.Text != "PR #18 has been open three days" {
		t.Fatalf("Text=%q", response.Text)
	}
}

// The silent path is the whole anti-repetition mechanism: seen, recorded,
// owner not messaged.
func TestHeartbeatRespondStaysSilentAndStillWritesTheWatchList(t *testing.T) {
	store := &watchStore{}
	tool := NewHeartbeatTools(store, NewSecretGuard(nil))[0]
	ctx, response := WithHeartbeatResponse(context.Background())

	raw := json.RawMessage(`{"notify":false,"next_check":"45m","watch":"# Eggy Watch\n\nPR #18 open since Aug 20 — mentioned Aug 22\n"}`)
	if _, err := tool.Execute(ctx, raw); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !response.Responded || response.Notify {
		t.Fatalf("response=%+v", response)
	}
	if store.document != ports.ContextWatch {
		t.Fatalf("document=%q", store.document)
	}
	if !strings.Contains(store.content, "mentioned Aug 22") {
		t.Fatalf("content=%q", store.content)
	}
}

func TestHeartbeatRespondRequiresTextWhenNotifying(t *testing.T) {
	tool := NewHeartbeatTools(&watchStore{}, NewSecretGuard(nil))[0]
	ctx, _ := WithHeartbeatResponse(context.Background())
	if _, err := tool.Execute(ctx, json.RawMessage(`{"notify":true}`)); err == nil {
		t.Fatal("notify with no text succeeded")
	}
}

func TestHeartbeatRespondRejectsASecretInTheWatchList(t *testing.T) {
	tool := NewHeartbeatTools(&watchStore{}, NewSecretGuard([]string{"hunter2"}))[0]
	ctx, _ := WithHeartbeatResponse(context.Background())
	_, err := tool.Execute(ctx, json.RawMessage(`{"notify":false,"next_check":"45m","watch":"token is hunter2"}`))
	if err == nil {
		t.Fatal("secret-bearing watch list accepted")
	}
}

// A rejected watch write must not lose the notification: the finding is what
// the owner actually needs, and the annotation is best-effort.
func TestHeartbeatRespondKeepsTheNotificationWhenTheWatchWriteFails(t *testing.T) {
	store := &watchStore{err: errors.New("WATCH.md is full (7000/6144 bytes)")}
	tool := NewHeartbeatTools(store, NewSecretGuard(nil))[0]
	ctx, response := WithHeartbeatResponse(context.Background())

	if _, err := tool.Execute(ctx, json.RawMessage(`{"notify":true,"notification_text":"deploy failed","next_check":"10m","watch":"too big"}`)); err == nil {
		t.Fatal("expected the watch write error to surface to the model")
	}
	if !response.Responded || !response.Notify || response.Text != "deploy failed" {
		t.Fatalf("response=%+v", response)
	}
}

// Called off a heartbeat turn there is nowhere to put the decision, and
// silently succeeding would let a turn believe it had reported something.
func TestHeartbeatRespondRefusesAnOrdinaryTurn(t *testing.T) {
	tool := NewHeartbeatTools(&watchStore{}, NewSecretGuard(nil))[0]
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"notify":false,"next_check":"1h"}`)); err == nil {
		t.Fatal("heartbeat_respond succeeded off a heartbeat turn")
	}
}

func TestHeartbeatRespondIsInternalClassified(t *testing.T) {
	definition := NewHeartbeatTools(&watchStore{}, NewSecretGuard(nil))[0].Definition()
	if definition.Name != HeartbeatRespondToolName {
		t.Fatalf("name=%q", definition.Name)
	}
	if !definition.Effect.Internal {
		t.Fatalf("effect=%+v want Internal", definition.Effect)
	}
}
