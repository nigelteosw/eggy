package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

// fakeGoogle stands in for every Google endpoint an accounts deployment
// touches: the model provider is faked beside it. Only the shared outbound
// Workspace grant reaches Google now; who it answers as is the test's to
// script.
type fakeGoogle struct {
	// workspaceAs is the account the next Workspace grant belongs to.
	workspaceAs struct{ subject, email string }
	modelBodies []string
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	return &fakeGoogle{}
}

func (g *fakeGoogle) client(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Host == "deepseek.test":
			body, _ := io.ReadAll(request.Body)
			g.modelBodies = append(g.modelBodies, string(body))
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"noted"}}]}`), nil
		case request.URL.Host == "telegram.test":
			return appJSON(200, `{"ok":true,"result":{"message_id":1}}`), nil
		case request.URL.Host == "oauth2.googleapis.com" && request.URL.Path == "/token":
			return appJSON(200, `{"access_token":"workspace-access","refresh_token":"workspace-refresh","token_type":"Bearer","expires_in":3600,"scope":"https://www.googleapis.com/auth/gmail.modify openid"}`), nil
		case request.URL.Host == "openidconnect.googleapis.com":
			response, _ := json.Marshal(map[string]any{"sub": g.workspaceAs.subject, "email": g.workspaceAs.email, "email_verified": true})
			return appJSON(200, string(response)), nil
		}
		return appJSON(404, `{"error":"unexpected `+request.URL.String()+`"}`), nil
	})}
}

// loginThrough posts a username and password to app and returns the
// session cookie it lands with, or nil when the login refused.
func loginThrough(t *testing.T, app *App, username, password string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(string(body)))
	request.RemoteAddr = "10.9." + strings.TrimSuffix(strings.Repeat("1.", 1), ".") + "." + username[:1] + ":1"
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	for _, c := range response.Result().Cookies() {
		if c.Name == "eggy_session" && c.Value != "" && c.MaxAge >= 0 {
			return c
		}
	}
	return nil
}

func apiCall(t *testing.T, app *App, cookie *http.Cookie, csrf, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.AddCookie(cookie)
	if csrf != "" {
		request.Header.Set("X-Eggy-CSRF", csrf)
	}
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	return response
}

func sessionOf(t *testing.T, app *App, cookie *http.Cookie) (id, csrf string) {
	t.Helper()
	response := apiCall(t, app, cookie, "", http.MethodGet, "/api/session", "")
	if response.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded struct {
		Account struct{ ID string }
		CSRF    string `json:"csrf"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Account.ID, decoded.CSRF
}

// The acceptance scenario, end to end with fake adapters: a fresh accounts
// deployment, two people sign in, talk privately, share one Google
// connection, cannot reach each other's records, and one is removed.
func TestAccountsEndToEnd(t *testing.T) {
	home := t.TempDir()
	google := newFakeGoogle(t)
	options := func() AppOptions {
		return AppOptions{HTTPClient: google.client(t), TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}}
	}

	// 1. A fresh deployment with two people and Eggy's own Google identity.
	cfg := accountTestConfig(home)
	cfg.MigrationOwnerID = ""
	cfg.Google = config.GoogleConfig{Enabled: true, ClientID: "desktop-client", Products: []string{"gmail"}, ExpectedEmail: "eggy@example.com"}
	secrets := accountTestSecrets()
	app, err := NewApp(cfg, secrets, options())
	if err != nil {
		t.Fatalf("fresh boot: %v", err)
	}
	nigel := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "nigel"})
	partner := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "partner"})
	// 2. Each starts with blank documents of their own.
	if err := app.context.AddEntry(nigel, ports.ContextMemory, "the oven runs hot"); err != nil {
		t.Fatal(err)
	}
	if loaded, _ := app.context.Load(partner); strings.Contains(loaded.Memory, "oven") {
		t.Fatalf("partner sees nigel's memory: %q", loaded.Memory)
	}
	if _, err := os.Stat(filepath.Join(home, "accounts", "nigel", "memories", "MEMORY.md")); err != nil {
		t.Fatalf("memory not under nigel's account: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "memories")); !os.IsNotExist(err) {
		t.Fatal("a shared memories directory was created")
	}

	// 3. nigel signs in with the environment login and gives partner a
	// local password from the People card; partner signs in with it. A
	// stranger and a wrong password cannot.
	nigelCookie := loginThrough(t, app, "owner@example.com", "operator-password")
	if nigelCookie == nil {
		t.Fatal("the environment login did not sign nigel in")
	}
	nigelID, nigelCSRF := sessionOf(t, app, nigelCookie)
	if nigelID != "nigel" {
		t.Fatalf("nigel's session resolved to %q", nigelID)
	}
	if response := apiCall(t, app, nigelCookie, nigelCSRF, http.MethodPost, "/api/config/accounts/partner/password", `{"password":"partner-password-long"}`); response.Code != http.StatusOK {
		t.Fatalf("set partner's password: status=%d body=%s", response.Code, response.Body.String())
	}
	partnerCookie := loginThrough(t, app, "partner", "partner-password-long")
	if partnerCookie == nil {
		t.Fatal("partner could not sign in with the local password")
	}
	if stranger := loginThrough(t, app, "stranger", "partner-password-long"); stranger != nil {
		t.Fatal("an unlisted person signed in")
	}
	if wrong := loginThrough(t, app, "partner", "operator-password"); wrong != nil {
		t.Fatal("the environment password signed partner in")
	}
	partnerID, partnerCSRF := sessionOf(t, app, partnerCookie)
	if partnerID != "partner" {
		t.Fatalf("partner's session resolved to %q", partnerID)
	}

	// 4. Private conversations: nigel's thread and words are nigel's only.
	created := apiCall(t, app, nigelCookie, func() string { _, csrf := sessionOf(t, app, nigelCookie); return csrf }(), http.MethodPost, "/api/chat/threads", "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create thread status=%d body=%s", created.Code, created.Body.String())
	}
	var thread struct{ ID string }
	_ = json.Unmarshal(created.Body.Bytes(), &thread)
	nigelWords, _ := json.Marshal(events.Message{Text: "nigel's private plan for the surprise party"})
	before := len(google.modelBodies)
	if err := app.HandleEvent(context.Background(), events.Event{ID: "web:1", Type: events.TypeMessage, Owner: "nigel", Destination: destinationWeb(thread.ID), Payload: nigelWords}); err != nil {
		t.Fatal(err)
	}
	if len(google.modelBodies) != before+1 {
		t.Fatalf("model calls=%d", len(google.modelBodies))
	}
	if response := apiCall(t, app, partnerCookie, partnerCSRF, http.MethodGet, "/api/chat/threads/"+thread.ID+"/history", ""); response.Code != http.StatusNotFound {
		t.Fatalf("partner read nigel's history: status=%d body=%s", response.Code, response.Body.String())
	}
	if response := apiCall(t, app, partnerCookie, partnerCSRF, http.MethodGet, "/api/chat/threads", ""); strings.Contains(response.Body.String(), thread.ID) {
		t.Fatal("partner lists nigel's thread")
	}
	// partner's own turn -- in a web thread, since partner has no Telegram
	// -- carries none of nigel's words in its prompt.
	partnerThread, err := app.database.CreateThread(partner, "thread-partner", "web", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	partnerWords, _ := json.Marshal(events.Message{Text: "what is on my calendar"})
	before = len(google.modelBodies)
	if err := app.HandleEvent(context.Background(), events.Event{ID: "web:9", Type: events.TypeMessage, Owner: "partner", Destination: destinationWeb(partnerThread.ID), Payload: partnerWords}); err != nil {
		t.Fatal(err)
	}
	for _, body := range google.modelBodies[before:] {
		if strings.Contains(body, "surprise party") || strings.Contains(body, "oven") {
			t.Fatalf("partner's prompt carried nigel's private content: %s", body)
		}
	}

	// 5. The shared Google connection: nigel connects as Eggy; partner sees
	// the same connection; a personal account is refused.
	google.workspaceAs.subject, google.workspaceAs.email = "sub-eggy", "eggy@example.com"
	reply, _, err := app.ExecuteCommand(nigel, "/google login")
	if err != nil || !strings.Contains(reply, "state=") {
		t.Fatalf("login start reply=%q err=%v", reply, err)
	}
	state := stateParam(t, reply)
	if reply, _, _ := app.ExecuteCommand(nigel, "/google login http://localhost:1/?code=4/abc&state="+state); !strings.Contains(reply, "Authorized") {
		t.Fatalf("login complete reply=%q", reply)
	}
	if reply, _, _ := app.ExecuteCommand(partner, "/google"); !strings.Contains(reply, "connected as eggy@example.com") || !strings.Contains(reply, "shared with all Eggy users") {
		t.Fatalf("partner's view of the connection=%q", reply)
	}
	google.workspaceAs.subject, google.workspaceAs.email = "sub-partner", "partner@example.com"
	reply, _, _ = app.ExecuteCommand(partner, "/google login")
	state = stateParam(t, reply)
	if reply, _, _ := app.ExecuteCommand(partner, "/google login http://localhost:1/?code=4/mine&state="+state); !strings.Contains(reply, "partner@example.com") || strings.Contains(reply, "Authorized") {
		t.Fatalf("personal account reply=%q", reply)
	}
	if reply, _, _ := app.ExecuteCommand(nigel, "/google"); !strings.Contains(reply, "connected as eggy@example.com") {
		t.Fatalf("refused connection altered the grant: %q", reply)
	}

	// 6. A stale approval: nigel approves an action, partner reconnects
	// Google as Eggy, nigel's approval is refused by generation.
	approval, err := app.approvals.Request(nigel, "tool_call", map[string]string{"send": "mail"}, "Send mail")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.approvals.Decide(partner, approval.ID, true); !errors.Is(err, approvals.ErrNotAuthorized) {
		t.Fatalf("partner decided nigel's approval: err=%v", err)
	}
	if err := app.approvals.Decide(nigel, approval.ID, true); err != nil {
		t.Fatal(err)
	}
	google.workspaceAs.subject, google.workspaceAs.email = "sub-eggy", "eggy@example.com"
	reply, _, _ = app.ExecuteCommand(partner, "/google login")
	state = stateParam(t, reply)
	if reply, _, _ := app.ExecuteCommand(partner, "/google login http://localhost:1/?code=4/again&state="+state); !strings.Contains(reply, "Authorized") {
		t.Fatalf("reconnect reply=%q", reply)
	}
	if err := app.approvals.Authorize(nigel, "tool_call", map[string]string{"send": "mail"}, approval.ID); !errors.Is(err, approvals.ErrStaleGeneration) {
		t.Fatalf("approval from before the reconnect: err=%v", err)
	}

	// 7. Remove partner and restart: the grant and nigel's history survive,
	// partner is out.
	if err := app.database.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = cfg.Accounts[:1]
	app, err = NewApp(cfg, secrets, options())
	if err != nil {
		t.Fatalf("boot after removal: %v", err)
	}
	defer app.database.Close()
	if reply, _, _ := app.ExecuteCommand(nigel, "/google"); !strings.Contains(reply, "connected as eggy@example.com") {
		t.Fatalf("grant lost across restart: %q", reply)
	}
	if recent, _ := app.database.RecentMessages(nigel, thread.ID, 10); len(recent) == 0 {
		t.Fatal("nigel's thread lost across restart")
	}
	if response := apiCall(t, app, partnerCookie, partnerCSRF, http.MethodGet, "/api/session", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("removed partner still has a session: status=%d", response.Code)
	}
	if loginThrough(t, app, "partner", "partner-password-long") != nil {
		t.Fatal("removed partner signed in again")
	}
	if response := apiCall(t, app, nigelCookie, "", http.MethodGet, "/api/session", ""); response.Code != http.StatusOK {
		t.Fatalf("nigel's session lost across restart: status=%d", response.Code)
	}
}

func stateParam(t *testing.T, reply string) string {
	t.Helper()
	for _, line := range strings.Split(reply, "\n") {
		if strings.HasPrefix(line, "https://") {
			parsed, err := url.Parse(line)
			if err != nil {
				t.Fatal(err)
			}
			return parsed.Query().Get("state")
		}
	}
	t.Fatalf("no authorization URL in %q", reply)
	return ""
}

func destinationWeb(threadID string) destination.Destination {
	return destination.Destination{Kind: destination.Web, ThreadID: threadID}
}
