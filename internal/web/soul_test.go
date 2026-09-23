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

type fakeSoulDocument struct{ soul string }

func (f *fakeSoulDocument) Load(context.Context) (ports.AgentContext, error) {
	return ports.AgentContext{Soul: f.soul}, nil
}

func (f *fakeSoulDocument) ReplaceDocument(_ context.Context, document ports.ContextDocument, content string) error {
	if document != ports.ContextSoul {
		return errors.New("wrong document: " + string(document))
	}
	f.soul = content
	return nil
}

func soulRequest(t *testing.T, handler http.Handler, cookie *http.Cookie, method, body string) webResult {
	t.Helper()
	request := httptest.NewRequest(method, "/api/context/soul", strings.NewReader(body))
	attachSession(request, cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", method, response.Code, response.Body.String())
	}
	var decoded webResult
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// SOUL.md is a setting like any other: the panel reads it and writes it whole,
// and saving nothing is the reset to Eggy's built-in soul.
func TestWebSoulReadsWritesAndResets(t *testing.T) {
	store := &fakeSoulDocument{soul: "# Eggy Soul\n\nWarm.\n"}
	webConfig := testWebConfig(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	webConfig.Documents = store
	handler := NewWebHandler("", webConfig)
	cookie := webLoginCookie(t, handler)

	got := soulRequest(t, handler, cookie, http.MethodGet, "")
	if len(got.Fields) != 1 || got.Fields[0].Label != "soul" || got.Fields[0].Value != "# Eggy Soul\n\nWarm.\n" {
		t.Fatalf("fields=%#v", got.Fields)
	}
	saved := soulRequest(t, handler, cookie, http.MethodPost, `{"content":"# Eggy Soul\n\nTerse.\n"}`)
	if saved.Title != "Saved soul." || store.soul != "# Eggy Soul\n\nTerse.\n" || strings.Contains(saved.Detail, "restart") {
		t.Fatalf("result=%#v soul=%q", saved, store.soul)
	}
	reset := soulRequest(t, handler, cookie, http.MethodPost, `{"content":"  "}`)
	if !strings.Contains(reset.Detail, "built-in") {
		t.Fatalf("reset detail=%q", reset.Detail)
	}
}
