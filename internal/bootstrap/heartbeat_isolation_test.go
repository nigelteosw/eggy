package bootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sessionjson "github.com/nigelteosw/eggy/internal/adapters/sessions/jsonfile"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

// heartbeatTestHarness wires a real App against a fake HTTP transport, so a
// heartbeat turn exercises the same code path production does: model
// request bodies and delivered Telegram texts are captured for assertions.
type heartbeatTestHarness struct {
	app           *App
	modelBodies   [][]byte
	telegramTexts []string
	mu            sync.Mutex
}

func newHeartbeatTestHarness(t *testing.T, dataDir string, now time.Time, modelReply string) *heartbeatTestHarness {
	t.Helper()
	harness := &heartbeatTestHarness{}
	cfg := appTestConfig(dataDir)
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "deepseek.test" {
			body, _ := io.ReadAll(request.Body)
			harness.mu.Lock()
			harness.modelBodies = append(harness.modelBodies, body)
			harness.mu.Unlock()
			escaped, _ := json.Marshal(modelReply)
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":`+string(escaped)+`}}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`), nil
		}
		if strings.Contains(request.URL.Path, "sendMessage") {
			body, _ := io.ReadAll(request.Body)
			var payload struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(body, &payload)
			harness.mu.Lock()
			harness.telegramTexts = append(harness.telegramTexts, payload.Text)
			harness.mu.Unlock()
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		}
		return appJSON(200, `{"ok":true,"result":true}`), nil
	})}
	app, err := NewApp(cfg, appTestSecrets("provider-secret"), AppOptions{
		HTTPClient: client, TelegramBaseURL: "https://telegram.test",
		ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"},
		Now:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Ready(); err != nil {
		t.Fatal(err)
	}
	harness.app = app
	return harness
}

func (h *heartbeatTestHarness) triggerHeartbeat(t *testing.T) {
	t.Helper()
	if err := h.app.HandleEvent(context.Background(), events.Event{
		ID: "heartbeat-test", Type: events.TypeHeartbeat, Owner: "42", Payload: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
}

func (h *heartbeatTestHarness) lastModelBody(t *testing.T) string {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.modelBodies) == 0 {
		t.Fatal("expected a model request, got none")
	}
	return string(h.modelBodies[len(h.modelBodies)-1])
}

func (h *heartbeatTestHarness) assertNoDurableMessages(t *testing.T) {
	t.Helper()
	messages, err := h.app.memory.PendingEmbeddings(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("durable messages=%#v", messages)
	}
}

// TestHeartbeatIsIsolatedFromRecentConversationHistory proves a heartbeat
// turn never sees state.RecentMessages (so an old chat cannot silently
// revive an instruction), but does see the owner-editable HEARTBEAT.md
// checklist and an explicit isolation marker.
func TestHeartbeatIsIsolatedFromRecentConversationHistory(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "HEARTBEAT.md"), []byte("# Eggy Heartbeat\n\n## Check\n\nOWNER_SPECIFIC_CHECKLIST_MARKER\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	noon := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	harness := newHeartbeatTestHarness(t, dataDir, noon, "All clear.")
	if _, err := harness.app.store.Update(context.Background(), 0, func(state *ports.State) error {
		state.RecentMessages = append(state.RecentMessages, ports.Message{Role: ports.RoleUser, Content: "STALE_OLD_CHAT_INSTRUCTION_MARKER"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	harness.triggerHeartbeat(t)
	body := harness.lastModelBody(t)
	if !strings.Contains(body, "OWNER_SPECIFIC_CHECKLIST_MARKER") {
		t.Fatalf("expected HEARTBEAT.md checklist in request: %s", body)
	}
	if !strings.Contains(body, "isolated turn") {
		t.Fatalf("expected an explicit isolation marker in request: %s", body)
	}
	if strings.Contains(body, "STALE_OLD_CHAT_INSTRUCTION_MARKER") {
		t.Fatalf("recent conversation history leaked into heartbeat turn: %s", body)
	}
	harness.mu.Lock()
	defer harness.mu.Unlock()
	if len(harness.telegramTexts) != 1 || harness.telegramTexts[0] != "All clear." {
		t.Fatalf("telegram=%v", harness.telegramTexts)
	}
	harness.assertNoDurableMessages(t)
}

// TestHeartbeatCurationRunsInQuietHoursButSendIsSuppressed proves silent
// context curation is never gated by quiet hours: the turn still runs (the
// model is still called) during quiet hours, but the check-in is never
// delivered and never counted against the weekly proactive-message limit.
func TestHeartbeatCurationRunsInQuietHoursButSendIsSuppressed(t *testing.T) {
	dataDir := t.TempDir()
	duringQuietHours := time.Date(2026, 7, 19, 23, 0, 0, 0, time.UTC)
	harness := newHeartbeatTestHarness(t, dataDir, duringQuietHours, "Would send this if I could.")
	harness.triggerHeartbeat(t)
	harness.lastModelBody(t) // fails the test if the model was never called
	harness.mu.Lock()
	defer harness.mu.Unlock()
	if len(harness.telegramTexts) != 0 {
		t.Fatalf("expected no check-in delivered during quiet hours, got %v", harness.telegramTexts)
	}
	state, err := harness.app.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.ProactiveMessages) != 0 {
		t.Fatalf("expected quiet-hours heartbeat not to record a proactive message, got %v", state.ProactiveMessages)
	}
}

// TestHeartbeatSentinelSuppressesDelivery proves the deterministic
// HEARTBEAT_OK sentinel (not just an empty string) is treated as "nothing
// useful to report" and never delivered.
func TestHeartbeatSentinelSuppressesDelivery(t *testing.T) {
	dataDir := t.TempDir()
	noon := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	harness := newHeartbeatTestHarness(t, dataDir, noon, services.HeartbeatNoReportSentinel)
	harness.triggerHeartbeat(t)
	harness.lastModelBody(t)
	harness.mu.Lock()
	defer harness.mu.Unlock()
	if len(harness.telegramTexts) != 0 {
		t.Fatalf("expected the sentinel reply to suppress delivery, got %v", harness.telegramTexts)
	}
}

// TestHeartbeatSkipsEntirelyWhileImplementationRunIsActive proves a
// heartbeat tick defers to the next cadence, with no model call at all,
// while an implementation run is actively executing.
func TestHeartbeatSkipsEntirelyWhileImplementationRunIsActive(t *testing.T) {
	dataDir := t.TempDir()
	noon := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	harness := newHeartbeatTestHarness(t, dataDir, noon, "All clear.")
	sessionStore := sessionjson.Open(filepath.Join(dataDir, "sessions"))
	sessions := services.NewImplementationSessions(sessionStore, services.SessionPolicy{}, func() time.Time { return noon })
	if _, err := sessions.Create(context.Background(), ports.ImplementationSession{ID: "active-run"}); err != nil {
		t.Fatal(err)
	}
	harness.triggerHeartbeat(t)
	harness.mu.Lock()
	defer harness.mu.Unlock()
	if len(harness.modelBodies) != 0 {
		t.Fatalf("expected no model call while an implementation run is active, got %d", len(harness.modelBodies))
	}
	if len(harness.telegramTexts) != 0 {
		t.Fatalf("expected no check-in while an implementation run is active, got %v", harness.telegramTexts)
	}
}

// TestScheduledMessageDeliversVerbatimWithoutModelCall proves a
// ScheduleExecutionMessage-kind schedule (a reminder or watchdog
// notification) is delivered exactly, with no model call, as distinct from
// an ordinary agent-turn schedule.
func TestScheduledMessageDeliversVerbatimWithoutModelCall(t *testing.T) {
	dataDir := t.TempDir()
	noon := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	harness := newHeartbeatTestHarness(t, dataDir, noon, "should never be requested")
	payload, _ := json.Marshal(events.Message{ChatID: "42", Text: "Take the bins out"})
	if err := harness.app.HandleEvent(context.Background(), events.Event{
		ID: "scheduled-message-test", Type: events.TypeScheduledMessage, Owner: "42", Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	harness.mu.Lock()
	defer harness.mu.Unlock()
	if len(harness.modelBodies) != 0 {
		t.Fatalf("expected no model call for a deterministic scheduled message, got %d", len(harness.modelBodies))
	}
	if len(harness.telegramTexts) != 1 || harness.telegramTexts[0] != "Take the bins out" {
		t.Fatalf("telegram=%v", harness.telegramTexts)
	}
	harness.assertNoDurableMessages(t)
}

// TestScheduledAgentTurnExcludesRecentConversationHistory proves a
// TypeSchedule agent turn (as distinct from TypeScheduledMessage above) is
// self-contained: it does not see state.RecentMessages either, matching the
// heartbeat isolation guarantee.
func TestScheduledAgentTurnExcludesRecentConversationHistory(t *testing.T) {
	dataDir := t.TempDir()
	noon := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	harness := newHeartbeatTestHarness(t, dataDir, noon, "Checked.")
	if _, err := harness.app.store.Update(context.Background(), 0, func(state *ports.State) error {
		state.RecentMessages = append(state.RecentMessages, ports.Message{Role: ports.RoleUser, Content: "STALE_OLD_CHAT_INSTRUCTION_MARKER"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.Message{ChatID: "42", Text: "Check my calendar for conflicts"})
	if err := harness.app.HandleEvent(context.Background(), events.Event{
		ID: "schedule-test", Type: events.TypeSchedule, Owner: "42", Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	body := harness.lastModelBody(t)
	if strings.Contains(body, "STALE_OLD_CHAT_INSTRUCTION_MARKER") {
		t.Fatalf("recent conversation history leaked into scheduled agent turn: %s", body)
	}
	harness.assertNoDurableMessages(t)
}
