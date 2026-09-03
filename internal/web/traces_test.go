package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeTraceDirectory struct {
	traces []ports.Trace
	spans  []ports.TraceSpan
	limit  int
}

func (f *fakeTraceDirectory) ListTraces(_ context.Context, limit int) ([]ports.Trace, error) {
	f.limit = limit
	return f.traces, nil
}

func (f *fakeTraceDirectory) Trace(_ context.Context, id string) (ports.Trace, []ports.TraceSpan, bool, error) {
	for _, trace := range f.traces {
		if trace.ID == id {
			return trace, f.spans, true, nil
		}
	}
	return ports.Trace{}, nil, false, nil
}

func traceTestHandler(t *testing.T, traces TraceDirectory) (http.Handler, *http.Cookie) {
	t.Helper()
	webConfig := testWebConfig(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	webConfig.Traces = traces
	handler := NewWebHandler("", webConfig)
	return handler, webLoginCookie(t, handler)
}

func getTrace(t *testing.T, handler http.Handler, cookie *http.Cookie, path string, into any) int {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if into != nil && response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), into); err != nil {
			t.Fatalf("decode %s: %v (body %s)", path, err, response.Body.String())
		}
	}
	return response.Code
}

func TestTraceListReturnsSummariesWithoutSpanBodies(t *testing.T) {
	t.Parallel()

	directory := &fakeTraceDirectory{traces: []ports.Trace{{
		ID: "trace-1", ConversationID: "thread-1", Channel: "web", Source: "web",
		Kind: ports.TraceKindOwner, Model: "gpt", Input: "what changed?", Output: "nothing",
		Spans: 4, StartedAt: time.Unix(1700000000, 0).UTC(), Duration: 1500 * time.Millisecond,
		Usage: ports.ModelUsage{TotalTokens: 120, PromptTokens: 100, CompletionTokens: 20}, Complete: true,
	}}}
	handler, cookie := traceTestHandler(t, directory)

	var body struct {
		Traces []traceSummaryJSON `json:"traces"`
	}
	if code := getTrace(t, handler, cookie, "/api/traces", &body); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(body.Traces) != 1 {
		t.Fatalf("traces = %d, want 1", len(body.Traces))
	}
	trace := body.Traces[0]
	if trace.ID != "trace-1" || trace.Spans != 4 || trace.DurationMS != 1500 || trace.TotalTokens != 120 {
		t.Fatalf("summary = %+v", trace)
	}
	if !trace.Complete || trace.Kind != ports.TraceKindOwner {
		t.Fatalf("summary = %+v", trace)
	}
}

func TestTraceListClampsAnOversizedLimitAndRefusesAnInvalidOne(t *testing.T) {
	t.Parallel()

	directory := &fakeTraceDirectory{}
	handler, cookie := traceTestHandler(t, directory)

	if code := getTrace(t, handler, cookie, "/api/traces?limit=9999", nil); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if directory.limit != traceListMaxLimit {
		t.Fatalf("limit = %d, want %d", directory.limit, traceListMaxLimit)
	}
	if code := getTrace(t, handler, cookie, "/api/traces?limit=0", nil); code != http.StatusBadRequest {
		t.Fatalf("status for limit=0 = %d, want 400", code)
	}
}

func TestTraceDetailReturnsSpansInOrder(t *testing.T) {
	t.Parallel()

	directory := &fakeTraceDirectory{
		traces: []ports.Trace{{ID: "trace-1", Kind: ports.TraceKindOwner, StartedAt: time.Unix(1700000000, 0).UTC()}},
		spans: []ports.TraceSpan{
			{Sequence: 1, Kind: ports.TraceSpanModelCall, Name: "gpt", Request: `{"messages":[]}`, Response: `{"message":{}}`, Duration: 900 * time.Millisecond, Usage: ports.ModelUsage{TotalTokens: 50, CachedPromptTokens: 40}},
			{Sequence: 2, Kind: ports.TraceSpanToolCall, Name: "status", CallID: "call-1", Request: `{}`, Response: `{"ok":true}`},
		},
	}
	handler, cookie := traceTestHandler(t, directory)

	var body traceDetailJSON
	if code := getTrace(t, handler, cookie, "/api/traces/trace-1", &body); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(body.Spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(body.Spans))
	}
	if body.Spans[0].Kind != string(ports.TraceSpanModelCall) || body.Spans[0].DurationMS != 900 || body.Spans[0].TotalTokens != 50 || body.Spans[0].CachedTokens != 40 {
		t.Fatalf("model span = %+v", body.Spans[0])
	}
	if body.Spans[1].CallID != "call-1" || body.Spans[1].Request != `{}` {
		t.Fatalf("tool span = %+v", body.Spans[1])
	}
}

func TestTraceDetailIsANotFoundForAnUnknownID(t *testing.T) {
	t.Parallel()

	handler, cookie := traceTestHandler(t, &fakeTraceDirectory{})
	if code := getTrace(t, handler, cookie, "/api/traces/absent", nil); code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

// Tracing switched off leaves the routes unmounted rather than serving an
// empty list. The distinction is the point: an empty 200 says "no turns yet"
// and a 404 says "this deployment does not trace", and the panel tells the
// owner which one they are looking at.
func TestTraceRoutesAreAbsentWhenTracingIsOff(t *testing.T) {
	t.Parallel()

	webConfig := testWebConfig(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	handler := NewWebHandler("", webConfig)
	cookie := webLoginCookie(t, handler)
	if code := getTrace(t, handler, cookie, "/api/traces", nil); code == http.StatusOK {
		t.Fatal("the trace list answered while tracing was off")
	}
}

func TestTraceRoutesRequireASession(t *testing.T) {
	t.Parallel()

	webConfig := testWebConfig(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	webConfig.Traces = &fakeTraceDirectory{}
	handler := NewWebHandler("", webConfig)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/traces", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 -- prompts are the most sensitive thing Eggy stores", response.Code)
	}
}
