package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeTraceStore struct {
	mu        sync.Mutex
	started   []ports.Trace
	spans     []ports.TraceSpan
	completed []ports.Trace
	pruned    []int
	before    []time.Time
}

func (s *fakeTraceStore) StartTrace(_ context.Context, trace ports.Trace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = append(s.started, trace)
	return nil
}

func (s *fakeTraceStore) AppendSpan(_ context.Context, span ports.TraceSpan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spans = append(s.spans, span)
	return nil
}

func (s *fakeTraceStore) CompleteTrace(_ context.Context, trace ports.Trace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = append(s.completed, trace)
	return nil
}

func (s *fakeTraceStore) ListTraces(context.Context, int) ([]ports.Trace, error) { return nil, nil }

func (s *fakeTraceStore) Trace(context.Context, string) (ports.Trace, []ports.TraceSpan, bool, error) {
	return ports.Trace{}, nil, false, nil
}

func (s *fakeTraceStore) PruneTraces(_ context.Context, keep int, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruned = append(s.pruned, keep)
	s.before = append(s.before, before)
	return 0, nil
}

type stubModel struct {
	response ports.ModelResponse
	err      error
	requests []ports.ModelRequest
}

func (m *stubModel) Generate(_ context.Context, request ports.ModelRequest) (ports.ModelResponse, error) {
	m.requests = append(m.requests, request)
	return m.response, m.err
}

func newTestRecorder(t *testing.T, store ports.TraceStore, secrets ...string) *TraceRecorder {
	t.Helper()
	return NewTraceRecorder(store, NewSecretGuard(secrets), TraceOptions{
		Keep: 3, Retention: time.Hour, MaxBodyBytes: 1 << 16,
		Now: func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
}

func TestNilRecorderRecordsNothingAndStaysUsable(t *testing.T) {
	t.Parallel()

	var recorder *TraceRecorder
	ctx, turn := recorder.Begin(context.Background(), ports.Trace{Input: "hello"})
	if TraceIDFromContext(ctx) != "" {
		t.Fatal("a nil recorder must not stamp a trace ID")
	}
	// Every method has to survive the nil turn, because that is what makes the
	// turn path branch-free when tracing is off.
	turn.ToolStarted(ports.ToolCall{ID: "a", Name: "status"})
	turn.ToolFinished(ports.ToolCall{ID: "a", Name: "status"}, "{}", nil)
	turn.SetModel("gpt")
	turn.Complete(ctx, "done", nil, ports.ModelUsage{})
	if turn.ID() != "" {
		t.Fatalf("nil turn ID = %q, want empty", turn.ID())
	}
}

func TestTracedModelRecordsPromptAndResponseAgainstTheOpenTurn(t *testing.T) {
	t.Parallel()

	store := &fakeTraceStore{}
	recorder := newTestRecorder(t, store)
	ctx, turn := recorder.Begin(context.Background(), ports.Trace{Kind: ports.TraceKindOwner, Model: "main", Input: "what changed?"})

	model := NewTracedModel(&stubModel{response: ports.ModelResponse{
		Message: ports.Message{Role: ports.RoleAssistant, Content: "nothing"},
		Usage:   ports.ModelUsage{TotalTokens: 42},
	}}, recorder)
	if _, err := model.Generate(ctx, ports.ModelRequest{
		Model:    "gpt-x",
		Messages: []ports.Message{{Role: ports.RoleUser, Content: "what changed?"}},
		Tools:    []ports.ToolDefinition{{Name: "status", Description: "read status", Schema: []byte(`{"type":"object"}`)}},
	}); err != nil {
		t.Fatal(err)
	}

	if len(store.spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(store.spans))
	}
	span := store.spans[0]
	if span.TraceID != turn.ID() || span.Sequence != 1 || span.Kind != ports.TraceSpanModelCall {
		t.Fatalf("span = %+v", span)
	}
	if span.Name != "gpt-x" || span.Usage.TotalTokens != 42 {
		t.Fatalf("span name/usage = %q/%+v", span.Name, span.Usage)
	}
	// The prompt is recorded verbatim, which is the whole point: a
	// reconstruction would be a second implementation of the prompt.
	if !strings.Contains(span.Request, `"what changed?"`) {
		t.Fatalf("request did not carry the prompt: %s", span.Request)
	}
	if !strings.Contains(span.Response, `"nothing"`) {
		t.Fatalf("response did not carry the completion: %s", span.Response)
	}
	// Tool schemas ride along on the first call of a turn only.
	if !strings.Contains(span.Request, `"read status"`) {
		t.Fatalf("first model call must carry full tool schemas: %s", span.Request)
	}

	if _, err := model.Generate(ctx, ports.ModelRequest{
		Model: "gpt-x",
		Tools: []ports.ToolDefinition{{Name: "status", Description: "read status", Schema: []byte(`{"type":"object"}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	second := store.spans[1]
	if strings.Contains(second.Request, `"read status"`) {
		t.Fatalf("schemas must be recorded once per turn, not per call: %s", second.Request)
	}
	if !strings.Contains(second.Request, `"status"`) {
		t.Fatalf("every call must still record which tools it offered: %s", second.Request)
	}
}

func TestTracedModelReplacesImageBytesWithAMarker(t *testing.T) {
	store := &fakeTraceStore{}
	recorder := newTestRecorder(t, store)
	ctx, _ := recorder.Begin(context.Background(), ports.Trace{Kind: ports.TraceKindOwner})
	model := NewTracedModel(&stubModel{response: ports.ModelResponse{Message: ports.Message{Content: "seen"}}}, recorder)

	_, err := model.Generate(ctx, ports.ModelRequest{Model: "gpt-x", Messages: []ports.Message{{
		Role: ports.RoleUser, Content: "inspect",
		Parts: []ports.ContentPart{{Type: ports.ContentTypeImage, MediaType: "image/png", Data: []byte("secret pixels")}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.spans) != 1 {
		t.Fatalf("spans=%#v", store.spans)
	}
	request := store.spans[0].Request
	if strings.Contains(request, "secret pixels") || strings.Contains(request, "c2VjcmV0IHBpeGVscw==") {
		t.Fatalf("trace persisted image bytes: %s", request)
	}
	if !strings.Contains(request, "[image attached]") {
		t.Fatalf("trace omitted image marker: %s", request)
	}
}

func TestTracedModelIgnoresCallsOutsideATurn(t *testing.T) {
	t.Parallel()

	store := &fakeTraceStore{}
	model := NewTracedModel(&stubModel{}, newTestRecorder(t, store))
	if _, err := model.Generate(context.Background(), ports.ModelRequest{Model: "gpt-x"}); err != nil {
		t.Fatal(err)
	}
	if len(store.spans) != 0 {
		t.Fatalf("spans = %d for a call with no open trace, want 0", len(store.spans))
	}
}

func TestTracedModelRecordsAFailedCall(t *testing.T) {
	t.Parallel()

	store := &fakeTraceStore{}
	recorder := newTestRecorder(t, store)
	ctx, _ := recorder.Begin(context.Background(), ports.Trace{Kind: ports.TraceKindOwner})
	model := NewTracedModel(&stubModel{err: errors.New("provider timeout")}, recorder)
	if _, err := model.Generate(ctx, ports.ModelRequest{Model: "gpt-x"}); err == nil {
		t.Fatal("the wrapper must not swallow the provider error")
	}
	if len(store.spans) != 1 || store.spans[0].Error != "provider timeout" {
		t.Fatalf("failed call span = %+v", store.spans)
	}
}

func TestToolSpansInterleaveWithModelCallsInOneSequence(t *testing.T) {
	t.Parallel()

	store := &fakeTraceStore{}
	recorder := newTestRecorder(t, store)
	ctx, turn := recorder.Begin(context.Background(), ports.Trace{Kind: ports.TraceKindOwner})
	model := NewTracedModel(&stubModel{}, recorder)

	if _, err := model.Generate(ctx, ports.ModelRequest{Model: "gpt-x"}); err != nil {
		t.Fatal(err)
	}
	call := ports.ToolCall{ID: "call-1", Name: "status", Arguments: []byte(`{"verbose":true}`)}
	turn.ToolStarted(call)
	turn.ToolFinished(call, `{"ok":true}`, nil)
	if _, err := model.Generate(ctx, ports.ModelRequest{Model: "gpt-x"}); err != nil {
		t.Fatal(err)
	}

	kinds := make([]ports.TraceSpanKind, 0, len(store.spans))
	for index, span := range store.spans {
		if span.Sequence != index+1 {
			t.Fatalf("span %d has sequence %d", index, span.Sequence)
		}
		kinds = append(kinds, span.Kind)
	}
	want := []ports.TraceSpanKind{ports.TraceSpanModelCall, ports.TraceSpanToolCall, ports.TraceSpanModelCall}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for index := range want {
		if kinds[index] != want[index] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	if store.spans[1].Request != `{"verbose":true}` || store.spans[1].Response != `{"ok":true}` {
		t.Fatalf("tool span = %+v", store.spans[1])
	}
}

func TestToolFailureIsRecordedAsTheSpansError(t *testing.T) {
	t.Parallel()

	store := &fakeTraceStore{}
	recorder := newTestRecorder(t, store)
	_, turn := recorder.Begin(context.Background(), ports.Trace{Kind: ports.TraceKindOwner})
	call := ports.ToolCall{ID: "call-1", Name: "read_file", Arguments: []byte(`{}`)}
	turn.ToolStarted(call)
	turn.ToolFinished(call, `{"error":"no such file"}`, errors.New("no such file"))

	if len(store.spans) != 1 || store.spans[0].Error != "no such file" {
		t.Fatalf("tool error span = %+v", store.spans)
	}
}

func TestCompleteRecordsOutcomeAndPrunesToTheConfiguredBudget(t *testing.T) {
	t.Parallel()

	store := &fakeTraceStore{}
	recorder := newTestRecorder(t, store)
	ctx, turn := recorder.Begin(context.Background(), ports.Trace{Kind: ports.TraceKindOwner})
	turn.Complete(ctx, "all done", errors.New("step limit"), ports.ModelUsage{TotalTokens: 9})

	if len(store.completed) != 1 {
		t.Fatalf("completed = %d, want 1", len(store.completed))
	}
	completed := store.completed[0]
	if completed.Output != "all done" || completed.Error != "step limit" || completed.Usage.TotalTokens != 9 || !completed.Complete {
		t.Fatalf("completed trace = %+v", completed)
	}
	if len(store.pruned) != 1 || store.pruned[0] != 3 {
		t.Fatalf("prune keep = %v, want [3]", store.pruned)
	}
}

func TestCompleteStillWritesOnACancelledContext(t *testing.T) {
	t.Parallel()

	store := &fakeTraceStore{}
	recorder := newTestRecorder(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	ctx, turn := recorder.Begin(ctx, ports.Trace{Kind: ports.TraceKindOwner})
	// A turn the owner stopped is exactly the one whose trace they will read.
	cancel()
	turn.Complete(ctx, "stopped", context.Canceled, ports.ModelUsage{})

	if len(store.completed) != 1 {
		t.Fatalf("a stopped turn must still complete its trace: %+v", store.completed)
	}
}

func TestRecordedBodiesAreRedactedAndCapped(t *testing.T) {
	t.Parallel()

	store := &fakeTraceStore{}
	recorder := NewTraceRecorder(store, NewSecretGuard([]string{"hunter2"}), TraceOptions{
		Keep: 3, Retention: time.Hour, MaxBodyBytes: 64,
		Now: func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	_, turn := recorder.Begin(context.Background(), ports.Trace{Kind: ports.TraceKindOwner})
	call := ports.ToolCall{ID: "call-1", Name: "terminal", Arguments: []byte(`{"cmd":"echo hunter2"}`)}
	turn.ToolStarted(call)
	turn.ToolFinished(call, strings.Repeat("x", 500), nil)

	span := store.spans[0]
	if strings.Contains(span.Request, "hunter2") {
		t.Fatalf("an active secret reached the trace: %s", span.Request)
	}
	if !strings.Contains(span.Request, "[redacted]") {
		t.Fatalf("redaction marker missing: %s", span.Request)
	}
	if len(span.Response) > 64+len("\n...[truncated by tracing.max_body_bytes]") {
		t.Fatalf("response body = %d bytes, want capped", len(span.Response))
	}
	if !strings.HasSuffix(span.Response, "[truncated by tracing.max_body_bytes]") {
		t.Fatal("a truncated body must say so")
	}
}
