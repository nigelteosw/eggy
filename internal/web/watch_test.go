package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeWatchList struct {
	watch    string
	replaced []string
	err      error
}

func (f *fakeWatchList) Load(context.Context) (ports.AgentContext, error) {
	return ports.AgentContext{Watch: f.watch}, nil
}

func (f *fakeWatchList) ReplaceDocument(_ context.Context, document ports.ContextDocument, content string) error {
	if document != ports.ContextWatch {
		return errors.New("wrong document: " + string(document))
	}
	if f.err != nil {
		return f.err
	}
	f.replaced = append(f.replaced, content)
	f.watch = content
	return nil
}

func watchTestHandler(t *testing.T, watch WatchList) (http.Handler, *http.Cookie) {
	t.Helper()
	webConfig := testWebConfig(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	webConfig.Watch = watch
	handler := NewWebHandler("", webConfig)
	return handler, webLoginCookie(t, handler)
}

func postWatch(t *testing.T, handler http.Handler, cookie *http.Cookie, body string) webResult {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/context/watch", strings.NewReader(body))
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded webResult
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// The whole point of the card: the watch list was reachable only by asking the
// agent to write it down for itself, so an owner with no heartbeat had no way
// to see why.
func TestWebWatchGetReturnsTheStoredList(t *testing.T) {
	handler, cookie := watchTestHandler(t, &fakeWatchList{watch: "# Watch\n\n- PR #18\n"})

	request := httptest.NewRequest(http.MethodGet, "/api/context/watch", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded webResult
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Fields) != 1 || decoded.Fields[0].Label != "watch" || decoded.Fields[0].Value != "# Watch\n\n- PR #18\n" {
		t.Fatalf("fields=%#v", decoded.Fields)
	}
}

func TestWebWatchSetReplacesTheWholeDocument(t *testing.T) {
	store := &fakeWatchList{watch: "# Watch\n\n- PR #18\n"}
	handler, cookie := watchTestHandler(t, store)

	result := postWatch(t, handler, cookie, `{"content":"# Watch\n\n- unread mail older than a day\n"}`)
	if result.Title != "Saved watch list." {
		t.Fatalf("title=%q", result.Title)
	}
	if len(store.replaced) != 1 || store.replaced[0] != "# Watch\n\n- unread mail older than a day\n" {
		t.Fatalf("replaced=%#v", store.replaced)
	}
	// A config write asks for a restart; this one must not, because the
	// heartbeat reads the store on every tick.
	if strings.Contains(result.Detail, "restart") {
		t.Fatalf("detail=%q", result.Detail)
	}
}

// A list of nothing but headings is what the daemon skips on. Saving one is
// allowed -- it is how an owner parks the heartbeat -- but silently
// acknowledging it would leave them waiting for beats that never come.
func TestWebWatchSetSaysWhenTheSavedListWillSkipEveryBeat(t *testing.T) {
	handler, cookie := watchTestHandler(t, &fakeWatchList{})

	for name, content := range map[string]string{
		"empty":            `{"content":""}`,
		"headings only":    `{"content":"# Watch\n\n"}`,
		"blank lines only": `{"content":"   \n\n"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if detail := postWatch(t, handler, cookie, content).Detail; !strings.Contains(detail, "skip every tick") {
				t.Fatalf("detail=%q", detail)
			}
		})
	}

	if detail := postWatch(t, handler, cookie, `{"content":"# Watch\n\n- PR #18\n"}`).Detail; detail != "" {
		t.Fatalf("detail=%q", detail)
	}
}

// A deployment with no context store must not draw a card that cannot save.
func TestWebWatchRoutesAreAbsentWithoutAStore(t *testing.T) {
	handler, cookie := watchTestHandler(t, nil)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/context/watch", nil),
		httptest.NewRequest(http.MethodPost, "/api/context/watch", strings.NewReader(`{"content":"x"}`)),
	} {
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d", request.Method, response.Code)
		}
	}
}

// The session gate is the whole authorization story for a document that tells
// Eggy what to look at.
func TestWebWatchRoutesRequireASession(t *testing.T) {
	webConfig := testWebConfig(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	webConfig.Watch = &fakeWatchList{}
	handler := NewWebHandler("", webConfig)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/context/watch", nil),
		httptest.NewRequest(http.MethodPost, "/api/context/watch", strings.NewReader(`{"content":"x"}`)),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", request.Method, response.Code)
		}
	}
}
