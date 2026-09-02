package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// A trace is the answer to "what did it actually do". Eggy already keeps the
// conversation, but the conversation is the smallest part of a turn: the
// prompt that produced a reply, the tools the model reached for, and what
// those handed back are all gone the moment the turn ends, which is exactly
// when someone starts wondering about them.
//
// The recorder captures them at the two boundaries a turn has:
//
//   - TracedModel wraps ports.Model, so every provider call is recorded at
//     the wire boundary. Wrapping the port rather than instrumenting one
//     adapter is what makes this provider-neutral: a backend added later is
//     traced because it is a ports.Model, not because anyone remembered.
//   - the loop's event stream, which the turn path already subscribes to for
//     the live "Calling X..." indicator, carries tool start and end.
//
// Both write through one open trace, found on the context, so the two streams
// share a single ordering. Nothing here is on the answer path: a trace write
// that fails is logged and the turn continues, because a turn must never fail
// over its own bookkeeping.

type traceIDKey struct{}

// WithTraceID stamps the running turn's trace onto ctx. TracedModel reads it
// back; there is no other way for a model adapter, which is handed nothing
// but a request, to know which turn it is serving.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey{}, id)
}

// TraceIDFromContext reports the trace of the running turn, or "" outside
// one. Empty is the honest answer: a model call made outside a turn (a
// warm-up, a test) belongs to no trace and is not recorded rather than
// attached to whichever trace happened to be open.
func TraceIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(traceIDKey{}).(string)
	return id
}

// TraceRecorder writes turn traces. A nil *TraceRecorder is a working
// recorder that records nothing, which is how tracing stays free when it is
// switched off: bootstrap leaves the field nil, no model is wrapped, and no
// call site needs a branch.
type TraceRecorder struct {
	store  ports.TraceStore
	guard  *SecretGuard
	now    func() time.Time
	logger *slog.Logger
	// maxBodyBytes caps one recorded prompt or tool output. Full bodies are
	// the point of this feature, so it is a safety valve rather than a
	// budget: a tool that returns a hundred megabytes must not be able to
	// take the database with it.
	maxBodyBytes int
	keep         int
	retention    time.Duration

	mu     sync.Mutex
	active map[string]*TraceTurn
}

// TraceOptions are the retention and size limits an owner configured. Zero
// values fall back to the defaults here rather than to "unbounded": a trace
// store with no ceiling is a disk-full outage waiting for a busy day.
type TraceOptions struct {
	Keep         int
	Retention    time.Duration
	MaxBodyBytes int
	Now          func() time.Time
	Logger       *slog.Logger
}

const (
	defaultTraceKeep      = 500
	defaultTraceRetention = 7 * 24 * time.Hour
	defaultTraceBodyBytes = 1 << 20
)

// NewTraceRecorder builds the recorder. store is required; guard may be nil,
// in which case nothing is redacted -- callers on the turn path always pass
// one, because a prompt is the most likely place in Eggy for a credential to
// appear verbatim.
func NewTraceRecorder(store ports.TraceStore, guard *SecretGuard, options TraceOptions) *TraceRecorder {
	if store == nil {
		return nil
	}
	if guard == nil {
		guard = NewSecretGuard(nil)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Keep <= 0 {
		options.Keep = defaultTraceKeep
	}
	if options.Retention <= 0 {
		options.Retention = defaultTraceRetention
	}
	if options.MaxBodyBytes <= 0 {
		options.MaxBodyBytes = defaultTraceBodyBytes
	}
	return &TraceRecorder{
		store: store, guard: guard, now: options.Now, logger: options.Logger,
		maxBodyBytes: options.MaxBodyBytes, keep: options.Keep, retention: options.Retention,
		active: map[string]*TraceTurn{},
	}
}

// Begin opens a trace for one turn and returns the context the turn must run
// on. The returned *TraceTurn is safe to use when nil, so a caller holding a
// nil recorder writes the same code as one holding a real recorder.
func (r *TraceRecorder) Begin(ctx context.Context, trace ports.Trace) (context.Context, *TraceTurn) {
	if r == nil {
		return ctx, nil
	}
	trace.ID = newTraceID()
	trace.StartedAt = r.now().UTC()
	trace.Input = r.body(trace.Input)
	turn := &TraceTurn{recorder: r, trace: trace, pending: map[string]time.Time{}}
	r.mu.Lock()
	r.active[trace.ID] = turn
	r.mu.Unlock()
	if err := r.store.StartTrace(r.detached(ctx), trace); err != nil {
		r.logger.Error("trace start failed", "trace_id", trace.ID, "error", err)
	}
	return WithTraceID(ctx, trace.ID), turn
}

// turn resolves the open trace ctx belongs to, or nil when there is none.
func (r *TraceRecorder) turn(ctx context.Context) *TraceTurn {
	if r == nil {
		return nil
	}
	id := TraceIDFromContext(ctx)
	if id == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active[id]
}

// detached strips cancellation from a context used for a trace write. A turn
// the owner stopped is precisely the one whose trace they will want to read,
// and writing it on the cancelled context would discard it.
func (r *TraceRecorder) detached(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

// body redacts and caps one recorded payload.
func (r *TraceRecorder) body(text string) string {
	text = r.guard.Redact(text)
	if len(text) <= r.maxBodyBytes {
		return text
	}
	// The marker is part of the record: a reader must be able to tell a
	// tool that returned nothing more from one that was cut off here.
	return text[:r.maxBodyBytes] + "\n...[truncated by tracing.max_body_bytes]"
}

// TraceTurn is one open trace. Every method tolerates a nil receiver.
type TraceTurn struct {
	recorder *TraceRecorder
	trace    ports.Trace

	mu       sync.Mutex
	sequence int
	// pending times a tool call from its start event to its end event. The
	// loop runs tools one at a time, but keying by call ID rather than
	// holding one field means a loop that ever runs them in parallel reports
	// real durations instead of nonsense.
	pending map[string]time.Time
	// toolsRecorded marks that the full tool schemas have been written once.
	// They are identical on every call of a turn and are the largest constant
	// part of a prompt, so recording them per call would multiply the store
	// by the step count to say the same thing.
	toolsRecorded bool
}

// ID is the trace's identifier, or "" when nothing is being recorded.
func (t *TraceTurn) ID() string {
	if t == nil {
		return ""
	}
	return t.trace.ID
}

// RecordModelCall writes one provider call. It is called by TracedModel
// rather than by the turn path: the turn does not see the requests the loop
// builds, and reconstructing them would be a second implementation of the
// prompt, guaranteed to drift from the one actually sent.
func (t *TraceTurn) RecordModelCall(request ports.ModelRequest, response ports.ModelResponse, callErr error, started time.Time, duration time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	includeSchemas := !t.toolsRecorded
	t.toolsRecorded = true
	t.mu.Unlock()

	span := ports.TraceSpan{
		Kind:      ports.TraceSpanModelCall,
		Name:      request.Model,
		Request:   encodeTraceJSON(modelRequestRecord(request, includeSchemas)),
		StartedAt: started,
		Duration:  duration,
		Usage:     response.Usage,
	}
	if callErr != nil {
		span.Error = callErr.Error()
	} else {
		span.Response = encodeTraceJSON(modelResponseRecord(response))
	}
	t.append(span)
}

// ToolStarted notes when a tool call began, so the span written when it ends
// carries a real duration.
func (t *TraceTurn) ToolStarted(call ports.ToolCall) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.pending[call.ID] = t.recorder.now().UTC()
	t.mu.Unlock()
}

// ToolFinished writes one tool span: the arguments the model chose and the
// output handed back to it, which is what the next model call reads.
func (t *TraceTurn) ToolFinished(call ports.ToolCall, output string, toolErr error) {
	if t == nil {
		return
	}
	now := t.recorder.now().UTC()
	t.mu.Lock()
	started, ok := t.pending[call.ID]
	delete(t.pending, call.ID)
	t.mu.Unlock()
	if !ok {
		started = now
	}
	span := ports.TraceSpan{
		Kind:      ports.TraceSpanToolCall,
		Name:      call.Name,
		CallID:    call.ID,
		Request:   string(call.Arguments),
		Response:  output,
		StartedAt: started,
		Duration:  now.Sub(started),
	}
	if toolErr != nil {
		span.Error = toolErr.Error()
	}
	t.append(span)
}

// Complete closes the trace and enforces retention. It is called once, after
// the loop returns, whatever the loop returned: a turn that hit the tool-step
// limit or was stopped mid-flight is the one most worth having a record of.
func (t *TraceTurn) Complete(ctx context.Context, output string, turnErr error, usage ports.ModelUsage) {
	if t == nil {
		return
	}
	recorder := t.recorder
	t.mu.Lock()
	t.trace.Output = recorder.body(output)
	t.trace.Usage = usage
	t.trace.Duration = recorder.now().UTC().Sub(t.trace.StartedAt)
	t.trace.Complete = true
	if turnErr != nil {
		t.trace.Error = turnErr.Error()
	}
	completed := t.trace
	t.mu.Unlock()
	detached := recorder.detached(ctx)
	if err := recorder.store.CompleteTrace(detached, completed); err != nil {
		recorder.logger.Error("trace completion failed", "trace_id", completed.ID, "error", err)
	}
	recorder.mu.Lock()
	delete(recorder.active, completed.ID)
	recorder.mu.Unlock()
	// Pruning at completion rather than on a timer keeps the store bounded
	// without a background loop that costs something on a deployment that
	// never traces anything.
	if _, err := recorder.store.PruneTraces(detached, recorder.keep, recorder.now().UTC().Add(-recorder.retention)); err != nil {
		recorder.logger.Error("trace prune failed", "error", err)
	}
}

// SetModel records which model actually ran the turn, once the turn path has
// resolved it. It is separate from Begin because the alias is known up front
// and the provider's model ID only after the first call resolves.
func (t *TraceTurn) SetModel(model string) {
	if t == nil || model == "" {
		return
	}
	// Guarded like every other field written after Begin. The loop is
	// sequential today, so this is never contended -- but pending above
	// already promises to survive a loop that runs steps in parallel, and one
	// unguarded write would make that promise false.
	t.mu.Lock()
	defer t.mu.Unlock()
	t.trace.Model = model
}

func (t *TraceTurn) append(span ports.TraceSpan) {
	recorder := t.recorder
	t.mu.Lock()
	t.sequence++
	span.Sequence = t.sequence
	t.mu.Unlock()
	span.TraceID = t.trace.ID
	span.Request = recorder.body(span.Request)
	span.Response = recorder.body(span.Response)
	// Spans are written on a background context: the span describes work
	// that already happened, and a cancelled turn must still record it.
	if err := recorder.store.AppendSpan(context.Background(), span); err != nil {
		recorder.logger.Error("trace span write failed", "trace_id", span.TraceID, "kind", span.Kind, "error", err)
	}
}

// TracedModel records every call made through one model adapter. It is a
// ports.Model wrapping a ports.Model, applied in bootstrap, so no adapter
// knows tracing exists and no adapter can forget to support it.
type TracedModel struct {
	model    ports.Model
	recorder *TraceRecorder
}

// NewTracedModel wraps model. A nil recorder returns model unchanged, so an
// unconfigured deployment carries no wrapper at all.
func NewTracedModel(model ports.Model, recorder *TraceRecorder) ports.Model {
	if recorder == nil || model == nil {
		return model
	}
	return TracedModel{model: model, recorder: recorder}
}

func (m TracedModel) Generate(ctx context.Context, request ports.ModelRequest) (ports.ModelResponse, error) {
	turn := m.recorder.turn(ctx)
	if turn == nil {
		return m.model.Generate(ctx, request)
	}
	started := m.recorder.now().UTC()
	response, err := m.model.Generate(ctx, request)
	turn.SetModel(request.Model)
	turn.RecordModelCall(request, response, err, started, m.recorder.now().UTC().Sub(started))
	return response, err
}

// The recorded shapes below are declared here rather than reusing the ports
// types directly, because what a trace holds is a rendering decision: the
// panel reads these field names, and a ports type is free to change without
// breaking a stored record it was never meant to define.

type tracedRequest struct {
	Model           string             `json:"model"`
	ReasoningEffort string             `json:"reasoning_effort,omitempty"`
	Messages        []ports.Message    `json:"messages"`
	ToolNames       []string           `json:"tool_names"`
	Tools           []tracedToolSchema `json:"tools,omitempty"`
}

type tracedToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema,omitempty"`
}

type tracedResponse struct {
	Message          ports.Message `json:"message"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
}

func modelRequestRecord(request ports.ModelRequest, includeSchemas bool) tracedRequest {
	record := tracedRequest{
		Model:           request.Model,
		ReasoningEffort: request.ReasoningEffort,
		Messages:        request.Messages,
		ToolNames:       make([]string, 0, len(request.Tools)),
	}
	for _, tool := range request.Tools {
		record.ToolNames = append(record.ToolNames, tool.Name)
	}
	if includeSchemas {
		record.Tools = make([]tracedToolSchema, 0, len(request.Tools))
		for _, tool := range request.Tools {
			record.Tools = append(record.Tools, tracedToolSchema{Name: tool.Name, Description: tool.Description, Schema: tool.Schema})
		}
	}
	return record
}

func modelResponseRecord(response ports.ModelResponse) tracedResponse {
	return tracedResponse{Message: response.Message, ReasoningContent: response.ReasoningContent}
}

func encodeTraceJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		// A trace that cannot be encoded still says so rather than vanishing.
		return `{"trace_encode_error":` + strconv.Quote(err.Error()) + `}`
	}
	return string(encoded)
}

func newTraceID() string {
	data := make([]byte, 8)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}
