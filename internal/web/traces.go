package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// TraceDirectory is the read half of the trace store, declared here rather
// than taken as the full ports.TraceStore for the same reason ThreadDirectory
// and HistoryReader are: writing traces belongs to the turn path, and an HTTP
// handler holding the whole interface could start or prune one by accident.
type TraceDirectory interface {
	ListTraces(ctx context.Context, limit int) ([]ports.Trace, error)
	Trace(ctx context.Context, id string) (trace ports.Trace, spans []ports.TraceSpan, found bool, err error)
}

// traceListLimit bounds one page of the trace list. It is a display bound,
// unrelated to tracing.keep_turns, which bounds what is stored.
const (
	traceListLimit    = 50
	traceListMaxLimit = 200
)

// Traces answer as JSON objects rather than through webResult's table shape.
// A trace is a nested document -- a turn holding an ordered list of spans,
// each holding a prompt -- and flattening it to headers and string rows would
// mean the panel parsing structure back out of strings.
type traceSummaryJSON struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	Channel        string `json:"channel"`
	Source         string `json:"source"`
	Kind           string `json:"kind"`
	Model          string `json:"model"`
	Effort         string `json:"effort,omitempty"`
	Input          string `json:"input"`
	Output         string `json:"output"`
	Error          string `json:"error,omitempty"`
	Spans          int    `json:"spans"`
	StartedAt      string `json:"started_at"`
	DurationMS     int64  `json:"duration_ms"`
	Complete       bool   `json:"complete"`
	TotalTokens    int64  `json:"total_tokens"`
	PromptTokens   int64  `json:"prompt_tokens"`
	OutputTokens   int64  `json:"completion_tokens"`
	CachedTokens   int64  `json:"cached_prompt_tokens,omitempty"`
}

type traceSpanJSON struct {
	Sequence     int    `json:"sequence"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	CallID       string `json:"call_id,omitempty"`
	Request      string `json:"request"`
	Response     string `json:"response"`
	Error        string `json:"error,omitempty"`
	StartedAt    string `json:"started_at"`
	DurationMS   int64  `json:"duration_ms"`
	TotalTokens  int64  `json:"total_tokens,omitempty"`
	PromptTokens int64  `json:"prompt_tokens,omitempty"`
	OutputTokens int64  `json:"completion_tokens,omitempty"`
}

type traceDetailJSON struct {
	Trace traceSummaryJSON `json:"trace"`
	Spans []traceSpanJSON  `json:"spans"`
}

func newTraceListHandler(traces TraceDirectory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := traceListLimit
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 {
				writeWebError(w, http.StatusBadRequest, "limit must be a positive integer")
				return
			}
			limit = min(parsed, traceListMaxLimit)
		}
		recorded, err := traces.ListTraces(r.Context(), limit)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows := make([]traceSummaryJSON, 0, len(recorded))
		for _, trace := range recorded {
			rows = append(rows, traceSummary(trace))
		}
		writeTraceJSON(w, struct {
			Traces []traceSummaryJSON `json:"traces"`
		}{Traces: rows})
	}
}

func newTraceDetailHandler(traces TraceDirectory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		trace, spans, found, err := traces.Trace(r.Context(), r.PathValue("id"))
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			writeWebError(w, http.StatusNotFound, "trace not found")
			return
		}
		rendered := make([]traceSpanJSON, 0, len(spans))
		for _, span := range spans {
			rendered = append(rendered, traceSpanJSON{
				Sequence: span.Sequence, Kind: string(span.Kind), Name: span.Name, CallID: span.CallID,
				Request: span.Request, Response: span.Response, Error: span.Error,
				StartedAt: span.StartedAt.UTC().Format(time.RFC3339Nano),
				// Milliseconds rather than a Go duration string: the panel
				// formats it, and a number is the only shape it can also sort
				// and compare on.
				DurationMS:   span.Duration.Milliseconds(),
				TotalTokens:  span.Usage.TotalTokens,
				PromptTokens: span.Usage.PromptTokens,
				OutputTokens: span.Usage.CompletionTokens,
			})
		}
		writeTraceJSON(w, traceDetailJSON{Trace: traceSummary(trace), Spans: rendered})
	}
}

func traceSummary(trace ports.Trace) traceSummaryJSON {
	return traceSummaryJSON{
		ID: trace.ID, ConversationID: trace.ConversationID, Channel: trace.Channel,
		Source: trace.Source, Kind: trace.Kind, Model: trace.Model, Effort: trace.Effort,
		Input: trace.Input, Output: trace.Output, Error: trace.Error, Spans: trace.Spans,
		StartedAt:    trace.StartedAt.UTC().Format(time.RFC3339Nano),
		DurationMS:   trace.Duration.Milliseconds(),
		Complete:     trace.Complete,
		TotalTokens:  trace.Usage.TotalTokens,
		PromptTokens: trace.Usage.PromptTokens,
		OutputTokens: trace.Usage.CompletionTokens,
		CachedTokens: trace.Usage.CachedPromptTokens,
	}
}

func writeTraceJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	body, err := json.Marshal(value)
	if err != nil {
		writeWebError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = w.Write(body)
}
