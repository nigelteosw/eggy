package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
)

type fakeGoogleCompleter struct {
	codes, states []string
	err           error
}

func (f *fakeGoogleCompleter) CompleteLogin(_ context.Context, code, state string) error {
	f.codes, f.states = append(f.codes, code), append(f.states, state)
	return f.err
}

// The callback is unauthenticated -- Google redirects a browser to it -- so a
// request without the state this process issued must never reach the exchange,
// and a finished login lands the owner back where they pressed Connect.
func TestGoogleCallbackRequiresStateAndReturnsToTheCard(t *testing.T) {
	completer := &fakeGoogleCompleter{}
	handler := googleCallbackHandler(completer)

	for _, target := range []string{"/auth/google/callback?code=4/x", "/auth/google/callback?state=abc"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d, want 400", target, response.Code)
		}
	}
	if len(completer.codes) != 0 {
		t.Fatalf("an incomplete redirect reached the exchange: %v", completer.codes)
	}

	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/auth/google/callback?error=access_denied", nil))
	if denied.Code != http.StatusBadRequest || !strings.Contains(denied.Body.String(), "access_denied") {
		t.Fatalf("denied: status=%d body=%q", denied.Code, denied.Body.String())
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/google/callback?code=4/x&state=abc", nil))
	if response.Code != http.StatusFound || response.Header().Get("Location") != googleCallbackReturn {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	if completer.codes[0] != "4/x" || completer.states[0] != "abc" {
		t.Fatalf("exchanged code=%v state=%v", completer.codes, completer.states)
	}

	// The wrong account is the failure an owner is most likely to cause, and
	// the adapter's reason is what tells them to sign in as Eggy instead.
	completer.err = errors.New("the Google connection is not Eggy's own account")
	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, httptest.NewRequest(http.MethodGet, "/auth/google/callback?code=4/y&state=abc", nil))
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "not Eggy's own account") {
		t.Fatalf("refused: status=%d body=%q", refused.Code, refused.Body.String())
	}
}

func TestGoogleRedirectFollowsTheClientType(t *testing.T) {
	cfg := config.Config{Server: config.ServerConfig{PublicBaseURL: "https://eggy.example/"}}
	if got := googleRedirectURL(cfg); got != "" {
		t.Fatalf("desktop redirect=%q, want the adapter's loopback default", got)
	}
	cfg.Google.ClientType = config.GoogleClientWeb
	if got := googleRedirectURL(cfg); got != "https://eggy.example/auth/google/callback" {
		t.Fatalf("web redirect=%q", got)
	}
}
