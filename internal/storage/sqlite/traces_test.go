package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func openTraceStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "eggy.db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestTraceRoundTripsTurnAndSpansInOrder(t *testing.T) {
	t.Parallel()

	store := openTraceStore(t)
	ctx := as("owner")
	started := time.Now().UTC().Truncate(time.Millisecond)

	if err := store.StartTrace(ctx, ports.Trace{
		ID: "trace-1", ConversationID: "thread-1", Channel: "web", Source: "web",
		Kind: ports.TraceKindOwner, Model: "main", Effort: "high", Input: "what changed?",
		StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	for _, span := range []ports.TraceSpan{
		{TraceID: "trace-1", Sequence: 1, Kind: ports.TraceSpanModelCall, Name: "gpt", Request: `{"messages":[]}`, Response: `{"message":{}}`, Usage: ports.ModelUsage{TotalTokens: 30}, StartedAt: started, Duration: 2 * time.Second},
		{TraceID: "trace-1", Sequence: 2, Kind: ports.TraceSpanToolCall, Name: "status", CallID: "call-1", Request: `{}`, Response: `{"ok":true}`, StartedAt: started, Duration: time.Millisecond},
	} {
		if err := store.AppendSpan(ctx, span); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CompleteTrace(ctx, ports.Trace{
		ID: "trace-1", Output: "nothing changed", Model: "gpt",
		Usage: ports.ModelUsage{TotalTokens: 30}, Duration: 3 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}

	trace, spans, found, err := store.Trace(ctx, "trace-1")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("trace not found after being written")
	}
	if trace.Input != "what changed?" || trace.Output != "nothing changed" {
		t.Fatalf("trace input/output = %q/%q", trace.Input, trace.Output)
	}
	// CompleteTrace stamps the provider's model ID over the alias the turn
	// opened with, so the record says what actually ran.
	if trace.Model != "gpt" {
		t.Fatalf("trace model = %q, want gpt", trace.Model)
	}
	if !trace.Complete || trace.Duration != 3*time.Second || trace.Usage.TotalTokens != 30 {
		t.Fatalf("trace outcome = %+v", trace)
	}
	if trace.Spans != 2 {
		t.Fatalf("trace span count = %d, want 2", trace.Spans)
	}
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(spans))
	}
	if spans[0].Kind != ports.TraceSpanModelCall || spans[1].Kind != ports.TraceSpanToolCall {
		t.Fatalf("spans returned out of sequence: %q then %q", spans[0].Kind, spans[1].Kind)
	}
	if spans[1].CallID != "call-1" || spans[1].Response != `{"ok":true}` {
		t.Fatalf("tool span = %+v", spans[1])
	}
	if spans[0].Usage.TotalTokens != 30 {
		t.Fatalf("model span usage = %+v", spans[0].Usage)
	}
}

func TestTraceReportsMissingWithoutError(t *testing.T) {
	t.Parallel()

	_, _, found, err := openTraceStore(t).Trace(as("owner"), "absent")
	if err != nil {
		t.Fatalf("missing trace must not be an error: %v", err)
	}
	if found {
		t.Fatal("found = true for a trace that was never written")
	}
}

func TestListTracesReturnsNewestFirst(t *testing.T) {
	t.Parallel()

	store := openTraceStore(t)
	ctx := as("owner")
	base := time.Now().UTC()
	for index, id := range []string{"old", "middle", "new"} {
		if err := store.StartTrace(ctx, ports.Trace{
			ID: id, ConversationID: "thread", Channel: "web", Kind: ports.TraceKindOwner,
			StartedAt: base.Add(time.Duration(index) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}

	traces, err := store.ListTraces(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 2 {
		t.Fatalf("traces = %d, want 2", len(traces))
	}
	if traces[0].ID != "new" || traces[1].ID != "middle" {
		t.Fatalf("order = %q, %q; want new, middle", traces[0].ID, traces[1].ID)
	}
}

func TestPruneTracesEnforcesCountAndAgeAndRemovesSpans(t *testing.T) {
	t.Parallel()

	store := openTraceStore(t)
	ctx := as("owner")
	base := time.Now().UTC()
	for index, id := range []string{"ancient", "old", "recent"} {
		if err := store.StartTrace(ctx, ports.Trace{
			ID: id, ConversationID: "thread", Channel: "web", Kind: ports.TraceKindOwner,
			StartedAt: base.Add(time.Duration(index) * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendSpan(ctx, ports.TraceSpan{TraceID: id, Sequence: 1, Kind: ports.TraceSpanToolCall, Name: "status"}); err != nil {
			t.Fatal(err)
		}
	}

	// Keeping two drops the oldest; the age cut-off then takes "old" as well.
	removed, err := store.PruneTraces(ctx, 2, base.Add(90*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	traces, err := store.ListTraces(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 1 || traces[0].ID != "recent" {
		t.Fatalf("remaining traces = %+v", traces)
	}

	var orphans int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trace_spans WHERE trace_id != 'recent'`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Fatalf("orphaned spans = %d, want 0", orphans)
	}
}

func TestAppendSpanDropsSpansForAPrunedTrace(t *testing.T) {
	t.Parallel()

	store := openTraceStore(t)
	ctx := as("owner")
	if err := store.AppendSpan(ctx, ports.TraceSpan{TraceID: "never-started", Sequence: 1, Kind: ports.TraceSpanToolCall, Name: "status"}); err != nil {
		t.Fatalf("a span for an unknown trace must be dropped, not an error: %v", err)
	}
	var spans int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trace_spans`).Scan(&spans); err != nil {
		t.Fatal(err)
	}
	if spans != 0 {
		t.Fatalf("orphan spans written = %d, want 0", spans)
	}
}

// Telegram's conversation ID never changes, so the session is the only thing
// that can tell one stretch of it from the next.
func TestTraceCarriesTheConversationSession(t *testing.T) {
	t.Parallel()

	store := openTraceStore(t)
	ctx := as("owner")
	started := time.Now().UTC().Truncate(time.Millisecond)

	for id, session := range map[string]string{"before-clear": "", "after-clear": "1757000000000000000"} {
		if err := store.StartTrace(ctx, ports.Trace{
			ID: id, ConversationID: "telegram", Session: session, Channel: "telegram",
			Source: "telegram", Kind: "owner", Model: "deepseek-v4-pro", Input: "hello", StartedAt: started,
		}); err != nil {
			t.Fatal(err)
		}
	}

	listed, err := store.ListTraces(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	sessions := map[string]string{}
	for _, trace := range listed {
		sessions[trace.ID] = trace.Session
	}
	if sessions["before-clear"] != "" || sessions["after-clear"] != "1757000000000000000" {
		t.Fatalf("sessions=%v", sessions)
	}

	trace, _, found, err := store.Trace(ctx, "after-clear")
	if err != nil || !found || trace.Session != "1757000000000000000" {
		t.Fatalf("trace=%+v found=%v err=%v", trace, found, err)
	}
}

func TestConversationResetAtReportsTheLastClear(t *testing.T) {
	t.Parallel()

	store := openTraceStore(t)
	ctx := as("owner")
	if _, found, err := store.ConversationResetAt(ctx, "telegram"); found || err != nil {
		t.Fatalf("found=%v err=%v before any clear", found, err)
	}

	cleared := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.ResetConversation(ctx, "telegram", cleared); err != nil {
		t.Fatal(err)
	}
	at, found, err := store.ConversationResetAt(ctx, "telegram")
	if err != nil || !found || !at.Equal(cleared) {
		t.Fatalf("at=%v found=%v err=%v want %v", at, found, err, cleared)
	}

	later := cleared.Add(time.Minute)
	if err := store.ResetConversation(ctx, "telegram", later); err != nil {
		t.Fatal(err)
	}
	if at, _, _ := store.ConversationResetAt(ctx, "telegram"); !at.Equal(later) {
		t.Fatalf("at=%v, want the most recent clear %v", at, later)
	}
}

// The chat reads back what each of a conversation's turns called, and only
// that: its own account's owner turns in that conversation, oldest first,
// each with its tool calls in the order they ran.
func TestConversationToolCallsReadsEachTurnsCallsInOrder(t *testing.T) {
	t.Parallel()

	store := openTraceStore(t)
	ctx := as("owner")
	base := time.Now().UTC().Truncate(time.Millisecond)
	start := func(ctx context.Context, id, conversation, kind string, at time.Time) {
		t.Helper()
		if err := store.StartTrace(ctx, ports.Trace{ID: id, ConversationID: conversation, Channel: "web", Source: "web", Kind: kind, Model: "m", StartedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	start(ctx, "first", "thread-1", ports.TraceKindOwner, base)
	start(ctx, "second", "thread-1", ports.TraceKindOwner, base.Add(time.Minute))
	start(ctx, "quiet", "thread-1", ports.TraceKindOwner, base.Add(2*time.Minute))
	start(ctx, "elsewhere", "thread-2", ports.TraceKindOwner, base)
	start(ctx, "beat", "thread-1", ports.TraceKindHeartbeat, base)
	start(as("someone-else"), "theirs", "thread-1", ports.TraceKindOwner, base)
	for _, span := range []ports.TraceSpan{
		{TraceID: "first", Sequence: 2, Kind: ports.TraceSpanToolCall, Name: "current_time", Request: `{}`},
		{TraceID: "first", Sequence: 1, Kind: ports.TraceSpanToolCall, Name: "web_search", Request: `{"query":"eggs"}`},
		{TraceID: "first", Sequence: 3, Kind: ports.TraceSpanModelCall, Name: "m", Request: `{"messages":[]}`},
		{TraceID: "second", Sequence: 1, Kind: ports.TraceSpanToolCall, Name: "memory", Request: `{"action":"add"}`},
	} {
		if err := store.AppendSpan(ctx, span); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CompleteTrace(ctx, ports.Trace{ID: "first", Model: "m", Duration: 4 * time.Second}); err != nil {
		t.Fatal(err)
	}

	turns, err := store.ConversationToolCalls(ctx, "thread-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 3 || turns[0].TraceID != "first" || turns[1].TraceID != "second" || turns[2].TraceID != "quiet" {
		t.Fatalf("turns=%+v", turns)
	}
	if !turns[0].Complete || turns[0].Duration != 4*time.Second || !turns[0].StartedAt.Equal(base) {
		t.Fatalf("first turn=%+v", turns[0])
	}
	calls := turns[0].Calls
	if len(calls) != 2 || calls[0].Name != "web_search" || string(calls[0].Arguments) != `{"query":"eggs"}` || calls[1].Name != "current_time" {
		t.Fatalf("first turn calls=%+v", calls)
	}
	if len(turns[2].Calls) != 0 {
		t.Fatalf("a turn that called nothing has calls: %+v", turns[2].Calls)
	}

	newest, err := store.ConversationToolCalls(ctx, "thread-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(newest) != 1 || newest[0].TraceID != "quiet" {
		t.Fatalf("limit kept %+v, want only the newest turn", newest)
	}
}
