package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// The complete cutover, through the real HTTP and webhook handlers: a local
// password account created and signed in by both paths, private data that
// stays private, People management in the hands of every trusted user, and
// a removal that leaves nothing the removed person can still use. Both
// users' turns reach the one shared model provider.
func TestLocalAccountsPasswordAndTelegramWebRemainIsolated(t *testing.T) {
	app, _, sends, logs, _, models := newWebLinkApp(t)
	ctx := context.Background()

	// 1. A signs in with the environment credentials and creates B with a
	// local password and a mapped Telegram sender.
	aCookie := loginThrough(t, app, "owner@example.com", "operator-password")
	if aCookie == nil {
		t.Fatal("the environment login did not sign nigel in")
	}
	aID, aCSRF := sessionOf(t, app, aCookie)
	if aID != "nigel" {
		t.Fatalf("nigel's session resolved to %q", aID)
	}
	if response := apiCall(t, app, aCookie, aCSRF, http.MethodPost, "/api/config/accounts", `{"id":"third","telegram_user_id":99,"password":"third-password-long"}`); response.Code != http.StatusOK {
		t.Fatalf("create third: status=%d body=%s", response.Code, response.Body.String())
	}

	// 2. B signs in with the password, and by a Telegram /web link: the
	// verified private chat mints it, the browser redeems it.
	bCookie := loginThrough(t, app, "third", "third-password-long")
	if bCookie == nil {
		t.Fatal("third could not sign in with the local password")
	}
	bID, bCSRF := sessionOf(t, app, bCookie)
	if bID != "third" {
		t.Fatalf("third's session resolved to %q", bID)
	}
	if code := postTelegram(app, 10, 99, "/web"); code != http.StatusNoContent {
		t.Fatalf("webhook status=%d", code)
	}
	drain(t, app)
	replies := sends()
	if len(replies) == 0 {
		t.Fatal("no reply to /web")
	}
	match := webLinkPattern.FindStringSubmatch(replies[len(replies)-1])
	if match == nil {
		t.Fatalf("reply carries no link: %s", replies[len(replies)-1])
	}
	linkToken := match[1]
	request := httptest.NewRequest(http.MethodPost, "/api/login/link", strings.NewReader(`{"token":"`+linkToken+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://eggy.example")
	request.Header.Set("X-Eggy-Login", "1")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 {
		t.Fatalf("link redemption: status=%d body=%s", response.Code, response.Body.String())
	}
	linkCookie := response.Result().Cookies()[0]
	if id, _ := sessionOf(t, app, linkCookie); id != "third" {
		t.Fatal("the link session did not resolve to third")
	}

	// 3. Distinct conversations through the one shared provider: each
	// turn's prompt carries its own user's words, never the other's.
	createThread := func(cookie *http.Cookie, csrf string) string {
		created := apiCall(t, app, cookie, csrf, http.MethodPost, "/api/chat/threads", "")
		if created.Code != http.StatusCreated {
			t.Fatalf("create thread: status=%d body=%s", created.Code, created.Body.String())
		}
		var thread struct{ ID string }
		_ = json.Unmarshal(created.Body.Bytes(), &thread)
		return thread.ID
	}
	send := func(cookie *http.Cookie, csrf, thread, text string) {
		if response := apiCall(t, app, cookie, csrf, http.MethodPost, "/api/chat/threads/"+thread+"/send", `{"text":`+quoteJSON(text)+`}`); response.Code != http.StatusAccepted {
			t.Fatalf("send: status=%d body=%s", response.Code, response.Body.String())
		}
		drain(t, app)
	}
	aThread := createThread(aCookie, aCSRF)
	bThread := createThread(bCookie, bCSRF)
	before := len(models())
	send(aCookie, aCSRF, aThread, "nigel's oak planting plan")
	if len(models()) != before+1 {
		t.Fatalf("nigel's turn made %d model calls", len(models())-before)
	}
	send(bCookie, bCSRF, bThread, "third asks about the calendar")
	if len(models()) != before+2 {
		t.Fatalf("third's turn made %d model calls", len(models())-before-1)
	}
	aSeen, bSeen := false, false
	for _, body := range models()[before:] {
		if strings.Contains(body, "oak planting") {
			aSeen = true
			if strings.Contains(body, "third asks") {
				t.Fatal("nigel's prompt carried third's words")
			}
		}
		if strings.Contains(body, "third asks") {
			bSeen = true
			if strings.Contains(body, "oak planting") {
				t.Fatal("third's prompt carried nigel's words")
			}
		}
	}
	if !aSeen || !bSeen {
		t.Fatalf("both users reached the provider: nigel=%v third=%v", aSeen, bSeen)
	}

	// 4. Traces stay private: A cannot list or fetch B's trace.
	listTraces := func(cookie *http.Cookie) []string {
		response := apiCall(t, app, cookie, "", http.MethodGet, "/api/traces", "")
		if response.Code != http.StatusOK {
			t.Fatalf("list traces: status=%d body=%s", response.Code, response.Body.String())
		}
		var decoded struct {
			Traces []struct{ ID string } `json:"traces"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, len(decoded.Traces))
		for i, trace := range decoded.Traces {
			ids[i] = trace.ID
		}
		return ids
	}
	bTraces := listTraces(bCookie)
	if len(bTraces) == 0 {
		t.Fatal("third has no trace")
	}
	bTrace := bTraces[0]
	for _, id := range listTraces(aCookie) {
		if id == bTrace {
			t.Fatal("nigel lists third's trace")
		}
	}
	if response := apiCall(t, app, aCookie, aCSRF, http.MethodGet, "/api/traces/"+bTrace, ""); response.Code != http.StatusNotFound {
		t.Fatalf("nigel fetched third's trace: status=%d", response.Code)
	}

	// 5. Preferences are per account: A's mode change leaves B's alone.
	if response := apiCall(t, app, aCookie, aCSRF, http.MethodPost, "/api/approvals/mode?mode=strict", ""); response.Code != http.StatusOK {
		t.Fatalf("set mode: status=%d body=%s", response.Code, response.Body.String())
	}
	if response := apiCall(t, app, bCookie, bCSRF, http.MethodGet, "/api/approvals/mode", ""); !strings.Contains(response.Body.String(), "normal") {
		t.Fatalf("third's mode followed nigel's: %s", response.Body.String())
	}

	// 6. Every trusted user manages People: B resets partner's password
	// without needing partner's current one.
	if response := apiCall(t, app, bCookie, bCSRF, http.MethodPost, "/api/config/accounts/partner/password", `{"password":"partner-password-long"}`); response.Code != http.StatusOK {
		t.Fatalf("third resets partner's password: status=%d body=%s", response.Code, response.Body.String())
	}

	// 7. Prepare everything the removal has to kill: an open stream on B's
	// own thread, an unredeemed link, and a queued Telegram message.
	streamCtx, cancelStream := context.WithTimeout(ctx, 2*time.Second)
	defer cancelStream()
	streamRequest := httptest.NewRequest(http.MethodGet, "/api/chat/threads/"+bThread+"/stream", nil).WithContext(streamCtx)
	streamRequest.AddCookie(bCookie)
	streamResponse := httptest.NewRecorder()
	streamDone := make(chan struct{})
	go func() {
		app.Handler().ServeHTTP(streamResponse, streamRequest)
		close(streamDone)
	}()
	time.Sleep(50 * time.Millisecond)
	if code := postTelegram(app, 11, 99, "/web"); code != http.StatusNoContent {
		t.Fatalf("second webhook status=%d", code)
	}
	drain(t, app)
	replies = sends()
	match = webLinkPattern.FindStringSubmatch(replies[len(replies)-1])
	if match == nil {
		t.Fatal("the second /web minted no link")
	}
	unusedToken := match[1]
	if code := postTelegram(app, 12, 99, "remember the oak"); code != http.StatusNoContent {
		t.Fatalf("queued message status=%d", code)
	}
	modelCalls := len(models())

	// 8. A removes B. YAML membership, sessions, links, the stream, and the
	// queued event all have to stop working.
	if response := apiCall(t, app, aCookie, aCSRF, http.MethodDelete, "/api/config/accounts/third", ""); response.Code != http.StatusOK {
		t.Fatalf("remove third: status=%d body=%s", response.Code, response.Body.String())
	}
	select {
	case <-streamDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("third's stream survived removal")
	}
	drainLenient(app)
	if len(models()) != modelCalls {
		t.Fatal("the queued message of a removed account reached the model")
	}
	third := ports.WithPrincipal(ctx, ports.Principal{AccountID: "third"})
	if recent, _ := app.database.RecentMessages(third, "telegram", 10); len(recent) != 0 {
		t.Fatalf("the removed account's queued message became history: %+v", recent)
	}
	if cookie := loginThrough(t, app, "third", "third-password-long"); cookie != nil {
		t.Fatal("third's password still signs in after removal")
	}
	for name, cookie := range map[string]*http.Cookie{"password session": bCookie, "link session": linkCookie} {
		response := apiCall(t, app, cookie, "", http.MethodGet, "/api/session", "")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s survived removal: status=%d", name, response.Code)
		}
	}
	request = httptest.NewRequest(http.MethodPost, "/api/login/link", strings.NewReader(`{"token":"`+unusedToken+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://eggy.example")
	request.Header.Set("X-Eggy-Login", "1")
	response = httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || len(response.Result().Cookies()) != 0 {
		t.Fatalf("unused link after removal: status=%d cookies=%d", response.Code, len(response.Result().Cookies()))
	}
	// Recreating the ID inherits nothing: the retired row refuses.
	if response := apiCall(t, app, aCookie, aCSRF, http.MethodPost, "/api/config/accounts", `{"id":"THIRD","password":"another-password-long"}`); response.Code == http.StatusOK {
		t.Fatal("the removed username was reissued")
	}
	// A session is untouched, and no secret reached the log.
	if id, _ := sessionOf(t, app, aCookie); id != "nigel" {
		t.Fatal("nigel's session was lost")
	}
	if strings.Contains(logs.String(), "provider-secret") || strings.Contains(logs.String(), "third-password-long") {
		t.Fatal("a secret reached the application log")
	}
}

// quoteJSON quotes text the way the send handler's JSON body needs.
func quoteJSON(text string) string {
	encoded, _ := json.Marshal(text)
	return string(encoded)
}
