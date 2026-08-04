package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// recordingTool stands in for a remote MCP tool. It counts executions, because
// "was this called" is the property every test in this file turns on.
type recordingTool struct {
	name      string
	calls     []string
	result    string
	failWith  error
	schemaRaw string
}

func (t *recordingTool) Definition() ports.ToolDefinition {
	schema := t.schemaRaw
	if schema == "" {
		schema = `{"type":"object"}`
	}
	return ports.ToolDefinition{Name: t.name, Description: "Send a message", Schema: json.RawMessage(schema)}
}

func (t *recordingTool) Execute(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	t.calls = append(t.calls, string(raw))
	if t.failWith != nil {
		return nil, t.failWith
	}
	result := t.result
	if result == "" {
		result = `{"ok":true}`
	}
	return json.RawMessage(result), nil
}

// staticLookup resolves tools by name the way the registry does for the
// executor.
type staticLookup map[string]ports.Tool

func (l staticLookup) Lookup(name string) (ports.Tool, bool) {
	tool, ok := l[name]
	return tool, ok
}

func newGateFixture(t *testing.T) (*ApprovalService, *recordingTool, ports.Tool) {
	t.Helper()
	store := newFakeStateStore()
	service := NewApprovalService(store, func() time.Time { return time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC) }, 30*time.Minute, ports.ModeNormal)
	inner := &recordingTool{name: "mail__send_message"}
	return service, inner, NewApprovalGatedTool(inner, service, service)
}

// The failure this whole feature exists to prevent: a protected tool reaching
// the server on the model's say-so alone.
func TestGatedToolDoesNotCallTheServerWithoutApproval(t *testing.T) {
	service, inner, gated := newGateFixture(t)
	raw, err := gated.Execute(context.Background(), json.RawMessage(`{"to":"someone@example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(inner.calls) != 0 {
		t.Fatalf("gated tool reached the server without an approval: %v", inner.calls)
	}
	var response struct {
		Status     string `json:"status"`
		ApprovalID string `json:"approval_id"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "awaiting_approval" || response.ApprovalID == "" {
		t.Fatalf("gated call did not report an approval: %s", raw)
	}
	pending, err := service.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Action != ApprovalToolCall {
		t.Fatalf("expected one pending tool.call approval, got %+v", pending)
	}
	if !contains(pending[0].Summary, "mail__send_message") || !contains(pending[0].Summary, "someone@example.com") {
		t.Fatalf("summary does not show the owner what they are approving: %q", pending[0].Summary)
	}
}

// The description has to say so, or the model reads "awaiting approval" as a
// transient failure and calls the tool again.
func TestGatedToolDescriptionAnnouncesTheGate(t *testing.T) {
	_, inner, gated := newGateFixture(t)
	if !contains(gated.Definition().Description, "requires the owner's approval") {
		t.Fatalf("gated description does not announce the gate: %q", gated.Definition().Description)
	}
	if !contains(gated.Definition().Description, inner.Definition().Description) {
		t.Fatal("gated description dropped the tool's own description")
	}
	if string(gated.Definition().Schema) != string(inner.Definition().Schema) {
		t.Fatal("gated tool changed the schema the model calls it with")
	}
}

func TestApprovedCallExecutesOnce(t *testing.T) {
	service, inner, gated := newGateFixture(t)
	ctx := context.Background()
	if _, err := gated.Execute(ctx, json.RawMessage(`{"to":"someone@example.com"}`)); err != nil {
		t.Fatal(err)
	}
	pending, _ := service.Pending(ctx)
	approval := pending[0]
	if err := service.Decide(ctx, approval.ID, true); err != nil {
		t.Fatal(err)
	}
	executor := NewApprovalToolExecutor(staticLookup{"mail__send_message": gated}, service)
	stored, err := loadApproval(service, ctx, approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.ExecuteApproved(ctx, stored)
	if err != nil {
		t.Fatal(err)
	}
	if len(inner.calls) != 1 || inner.calls[0] != `{"to":"someone@example.com"}` {
		t.Fatalf("approved call did not reach the server exactly once: %v", inner.calls)
	}
	if result != `{"ok":true}` {
		t.Fatalf("unexpected approved result: %v", result)
	}
	// Replay: the approval is consumed, so a second execution is refused.
	if _, err := executor.ExecuteApproved(ctx, stored); !errors.Is(err, approvals.ErrNotAuthorized) {
		t.Fatalf("expected a used approval to be unusable, got %v", err)
	}
	if len(inner.calls) != 1 {
		t.Fatalf("a consumed approval executed the tool again: %v", inner.calls)
	}
}

// An approval authorizes one call, not a class of calls.
func TestApprovalDoesNotAuthorizeDifferentArgumentsOrTool(t *testing.T) {
	service, inner, gated := newGateFixture(t)
	other := &recordingTool{name: "mail__delete_message"}
	ctx := context.Background()
	if _, err := gated.Execute(ctx, json.RawMessage(`{"to":"someone@example.com"}`)); err != nil {
		t.Fatal(err)
	}
	pending, _ := service.Pending(ctx)
	if err := service.Decide(ctx, pending[0].ID, true); err != nil {
		t.Fatal(err)
	}
	stored, err := loadApproval(service, ctx, pending[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	executor := NewApprovalToolExecutor(staticLookup{
		"mail__send_message":   gated,
		"mail__delete_message": other,
	}, service)

	tampered := stored
	tampered.Payload = json.RawMessage(`{"arguments":{"to":"attacker@example.com"},"tool":"mail__send_message"}`)
	if _, err := executor.ExecuteApproved(ctx, tampered); !errors.Is(err, approvals.ErrPayloadMismatch) {
		t.Fatalf("changed arguments were accepted: %v", err)
	}
	redirected := stored
	redirected.Payload = json.RawMessage(`{"arguments":{"to":"someone@example.com"},"tool":"mail__delete_message"}`)
	if _, err := executor.ExecuteApproved(ctx, redirected); !errors.Is(err, approvals.ErrPayloadMismatch) {
		t.Fatalf("a different tool was accepted: %v", err)
	}
	if len(inner.calls) != 0 || len(other.calls) != 0 {
		t.Fatalf("a rejected approval still executed something: %v %v", inner.calls, other.calls)
	}
}

// A rejected approval must never execute, and the tap is the only thing that
// decides.
func TestRejectedApprovalNeverExecutes(t *testing.T) {
	service, inner, gated := newGateFixture(t)
	ctx := context.Background()
	if _, err := gated.Execute(ctx, json.RawMessage(`{"to":"someone@example.com"}`)); err != nil {
		t.Fatal(err)
	}
	pending, _ := service.Pending(ctx)
	if err := service.Decide(ctx, pending[0].ID, false); err != nil {
		t.Fatal(err)
	}
	stored, err := loadApproval(service, ctx, pending[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	executor := NewApprovalToolExecutor(staticLookup{"mail__send_message": gated}, service)
	if _, err := executor.ExecuteApproved(ctx, stored); !errors.Is(err, approvals.ErrNotAuthorized) {
		t.Fatalf("expected a rejected approval to be unusable, got %v", err)
	}
	if len(inner.calls) != 0 {
		t.Fatalf("a rejected approval executed the tool: %v", inner.calls)
	}
}

// The executor resolves the tool from the live catalog at execution time, so
// an approval issued before a reconnect still runs against the tool that
// replaced it.
func TestApprovedCallResolvesTheLiveToolNotTheGatedOne(t *testing.T) {
	service, _, gated := newGateFixture(t)
	ctx := context.Background()
	if _, err := gated.Execute(ctx, json.RawMessage(`{"to":"someone@example.com"}`)); err != nil {
		t.Fatal(err)
	}
	pending, _ := service.Pending(ctx)
	if err := service.Decide(ctx, pending[0].ID, true); err != nil {
		t.Fatal(err)
	}
	stored, err := loadApproval(service, ctx, pending[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	// The catalog was rebuilt: a new inner tool behind a new gate, same name.
	reconnected := &recordingTool{name: "mail__send_message", result: `{"ok":"reconnected"}`}
	executor := NewApprovalToolExecutor(staticLookup{
		"mail__send_message": NewApprovalGatedTool(reconnected, service, service),
	}, service)
	result, err := executor.ExecuteApproved(ctx, stored)
	if err != nil {
		t.Fatal(err)
	}
	if len(reconnected.calls) != 1 {
		t.Fatalf("approved call did not reach the reconnected tool: %v", reconnected.calls)
	}
	if result != `{"ok":"reconnected"}` {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestAutoModeRunsTheCallAndReturnsItsResult(t *testing.T) {
	service, inner, gated := newGateFixture(t)
	ctx := context.Background()
	if err := service.SetMode(ctx, ports.ModeAuto); err != nil {
		t.Fatal(err)
	}
	raw, err := gated.Execute(ctx, json.RawMessage(`{"to":"someone@example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(inner.calls) != 1 {
		t.Fatalf("auto mode did not run the call: %v", inner.calls)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("auto mode did not return the tool's own result: %s", raw)
	}
	pending, _ := service.Pending(ctx)
	if len(pending) != 0 {
		t.Fatalf("auto mode still recorded an approval: %+v", pending)
	}
	// Switching it back off restores the gate on the next call, without a
	// restart and without rebuilding the tool.
	if err := service.SetMode(ctx, ports.ModeNormal); err != nil {
		t.Fatal(err)
	}
	if _, err := gated.Execute(ctx, json.RawMessage(`{"to":"someone@example.com"}`)); err != nil {
		t.Fatal(err)
	}
	if len(inner.calls) != 1 {
		t.Fatalf("the gate did not come back on: %v", inner.calls)
	}
}

// A rule gates the calls it names and leaves the rest alone, which is what
// makes a multi-action tool gateable at all: an approval prompt in front of
// reading the calendar teaches the owner to approve without looking.
func TestRuleGatesOnlyTheCallsItNames(t *testing.T) {
	store := newFakeStateStore()
	service := NewApprovalService(store, time.Now, 30*time.Minute, ports.ModeNormal)
	inner := &recordingTool{name: "google_calendar"}
	rule := ApprovalRule{
		Gated: func(arguments json.RawMessage) bool {
			var call struct{ Action string }
			_ = json.Unmarshal(arguments, &call)
			return call.Action == "delete"
		},
		Notice: " Only delete asks.",
	}
	gated := NewApprovalGatedToolIf(inner, service, service, rule)
	ctx := context.Background()

	raw, err := gated.Execute(ctx, json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(inner.calls) != 1 || string(raw) != `{"ok":true}` {
		t.Fatalf("an ungated action did not run inline: calls=%v result=%s", inner.calls, raw)
	}
	if pending, _ := service.Pending(ctx); len(pending) != 0 {
		t.Fatalf("an ungated action asked the owner: %+v", pending)
	}

	if _, err := gated.Execute(ctx, json.RawMessage(`{"action":"delete","event_id":"e1"}`)); err != nil {
		t.Fatal(err)
	}
	if len(inner.calls) != 1 {
		t.Fatalf("a gated action ran without approval: %v", inner.calls)
	}
	pending, _ := service.Pending(ctx)
	if len(pending) != 1 || pending[0].Action != ApprovalToolCall {
		t.Fatalf("gated action recorded no approval: %+v", pending)
	}

	// The notice must name what stops, or the model avoids the actions that
	// were never gated in the first place.
	if !contains(gated.Definition().Description, "Only delete asks.") {
		t.Fatalf("description=%q", gated.Definition().Description)
	}
}

// A rule that cannot read the call gates it. The two mistakes do not cost the
// same: an unnecessary prompt is an annoyance, an ungated mutation is the thing
// the gate exists to stop.
func TestUnreadableArgumentsAreGated(t *testing.T) {
	store := newFakeStateStore()
	service := NewApprovalService(store, time.Now, 30*time.Minute, ports.ModeNormal)
	inner := &recordingTool{name: "google_calendar"}
	rule := ApprovalRule{Gated: func(arguments json.RawMessage) bool {
		var call struct{ Action string }
		return json.Unmarshal(arguments, &call) != nil || call.Action != "list"
	}}
	gated := NewApprovalGatedToolIf(inner, service, service, rule)
	// Arguments that are not JSON at all fail at the approval payload rather
	// than reaching the tool, which is the safe end of that failure. What must
	// hold either way is that nothing executed.
	for _, arguments := range []string{``, `not json`, `{}`} {
		_, _ = gated.Execute(context.Background(), json.RawMessage(arguments))
	}
	if len(inner.calls) != 0 {
		t.Fatalf("an unreadable call ran ungated: %v", inner.calls)
	}
}

// The three modes on one tool. Strict is the only one that stops a read, and
// it has to stop one, or "ask me about everything" is not what it says.
func TestModesDecideWhatStops(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []ports.ApprovalMode{ports.ModeStrict, ports.ModeNormal, ports.ModeAuto} {
		store := newFakeStateStore()
		service := NewApprovalService(store, time.Now, 30*time.Minute, ports.ModeNormal)
		if err := service.SetMode(ctx, mode); err != nil {
			t.Fatal(err)
		}
		reader := &recordingTool{name: "google_calendar"}
		writer := &recordingTool{name: "google_calendar"}
		read := NewApprovalGatedToolIf(reader, service, service, RuleFor(ports.ToolDefinition{Effect: ports.ReadOnlyTool()}))
		write := NewApprovalGatedToolIf(writer, service, service, RuleFor(ports.ToolDefinition{Effect: ports.MutatingActions("delete")}))

		if _, err := read.Execute(ctx, json.RawMessage(`{"action":"list"}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := write.Execute(ctx, json.RawMessage(`{"action":"delete"}`)); err != nil {
			t.Fatal(err)
		}
		readRan, wroteRan := len(reader.calls) == 1, len(writer.calls) == 1
		wantRead, wantWrite := mode != ports.ModeStrict, mode == ports.ModeAuto
		if readRan != wantRead || wroteRan != wantWrite {
			t.Fatalf("%s: read ran=%v (want %v), write ran=%v (want %v)", mode, readRan, wantRead, wroteRan, wantWrite)
		}
	}
}

// The mode is durable runtime state, so a tool wrapped at boot has to notice a
// change without a restart -- otherwise tightening the setting does nothing
// until the next one, which is the wrong direction for this particular knob.
func TestChangingTheModeTakesEffectOnTheNextCall(t *testing.T) {
	ctx := context.Background()
	service := NewApprovalService(newFakeStateStore(), time.Now, 30*time.Minute, ports.ModeNormal)
	inner := &recordingTool{name: "read_file"}
	tool := NewApprovalGatedToolIf(inner, service, service, RuleFor(ports.ToolDefinition{Effect: ports.ReadOnlyTool()}))

	if _, err := tool.Execute(ctx, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if len(inner.calls) != 1 {
		t.Fatalf("a read did not run in normal mode: %v", inner.calls)
	}
	if err := service.SetMode(ctx, ports.ModeStrict); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if len(inner.calls) != 1 {
		t.Fatalf("strict mode did not close on the same tool object: %v", inner.calls)
	}
}

// An owner who left the old boolean bypass on must not have the gate come back
// on under them because a field was renamed.
func TestTheRetiredAutoBooleanIsHonouredOnce(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	if _, err := store.Update(ctx, 0, func(state *ports.State) error {
		state.ApprovalAutoMode = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := NewApprovalService(store, time.Now, 30*time.Minute, ports.ModeNormal)
	mode, err := service.Mode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ports.ModeAuto {
		t.Fatalf("mode=%q, want the existing bypass carried forward", mode)
	}
	// Once the owner chooses, the retired field must not be able to contradict
	// it later.
	if err := service.SetMode(ctx, ports.ModeStrict); err != nil {
		t.Fatal(err)
	}
	if mode, _ := service.Mode(ctx); mode != ports.ModeStrict {
		t.Fatalf("mode=%q, want the explicit choice to win", mode)
	}
}

// Config says where a deployment starts; the owner's choice outranks it from
// then on, or /mode would be undone by the next restart.
func TestConfiguredDefaultOnlyAppliesUntilTheOwnerChooses(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	service := NewApprovalService(store, time.Now, 30*time.Minute, ports.ModeStrict)
	if mode, _ := service.Mode(ctx); mode != ports.ModeStrict {
		t.Fatalf("mode=%q, want the configured default", mode)
	}
	if err := service.SetMode(ctx, ports.ModeNormal); err != nil {
		t.Fatal(err)
	}
	// A fresh service over the same state is what a restart looks like.
	restarted := NewApprovalService(store, time.Now, 30*time.Minute, ports.ModeStrict)
	if mode, _ := restarted.Mode(ctx); mode != ports.ModeNormal {
		t.Fatalf("mode=%q, want the owner's stored choice to survive a restart", mode)
	}
}

func TestUnknownModesAreRefused(t *testing.T) {
	service := NewApprovalService(newFakeStateStore(), time.Now, 30*time.Minute, ports.ModeNormal)
	if err := service.SetMode(context.Background(), ports.ApprovalMode("readonly")); err == nil {
		t.Fatal("an unknown mode was stored")
	}
	if mode, _ := service.Mode(context.Background()); mode != ports.ModeNormal {
		t.Fatalf("mode=%q, want the refusal to have changed nothing", mode)
	}
}

// An unreadable switch must leave the gate standing rather than be read as
// "off", or a broken state store becomes a bypass.
func TestUnreadableAutoModeDoesNotOpenTheGate(t *testing.T) {
	inner := &recordingTool{name: "mail__send_message"}
	gated := NewApprovalGatedTool(inner, failingApprovals{}, failingApprovals{})
	if _, err := gated.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected an unreadable auto-mode switch to fail the call")
	}
	if len(inner.calls) != 0 {
		t.Fatalf("an unreadable switch let the call through: %v", inner.calls)
	}
}

type failingApprovals struct{}

func (failingApprovals) Mode(context.Context) (ports.ApprovalMode, error) {
	return "", errors.New("state unavailable")
}

func (failingApprovals) Request(context.Context, approvals.Action, any, string) (approvals.Approval, error) {
	return approvals.Approval{}, errors.New("state unavailable")
}

func loadApproval(service *ApprovalService, ctx context.Context, id string) (approvals.Approval, error) {
	pending, err := service.Pending(ctx)
	if err != nil {
		return approvals.Approval{}, err
	}
	for _, approval := range pending {
		if approval.ID == id {
			return approval, nil
		}
	}
	// Decided approvals are no longer pending, so read them from the store the
	// same way turns.Approval does.
	state, err := service.store.Load(ctx)
	if err != nil {
		return approvals.Approval{}, err
	}
	approval, ok := state.Approvals[id]
	if !ok {
		return approvals.Approval{}, errors.New("approval not found")
	}
	return approval, nil
}
