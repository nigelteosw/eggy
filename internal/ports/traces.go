package ports

import (
	"context"
	"time"
)

// TraceSpanKind names what one recorded step of a turn was. There are two,
// because a turn only ever does two things: it asks a model, and it runs
// what the model asked for.
type TraceSpanKind string

const (
	// TraceSpanModelCall is one request to a model provider and the response
	// it produced: the exact prompt that went out, the assistant message that
	// came back (tool calls included), and what it cost.
	TraceSpanModelCall TraceSpanKind = "model_call"
	// TraceSpanToolCall is one tool execution: the arguments the model chose
	// and the output handed back to it, which is the next call's input.
	TraceSpanToolCall TraceSpanKind = "tool_call"
)

// Trace kinds, matching the three entry points in internal/core/turns. They
// are recorded so the panel can tell a turn the owner is sitting in front of
// from one that fired while nobody was watching -- which is the first thing
// worth knowing about a turn that surprised them.
const (
	TraceKindOwner     = "owner"
	TraceKindScheduled = "scheduled"
	TraceKindHeartbeat = "heartbeat"
)

// TraceSpan is one step inside a turn, in the order it happened. Sequence is
// that order and is unique within a trace: model calls and tool calls share
// one counter because they interleave, and reading them apart would lose the
// only thing a trace is for -- what followed what.
//
// Request and Response are JSON documents rather than typed fields. What
// belongs in them differs per Kind (a prompt and a completion, or arguments
// and an output), and a trace is a record of what actually crossed the
// boundary: freezing it into a struct would mean a provider growing a field
// silently stops being recorded.
type TraceSpan struct {
	ID       int64
	TraceID  string
	Sequence int
	Kind     TraceSpanKind
	// Name is the model ID for a model call, the tool name for a tool call.
	Name string
	// CallID is the model-issued tool call ID, so a tool span can be matched
	// to the assistant message that asked for it. Empty on a model call.
	CallID    string
	Request   string
	Response  string
	Error     string
	Usage     ModelUsage
	StartedAt time.Time
	Duration  time.Duration
}

// Trace is one turn end to end: what started it, which model ran it, what it
// cost, and what it answered. Its steps are TraceSpans.
//
// It is deliberately not a message: MemoryStore holds the conversation the
// owner had, and this holds what Eggy did to produce it. Merging them would
// make every recall and search read prompts nobody asked for.
type Trace struct {
	ID string
	// ConversationID scopes a trace to the same thread MemoryStore uses, so
	// the panel can line a trace up against the messages it produced.
	ConversationID string
	// Session distinguishes the stretches of one conversation that /clear
	// separates: it holds the conversation's last reset time when the turn
	// began, and is empty before the first clear. Telegram is why it
	// exists -- its conversation ID never changes, so clearing is the only
	// signal that one line of work ended and another began, and the traces
	// panel groups on the pair.
	Session string
	Channel string
	Source  string
	// Kind is one of TraceKindOwner, TraceKindScheduled, TraceKindHeartbeat.
	Kind   string
	Model  string
	Effort string
	Input  string
	Output string
	Error  string
	Usage  ModelUsage
	// Spans is how many steps the trace holds. ListTraces fills it so the
	// list can show the shape of a turn without loading any of its bodies,
	// which are the expensive part.
	Spans     int
	StartedAt time.Time
	Duration  time.Duration
	// Complete is false while the turn is still running, and stays false for
	// one the process died in the middle of. A trace that never completed is
	// evidence, not corruption, so it is kept and shown as such.
	Complete bool
}

// TraceStore persists turn traces. It is written by the recorder on the turn
// path and read by the web panel; nothing in the agent's own context ever
// reads it back, which is what keeps a trace from feeding itself into the
// next prompt.
type TraceStore interface {
	StartTrace(ctx context.Context, trace Trace) error
	AppendSpan(ctx context.Context, span TraceSpan) error
	// CompleteTrace fills in the outcome of trace.ID. A trace ID that no
	// longer exists (pruned mid-turn) is not an error.
	CompleteTrace(ctx context.Context, trace Trace) error
	// ListTraces returns the most recent traces, newest first, without their
	// spans' bodies.
	ListTraces(ctx context.Context, limit int) ([]Trace, error)
	// Trace returns one trace with every span it holds, oldest first,
	// reporting found=false with a nil error when there is no such trace.
	Trace(ctx context.Context, id string) (trace Trace, spans []TraceSpan, found bool, err error)
	// PruneTraces enforces the retention budget: everything older than
	// before goes, and beyond that only the newest keep traces are retained.
	// Full prompt bodies are the largest thing Eggy writes, so this is not
	// optional housekeeping -- it is what makes recording them affordable.
	PruneTraces(ctx context.Context, keep int, before time.Time) (removed int, err error)
}
