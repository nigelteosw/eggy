package panel

import (
	"context"
	"crypto/sha256"
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

type fakeIdentityLinks struct {
	account    string
	connection string
	hash       [32]byte
	expires    time.Time
	deleted    []string
}

func (f *fakeIdentityLinks) CreateIdentityLink(_ context.Context, account, connection string, hash [32]byte, expires time.Time) error {
	f.account, f.connection, f.hash, f.expires = account, connection, hash, expires
	return nil
}
func (f *fakeIdentityLinks) ClaimIdentityLink(context.Context, string, [32]byte, time.Time) (string, [16]byte, bool, error) {
	return "", [16]byte{}, false, nil
}
func (f *fakeIdentityLinks) FinishIdentityLink(context.Context, [16]byte, bool) error { return nil }
func (f *fakeIdentityLinks) DeleteIdentityLinks(_ context.Context, account, connection string) error {
	f.deleted = append(f.deleted, account+"/"+connection)
	return nil
}

func asAccount(request *http.Request, id, self string) *http.Request {
	request.SetPathValue("id", id)
	return request.WithContext(context.WithValue(request.Context(), sessionKey{}, accountSession{account: AccountRecord{ID: self}}))
}

func TestTelegramPairingRouteReturnsCredentialURLAndStoresOnlyHash(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	store := &fakeIdentityLinks{}
	raw := [32]byte{}
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	response := httptest.NewRecorder()
	request := asAccount(httptest.NewRequest(http.MethodPost, "/api/accounts/nigel/telegram/pairing", nil), "nigel", "nigel")
	fill := func(dst []byte) error { copy(dst, raw[:]); return nil }
	telegramPairingCreateRoute(store, "eggy_bot", fill, func() time.Time { return now })(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "https://t.me/eggy_bot?start=") || strings.Contains(response.Body.String(), `"code"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if store.account != "nigel" || store.connection != TelegramConnection || store.hash != sha256.Sum256(raw[:]) || !store.expires.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("stored account=%q connection=%q hash=%x expires=%s", store.account, store.connection, store.hash, store.expires)
	}
}

func TestDiscordLinkRouteReturnsTheCommandUnderTheDiscordConnection(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	store := &fakeIdentityLinks{}
	raw := [32]byte{7}
	response := httptest.NewRecorder()
	request := asAccount(httptest.NewRequest(http.MethodPost, "/api/accounts/nigel/discord/link", nil), "nigel", "nigel")
	fill := func(dst []byte) error { copy(dst, raw[:]); return nil }
	discordLinkCreateRoute(store, "4242", fill, func() time.Time { return now })(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Command string `json:"command"`
		DMURL   string `json:"dm_url"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body.Command, "/link ") || body.DMURL != "https://discord.com/users/4242" {
		t.Fatalf("body=%+v", body)
	}
	if store.connection != DiscordConnection || store.hash != sha256.Sum256(raw[:]) {
		t.Fatalf("stored connection=%q hash=%x", store.connection, store.hash)
	}
	// Without a store the route says so instead of minting nothing.
	response = httptest.NewRecorder()
	discordLinkCreateRoute(nil, "", fill, time.Now)(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestIdentityLinkRoutesAreSelfOnly(t *testing.T) {
	store := &fakeIdentityLinks{}
	fill := func(dst []byte) error { return nil }
	for name, handler := range map[string]http.HandlerFunc{
		"telegram": telegramPairingCreateRoute(store, "eggy_bot", fill, time.Now),
		"discord":  discordLinkCreateRoute(store, "", fill, time.Now),
	} {
		response := httptest.NewRecorder()
		request := asAccount(httptest.NewRequest(http.MethodPost, "/api/accounts/partner/x", nil), "partner", "nigel")
		handler(response, request)
		if response.Code != http.StatusForbidden || store.account != "" {
			t.Fatalf("%s: status=%d account=%q", name, response.Code, store.account)
		}
	}
}

func TestUnlinkRoutesDeletePendingAndConfigBindingPerConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := strings.Replace(accountConfigYAML(), "accounts:\n", "telegram:\n  enabled: true\ndiscord:\n  enabled: true\naccounts:\n", 1)
	body = strings.Replace(body, "    telegram_user_id: 42\n", "    telegram_user_id: 42\n    discord_user_id: \"99\"\n", 1)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &fakeIdentityLinks{}
	response := httptest.NewRecorder()
	telegramUnlinkRoute(path, store)(response, asAccount(httptest.NewRequest(http.MethodDelete, "/api/accounts/nigel/telegram", nil), "nigel", "nigel"))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cfg, err := config.LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if account, _ := cfg.Account("nigel"); account.TelegramUserID != 0 || account.DiscordUserID != "99" {
		t.Fatalf("unlinking Telegram touched Discord: %#v", account)
	}
	response = httptest.NewRecorder()
	discordUnlinkRoute(path, store)(response, asAccount(httptest.NewRequest(http.MethodDelete, "/api/accounts/nigel/discord", nil), "nigel", "nigel"))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cfg, _ = config.LoadDocument(path)
	if account, _ := cfg.Account("nigel"); account.DiscordUserID != "" {
		t.Fatalf("account=%#v", account)
	}
	if strings.Join(store.deleted, ",") != "nigel/telegram,nigel/discord" {
		t.Fatalf("deleted=%v", store.deleted)
	}
}
