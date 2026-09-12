package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
)

// accountConfigYAML is validConfig converted to two accounts.
func accountConfigYAML() string {
	return strings.Replace(validConfig(), "telegram:\n  owner_id: 42\n", `accounts:
  - id: nigel
    google_email: nigel@example.com
    telegram_user_id: 42
  - id: partner
    google_email: partner@example.com
web:
  google_login:
    client_id: web-client
    client_secret_env: EGGY_GOOGLE_LOGIN_CLIENT_SECRET
`, 1)
}

func TestAccountsCardRoutesManageTheListThroughConfig(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	cfg, database, accounts := accountWebConfig(t, now)
	cfg.Identities = database
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(accountConfigYAML()), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewWebHandler(path, cfg)
	cookie, csrf := signIn(t, handler, database, "nigel", now)
	if err := database.BindIdentity(context.Background(), "partner", "iss", "sub-partner"); err != nil {
		t.Fatal(err)
	}
	do := func(method, target, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		request.AddCookie(cookie)
		request.Header.Set(csrfHeader, csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	list := func() accountsView {
		response := do(http.MethodGet, "/api/config/accounts", "")
		if response.Code != http.StatusOK {
			t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
		}
		var view accountsView
		if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		return view
	}

	view := list()
	if !view.AccountMode || len(view.Accounts) != 2 || view.LoginClientID != "web-client" || view.LoginClientSecretEnv != "EGGY_GOOGLE_LOGIN_CLIENT_SECRET" {
		t.Fatalf("view=%+v", view)
	}
	if !view.Accounts[0].Self || !view.Accounts[0].SignedIn || view.Accounts[0].Enrolled {
		t.Fatalf("nigel row=%+v", view.Accounts[0])
	}
	if view.Accounts[1].Self || view.Accounts[1].SignedIn || !view.Accounts[1].Enrolled {
		t.Fatalf("partner row=%+v", view.Accounts[1])
	}
	if strings.Contains(do(http.MethodGet, "/api/config/accounts", "").Body.String(), "sub-partner") {
		t.Fatal("the list exposes identity subjects")
	}

	if response := do(http.MethodPost, "/api/config/accounts", `{"id":"third","email":"Third@Example.com","telegram_user_id":7}`); response.Code != http.StatusOK {
		t.Fatalf("add status=%d body=%s", response.Code, response.Body.String())
	}
	if view := list(); len(view.Accounts) != 3 || view.Accounts[2].Email != "third@example.com" {
		t.Fatalf("after add: %+v", view.Accounts)
	}
	// partner is enrolled, so the address is pinned until reset.
	if response := do(http.MethodPatch, "/api/config/accounts/partner", `{"email":"other@example.com"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("edit bound email status=%d", response.Code)
	}
	if response := do(http.MethodPatch, "/api/config/accounts/partner", `{"email":"partner@example.com","telegram_user_id":55}`); response.Code != http.StatusOK {
		t.Fatalf("edit telegram status=%d body=%s", response.Code, response.Body.String())
	}
	// Reset the binding: signed out, unenrolled, and the address may change.
	accounts.list["partner"] = AccountRecord{ID: "partner", Email: "partner@example.com"}
	if response := do(http.MethodPost, "/api/config/accounts/partner/reset-binding", ""); response.Code != http.StatusOK {
		t.Fatalf("reset status=%d body=%s", response.Code, response.Body.String())
	}
	if view := list(); view.Accounts[1].Enrolled {
		t.Fatal("still enrolled after reset")
	}
	if response := do(http.MethodPatch, "/api/config/accounts/partner", `{"email":"other@example.com"}`); response.Code != http.StatusOK {
		t.Fatalf("edit after reset status=%d body=%s", response.Code, response.Body.String())
	}
	// Your own account cannot go; someone else's can, and is signed out.
	if response := do(http.MethodDelete, "/api/config/accounts/nigel", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("remove self status=%d", response.Code)
	}
	if response := do(http.MethodDelete, "/api/config/accounts/third", ""); response.Code != http.StatusOK {
		t.Fatalf("remove status=%d body=%s", response.Code, response.Body.String())
	}
	if view := list(); len(view.Accounts) != 2 {
		t.Fatalf("after remove: %+v", view.Accounts)
	}
	// Login client and expected email.
	if response := do(http.MethodPost, "/api/config/login", `{"client_id":"new-client","client_secret_env":"NEW_ENV"}`); response.Code != http.StatusOK {
		t.Fatalf("login client status=%d body=%s", response.Code, response.Body.String())
	}
	if response := do(http.MethodPost, "/api/config/google/expected-email", `{"email":"Eggy@Example.com"}`); response.Code != http.StatusOK {
		t.Fatalf("expected email status=%d body=%s", response.Code, response.Body.String())
	}
	if view := list(); view.LoginClientID != "new-client" || view.LoginClientSecretEnv != "NEW_ENV" || view.ExpectedEmail != "eggy@example.com" {
		t.Fatalf("after settings: %+v", view)
	}
	// The written file is what the loader accepts.
	if _, _, err := config.LoadConfig(path, func(key string) string {
		return map[string]string{"DEEPSEEK_API_KEY": "k", "GITHUB_TOKEN": "g", "TELEGRAM_BOT_TOKEN": "t", "TELEGRAM_WEBHOOK_SECRET": "s", "EGGY_ENCRYPTION_KEY": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", "NEW_ENV": "secret"}[key]
	}); err != nil {
		t.Fatalf("written config does not load: %v", err)
	}
}

func TestAccountsCardConvertsALegacyDeployment(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewWebHandler(path, testWebConfig(now))
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"owner@example.com","password":"hunter2"}`)))
	cookie := login.Result().Cookies()[0]
	do := func(method, target, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	response := do(http.MethodGet, "/api/config/accounts", "")
	var view accountsView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.AccountMode || view.LegacyOwner != "42" || view.LegacyTelegramID != 42 {
		t.Fatalf("legacy view=%+v", view)
	}
	if response := do(http.MethodPost, "/api/config/accounts", `{"id":"x","email":"x@example.com"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("add in legacy mode status=%d", response.Code)
	}
	body := `{"accounts":[{"id":"nigel","email":"nigel@example.com","telegram_user_id":42},{"id":"partner","email":"partner@example.com"}],"login_client_id":"web-client","login_client_secret_env":"EGGY_GOOGLE_LOGIN_CLIENT_SECRET","migration_owner_id":"nigel"}`
	if response := do(http.MethodPost, "/api/config/accounts/convert", body); response.Code != http.StatusOK {
		t.Fatalf("convert status=%d body=%s", response.Code, response.Body.String())
	}
	cfg, err := config.LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AccountMode() || cfg.MigrationOwnerID != "nigel" || cfg.Owner.ID != "" {
		t.Fatalf("converted=%+v", cfg)
	}
}
