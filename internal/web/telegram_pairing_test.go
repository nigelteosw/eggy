package web

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
)

type fakeTelegramPairings struct {
	account string
	hash    [32]byte
	expires time.Time
	deleted string
}

func (f *fakeTelegramPairings) CreateTelegramPairing(_ context.Context, account string, hash [32]byte, expires time.Time) error {
	f.account, f.hash, f.expires = account, hash, expires
	return nil
}
func (f *fakeTelegramPairings) ClaimTelegramPairing(context.Context, [32]byte, time.Time) (string, [16]byte, bool, error) {
	return "", [16]byte{}, false, nil
}
func (f *fakeTelegramPairings) FinishTelegramPairing(context.Context, [16]byte, bool) error {
	return nil
}
func (f *fakeTelegramPairings) DeleteTelegramPairings(_ context.Context, account string) error {
	f.deleted = account
	return nil
}

func TestTelegramPairingRouteReturnsCredentialURLAndStoresOnlyHash(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	store := &fakeTelegramPairings{}
	raw := [32]byte{}
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/accounts/nigel/telegram/pairing", nil)
	request.SetPathValue("id", "nigel")
	request = request.WithContext(context.WithValue(request.Context(), sessionKey{}, accountSession{account: AccountRecord{ID: "nigel"}}))
	fill := func(dst []byte) error { copy(dst, raw[:]); return nil }
	handler := telegramPairingCreateRoute(store, "eggy_bot", fill, func() time.Time { return now })
	handler(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "https://t.me/eggy_bot?start=") || strings.Contains(response.Body.String(), `"code"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if store.account != "nigel" || store.hash != sha256.Sum256(raw[:]) || !store.expires.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("stored account=%q hash=%x expires=%s", store.account, store.hash, store.expires)
	}
}

func TestTelegramPairingRoutesAreSelfOnly(t *testing.T) {
	store := &fakeTelegramPairings{}
	create := telegramPairingCreateRoute(store, "eggy_bot", func(dst []byte) error { return nil }, time.Now)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/accounts/partner/telegram/pairing", nil)
	request.SetPathValue("id", "partner")
	request = request.WithContext(context.WithValue(request.Context(), sessionKey{}, accountSession{account: AccountRecord{ID: "nigel"}}))
	create(response, request)
	if response.Code != http.StatusForbidden || store.account != "" {
		t.Fatalf("status=%d account=%q", response.Code, store.account)
	}
}

func TestTelegramUnlinkDeletesPendingAndConfigBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := strings.Replace(accountConfigYAML(), "accounts:\n", "telegram:\n  enabled: true\naccounts:\n", 1)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &fakeTelegramPairings{}
	handler := telegramUnlinkRoute(path, store)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/accounts/nigel/telegram", nil)
	request.SetPathValue("id", "nigel")
	request = request.WithContext(context.WithValue(request.Context(), sessionKey{}, accountSession{account: AccountRecord{ID: "nigel"}}))
	handler(response, request)
	if response.Code != http.StatusOK || store.deleted != "nigel" {
		t.Fatalf("status=%d deleted=%q body=%s", response.Code, store.deleted, response.Body.String())
	}
	cfg, err := config.LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if account, _ := cfg.Account("nigel"); account.TelegramUserID != 0 {
		t.Fatalf("account=%#v", account)
	}
}
