package panel

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/auth/session"
	"github.com/nigelteosw/eggy/internal/ports"
)

// mintLink stores a link for the account as /web would and returns the raw
// token the page would receive in the fragment.
func mintLink(t *testing.T, cfg WebUIConfig, accountID, sender string, now time.Time) string {
	t.Helper()
	raw, hash, err := session.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	record, err := cfg.Auth.AccountAuth(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Auth.CreateWebLoginLink(context.Background(), hash, ports.WebLoginLink{AccountID: accountID, SenderID: sender, Generation: record.Generation, ExpiresAt: now.Add(5 * time.Minute)}, now); err != nil {
		t.Fatal(err)
	}
	return raw
}

func redeem(handler http.Handler, token string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/login/link", strings.NewReader(`{"token":"`+token+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://eggy.example")
	request.Header.Set(loginLinkHeader, "1")
	for key, value := range headers {
		if value == "" {
			request.Header.Del(key)
		} else {
			request.Header.Set(key, value)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestWebLoginLinkIsRedeemedOnceByAnExplicitPost(t *testing.T) {
	now := time.Now().UTC()
	cfg, db, _ := accountWebConfig(t, now)
	handler := NewWebHandler("", cfg)
	token := mintLink(t, cfg, "nigel", "42", now)

	// GET, HEAD, and a legacy ?token= query consume nothing and set nothing.
	for _, target := range []string{"/auth/link", "/auth/link?token=" + token, "/api/login/link?token=" + token} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(method, target, nil))
			if len(response.Result().Cookies()) != 0 {
				t.Fatalf("%s %s issued a cookie", method, target)
			}
		}
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/auth/link", nil))
	if page.Code != http.StatusOK || page.Header().Get("Cache-Control") != "no-store" || page.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("landing page status=%d headers=%v", page.Code, page.Header())
	}
	if _, err := db.WebLoginLink(context.Background(), session.HashToken(token), now); err != nil {
		t.Fatalf("a GET consumed the link: %v", err)
	}

	response := redeem(handler, token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("redeem status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != webSessionCookie {
		t.Fatalf("cookies=%v", cookies)
	}
	if account, err := db.SessionAccount(context.Background(), session.HashToken(cookies[0].Value), now); err != nil || account != "nigel" {
		t.Fatalf("session account=%q err=%v", account, err)
	}
	if replay := redeem(handler, token, nil); replay.Code != http.StatusUnauthorized || len(replay.Result().Cookies()) != 0 {
		t.Fatalf("replay status=%d cookies=%d", replay.Code, len(replay.Result().Cookies()))
	}
}

func TestWebLoginLinkRefusals(t *testing.T) {
	now := time.Now().UTC()
	cfg, db, accounts := accountWebConfig(t, now)
	handler := NewWebHandler("", cfg)
	fresh := func() string { return mintLink(t, cfg, "nigel", "42", now) }
	forged := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("z", 32)))

	cases := []struct {
		name    string
		token   string
		headers map[string]string
		code    int
	}{
		{"missing origin", fresh(), map[string]string{"Origin": ""}, http.StatusForbidden},
		{"null origin", fresh(), map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"foreign origin", fresh(), map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"missing marker header", fresh(), map[string]string{loginLinkHeader: ""}, http.StatusForbidden},
		{"form content type", fresh(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, http.StatusBadRequest},
		{"forged token", forged, nil, http.StatusUnauthorized},
		{"malformed token", "not-a-token", nil, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := redeem(handler, tc.token, tc.headers)
			if response.Code != tc.code || len(response.Result().Cookies()) != 0 {
				t.Fatalf("status=%d want %d cookies=%d body=%s", response.Code, tc.code, len(response.Result().Cookies()), response.Body.String())
			}
		})
	}
	// Expired.
	clock := now
	cfg.Now = func() time.Time { return clock }
	handler = NewWebHandler("", cfg)
	expired := fresh()
	clock = now.Add(6 * time.Minute)
	if response := redeem(handler, expired, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("expired status=%d", response.Code)
	}
	clock = now
	// Reset generation after minting.
	stale := fresh()
	localPassword(t, db, "nigel", "nigel-password-long-enough")
	if response := redeem(handler, stale, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("stale generation status=%d", response.Code)
	}
	// Sender unlinked or reassigned after minting.
	unlinked := fresh()
	accounts.list["nigel"] = AccountRecord{ID: "nigel"}
	if response := redeem(handler, unlinked, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unlinked status=%d", response.Code)
	}
	accounts.list["nigel"] = AccountRecord{ID: "nigel", TelegramUserID: 43}
	if response := redeem(handler, fresh(), nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("reassigned status=%d", response.Code)
	}
	accounts.list["nigel"] = AccountRecord{ID: "nigel", TelegramUserID: 42}
	// Telegram switched off.
	accounts.telegram = false
	if response := redeem(handler, fresh(), nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("telegram off status=%d", response.Code)
	}
	accounts.telegram = true
	// Membership gone: no cookie, and the link is untouched for whoever
	// restores it (it is denied, not consumed, by the guard).
	gone := fresh()
	delete(accounts.list, "nigel")
	response := redeem(handler, gone, nil)
	if response.Code == http.StatusOK || len(response.Result().Cookies()) != 0 {
		t.Fatalf("removed account status=%d cookies=%d", response.Code, len(response.Result().Cookies()))
	}
	if _, err := db.WebLoginLink(context.Background(), session.HashToken(gone), now); err != nil {
		t.Fatalf("a failed membership check consumed the link: %v", err)
	}
	accounts.list["nigel"] = AccountRecord{ID: "nigel", TelegramUserID: 42}
	// Store rollback: a session hash collision rolls the consumption back.
	colliding := &collidingAuth{AccountAuthStore: db}
	cfg.Auth = colliding
	handler = NewWebHandler("", cfg)
	kept := mintLink(t, cfg, "nigel", "42", now)
	colliding.fail = true
	if response := redeem(handler, kept, nil); response.Code != http.StatusServiceUnavailable || len(response.Result().Cookies()) != 0 {
		t.Fatalf("rolled back status=%d cookies=%d", response.Code, len(response.Result().Cookies()))
	}
	colliding.fail = false
	if response := redeem(handler, kept, nil); response.Code != http.StatusOK {
		t.Fatalf("retry after rollback status=%d body=%s", response.Code, response.Body.String())
	}
}

// collidingAuth makes the redemption transaction fail after the link
// delete, standing in for a session insert that collided.
type collidingAuth struct {
	ports.AccountAuthStore
	fail bool
}

func (c *collidingAuth) RedeemWebLoginLink(ctx context.Context, linkHash, sessionHash string, expected ports.WebLoginLink, now, expiry time.Time) error {
	if c.fail {
		// Pre-insert the session hash so the real transaction collides on
		// its primary key and rolls back.
		if err := c.AccountAuthStore.CreateAuthenticatedSession(ctx, sessionHash, expected.AccountID, expected.Generation, expiry); err != nil {
			return err
		}
	}
	return c.AccountAuthStore.RedeemWebLoginLink(ctx, linkHash, sessionHash, expected, now, expiry)
}

func TestWebLoginLinkReplacesAnExistingSessionOnlyOnSuccess(t *testing.T) {
	now := time.Now().UTC()
	cfg, db, _ := accountWebConfig(t, now)
	handler := NewWebHandler("", cfg)
	partnerCookie, _ := signIn(t, handler, db, "partner", now)
	token := mintLink(t, cfg, "nigel", "42", now)
	request := httptest.NewRequest(http.MethodPost, "/api/login/link", strings.NewReader(`{"token":"`+token+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://eggy.example")
	request.Header.Set(loginLinkHeader, "1")
	request.AddCookie(partnerCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cookie := response.Result().Cookies()[0]
	if account, _ := db.SessionAccount(context.Background(), session.HashToken(cookie.Value), now); account != "nigel" {
		t.Fatalf("new session account=%q", account)
	}
	// partner's own session is not revoked globally; only this browser
	// moved on.
	if account, err := db.SessionAccount(context.Background(), session.HashToken(partnerCookie.Value), now); err != nil || account != "partner" {
		t.Fatalf("previous session account=%q err=%v", account, err)
	}
}
