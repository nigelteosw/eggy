package turns

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

type recordingTraceStore struct {
	mu        sync.Mutex
	started   []ports.Trace
	spans     []ports.TraceSpan
	completed []ports.Trace
}

func (s *recordingTraceStore) StartTrace(_ context.Context, trace ports.Trace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = append(s.started, trace)
	return nil
}

func (s *recordingTraceStore) AppendSpan(_ context.Context, span ports.TraceSpan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spans = append(s.spans, span)
	return nil
}

func (s *recordingTraceStore) CompleteTrace(_ context.Context, trace ports.Trace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = append(s.completed, trace)
	return nil
}

func (s *recordingTraceStore) ListTraces(context.Context, int) ([]ports.Trace, error) {
	return nil, nil
}

func (s *recordingTraceStore) Trace(context.Context, string) (ports.Trace, []ports.TraceSpan, bool, error) {
	return ports.Trace{}, nil, false, nil
}

func (s *recordingTraceStore) PruneTraces(context.Context, int, time.Time) (int, error) {
	return 0, nil
}

func newTracedTestService(loop *fakeLoop, channel *fakeChannel, store ports.TraceStore) *Service {
	return New(Options{
		Registry: &fakeRegistry{}, Conversation: &fakeConversation{}, Context: fakeContextStore{},
		Store: fakeStore{}, Runtime: fakeRuntime{}, Skills: fakeSkills{}, Loop: loop,
		Channel: channel, Now: func() time.Time { return time.Unix(0, 0).UTC() },
		Traces: services.NewTraceRecorder(store, nil, services.TraceOptions{
			Keep: 10, Retention: time.Hour, MaxBodyBytes: 1 << 16,
			Now: func() time.Time { return time.Unix(0, 0).UTC() },
		}),
	})
}

// A turn's trace has to be openable from inside the loop, because that is
// where the model calls and tool calls it records actually happen.
func TestTurnOpensATraceTheLoopContextCarries(t *testing.T) {
	t.Parallel()

	store := &recordingTraceStore{}
	var seen string
	loop := &fakeLoop{reply: "done", onRun: func(ctx context.Context) {
		seen = services.TraceIDFromContext(ctx)
	}}
	ctx := destination.With(context.Background(), destination.Destination{Kind: destination.Web, ThreadID: "thread-1"})
	if err := newTracedTestService(loop, &fakeChannel{}, store).OwnerMessage(ctx, "what changed?", "web"); err != nil {
		t.Fatal(err)
	}

	if len(store.started) != 1 {
		t.Fatalf("started traces = %d, want 1", len(store.started))
	}
	trace := store.started[0]
	if seen == "" || seen != trace.ID {
		t.Fatalf("loop context trace ID = %q, trace = %q", seen, trace.ID)
	}
	if trace.Kind != ports.TraceKindOwner || trace.Source != "web" || trace.Input != "what changed?" {
		t.Fatalf("trace = %+v", trace)
	}
	if trace.ConversationID != "thread-1" {
		t.Fatalf("trace conversation = %q, want thread-1", trace.ConversationID)
	}
	if len(store.completed) != 1 || store.completed[0].Output != "done" {
		t.Fatalf("completed = %+v", store.completed)
	}
}

// The heartbeat and scheduled turns are labelled as such, because "who asked
// for this" is the first question about a turn nobody remembers requesting.
func TestUnpromptedTurnsAreLabelledInTheirTrace(t *testing.T) {
	t.Parallel()

	for name, run := range map[string]struct {
		start func(*Service) error
		want  string
	}{
		"scheduled": {start: func(s *Service) error { return s.ScheduledTurn(context.Background(), "check") }, want: ports.TraceKindScheduled},
		"heartbeat": {start: func(s *Service) error {
			_, err := s.HeartbeatTurn(context.Background(), "check", false)
			return err
		}, want: ports.TraceKindHeartbeat},
	} {
		t.Run(name, func(t *testing.T) {
			store := &recordingTraceStore{}
			if err := run.start(newTracedTestService(&fakeLoop{reply: "ok"}, &fakeChannel{}, store)); err != nil {
				t.Fatal(err)
			}
			if len(store.started) != 1 || store.started[0].Kind != run.want {
				t.Fatalf("trace kind = %+v, want %q", store.started, run.want)
			}
		})
	}
}

// Tool calls reach the trace through the same event stream the live "Calling
// X..." indicator uses, so the record and what the owner watched cannot
// describe different turns.
func TestToolEventsBecomeTraceSpans(t *testing.T) {
	t.Parallel()

	store := &recordingTraceStore{}
	call := ports.ToolCall{ID: "call-1", Name: "status", Arguments: []byte(`{}`)}
	loop := &fakeLoop{reply: "done"}
	loop.onRun = func(context.Context) {
		loop.options.OnEvent(agent.Event{Kind: agent.EventToolStart, Call: call})
		loop.options.OnEvent(agent.Event{Kind: agent.EventToolEnd, Call: call, Output: `{"ok":true}`})
		failing := ports.ToolCall{ID: "call-2", Name: "read_file", Arguments: []byte(`{}`)}
		loop.options.OnEvent(agent.Event{Kind: agent.EventToolStart, Call: failing})
		loop.options.OnEvent(agent.Event{Kind: agent.EventToolError, Call: failing, Output: `{"error":"gone"}`, Err: errors.New("gone")})
	}
	if err := newTracedTestService(loop, &fakeChannel{}, store).OwnerMessage(context.Background(), "go", "web"); err != nil {
		t.Fatal(err)
	}

	if len(store.spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(store.spans))
	}
	if store.spans[0].Name != "status" || store.spans[0].Response != `{"ok":true}` {
		t.Fatalf("first span = %+v", store.spans[0])
	}
	if store.spans[1].Name != "read_file" || store.spans[1].Error != "gone" {
		t.Fatalf("second span = %+v", store.spans[1])
	}
}

// Tracing off is the zero value, and the turn path must not notice.
func TestTurnRunsUnchangedWithoutARecorder(t *testing.T) {
	t.Parallel()

	loop := &fakeLoop{reply: "done"}
	loop.onRun = func(ctx context.Context) {
		if id := services.TraceIDFromContext(ctx); id != "" {
			t.Errorf("an untraced turn stamped trace ID %q", id)
		}
		loop.options.OnEvent(agent.Event{Kind: agent.EventToolStart, Call: ports.ToolCall{ID: "call-1", Name: "status"}})
		loop.options.OnEvent(agent.Event{Kind: agent.EventToolEnd, Call: ports.ToolCall{ID: "call-1", Name: "status"}, Output: "{}"})
	}
	channel := &fakeChannel{}
	if err := newTestService(loop, channel).OwnerMessage(context.Background(), "go", "web"); err != nil {
		t.Fatal(err)
	}
	if len(channel.delivered) != 1 || channel.delivered[0] != "done" {
		t.Fatalf("delivered = %v", channel.delivered)
	}
}
