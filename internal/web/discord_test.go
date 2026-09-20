package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/plugins/auth/connections"
	"github.com/nigelteosw/eggy/plugins/auth/grants"
)

// The Discord card saves the bot token from the panel into the sealed
// credential store -- never into config.yaml or the environment -- and
// refuses to enable the bot with no token anywhere.
func TestDiscordCardStoresTheBotTokenSealedAndGatesEnabling(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	cfg, database, _ := accountWebConfig(t, now)
	sealer, err := grants.NewSealer("connections", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	credentials := connections.New(database.Auth(), sealer)
	cfg.Connections = credentials
	cfg.Getenv = func(string) string { return "" }
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(accountConfigYAML()), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewWebHandler(path, cfg)
	cookie, csrf := signIn(t, handler, database, "nigel", now)
	do := func(method, target, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		attachSession(request, cookie)
		request.Header.Set(csrfHeader, csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	view := func() discordView {
		response := do(http.MethodGet, "/api/config/discord", "")
		if response.Code != http.StatusOK {
			t.Fatalf("get status=%d body=%s", response.Code, response.Body.String())
		}
		var view discordView
		if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	if v := view(); v.Enabled || v.BotTokenSet {
		t.Fatalf("initial view=%+v", v)
	}
	if response := do(http.MethodPost, "/api/config/discord", `{"enabled":true,"application_id":"4242"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("enabling without a token: status=%d body=%s", response.Code, response.Body.String())
	}
	if response := do(http.MethodPost, "/api/config/discord", `{"enabled":true,"application_id":"4242","bot_token":" bot-secret "}`); response.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", response.Code, response.Body.String())
	}
	if v := view(); !v.Enabled || !v.BotTokenSet || v.BotTokenSource != "stored" || v.ApplicationID != "4242" {
		t.Fatalf("view=%+v", v)
	}
	stored, err := credentials.Read(DiscordConnection)
	if err != nil || stored["bot_token"] != "bot-secret" {
		t.Fatalf("stored=%v err=%v", stored, err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "bot-secret") {
		t.Fatal("the token reached config.yaml")
	}
	if strings.Contains(do(http.MethodGet, "/api/config/discord", "").Body.String(), "bot-secret") {
		t.Fatal("the token is echoed back to the panel")
	}
	// Re-saving with a blank token keeps the stored one.
	if response := do(http.MethodPost, "/api/config/discord", `{"enabled":true,"application_id":"4242","bot_token":""}`); response.Code != http.StatusOK {
		t.Fatalf("resave status=%d body=%s", response.Code, response.Body.String())
	}
	if stored, _ := credentials.Read(DiscordConnection); stored["bot_token"] != "bot-secret" {
		t.Fatal("a blank resave erased the token")
	}
	loaded, err := config.LoadDocument(path)
	if err != nil || !loaded.DiscordEnabled() {
		t.Fatalf("config=%+v err=%v", loaded.Discord, err)
	}
	if response := do(http.MethodDelete, "/api/config/discord/token", ""); response.Code != http.StatusOK {
		t.Fatalf("clear status=%d body=%s", response.Code, response.Body.String())
	}
	if v := view(); v.BotTokenSet {
		t.Fatalf("view after clear=%+v", v)
	}
	// An environment override counts as a token, and is reported as such.
	cfg.Getenv = func(key string) string {
		if key == config.DiscordBotTokenEnv {
			return "env-token"
		}
		return ""
	}
	handler = NewWebHandler(path, cfg)
	if v := view(); !v.BotTokenSet || v.BotTokenSource != "environment" {
		t.Fatalf("view with env=%+v", v)
	}
}

func TestDiscordCardWithoutAnEncryptionKeyExplainsInsteadOfStoring(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	cfg, database, _ := accountWebConfig(t, now)
	cfg.Getenv = func(string) string { return "" }
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(accountConfigYAML()), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewWebHandler(path, cfg)
	cookie, csrf := signIn(t, handler, database, "nigel", now)
	request := httptest.NewRequest(http.MethodPost, "/api/config/discord", strings.NewReader(`{"enabled":true,"bot_token":"t"}`))
	attachSession(request, cookie)
	request.Header.Set(csrfHeader, csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "EGGY_ENCRYPTION_KEY") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
