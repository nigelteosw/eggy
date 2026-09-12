package google

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// identityServer stands in for Google's userinfo endpoint. It answers with
// whoever the test says the access token belongs to, and records whether it
// was asked at all.
type identityServer struct {
	email    string
	subject  string
	verified bool
	asked    int
	bearer   string
}

func (s *identityServer) start(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.asked++
		s.bearer = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": s.subject, "email": s.email, "email_verified": s.verified})
	}))
	previous := identityEndpoint
	identityEndpoint = server.URL
	t.Cleanup(func() { identityEndpoint = previous; server.Close() })
}

func expectingAuth(t *testing.T, store *TokenStore, expected string) *Auth {
	t.Helper()
	return NewAuth(Config{ClientID: "eggy.apps.googleusercontent.com", ClientSecret: "secret", ExpectedEmail: expected,
		Scopes: []string{"https://www.googleapis.com/auth/calendar"}}, store, http.DefaultClient, time.Now)
}

func login(t *testing.T, auth *Auth) error {
	t.Helper()
	authorizationURL, err := auth.BeginLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorizationURL)
	return auth.CompleteLogin(context.Background(), "4/code", parsed.Query().Get("state"))
}

func TestConnectingTheExpectedIdentityStoresItAndBumpsTheGeneration(t *testing.T) {
	(&authServer{refreshToken: "refresh-token"}).start(t)
	identity := &identityServer{email: "Eggy@Example.com", subject: "sub-eggy", verified: true}
	identity.start(t)
	store := testStore(t)
	auth := expectingAuth(t, store, "eggy@example.com")

	authorizationURL, _ := auth.BeginLogin(context.Background())
	parsed, _ := url.Parse(authorizationURL)
	scope := parsed.Query().Get("scope")
	if !strings.Contains(scope, "openid") || !strings.Contains(scope, "userinfo.email") || !strings.Contains(scope, "calendar") {
		t.Fatalf("scope=%q must add identity scopes to the configured product scopes", scope)
	}
	if err := auth.CompleteLogin(context.Background(), "4/code", parsed.Query().Get("state")); err != nil {
		t.Fatal(err)
	}
	if identity.asked != 1 || identity.bearer != "Bearer access-token" {
		t.Fatalf("identity endpoint asked=%d bearer=%q", identity.asked, identity.bearer)
	}
	record, err := store.Load()
	if err != nil || record.Email != "eggy@example.com" || record.Subject != "sub-eggy" || record.Generation != 1 {
		t.Fatalf("record=%#v err=%v", record, err)
	}
	// Reconnecting replaces the grant and bumps the generation again.
	if err := login(t, auth); err != nil {
		t.Fatal(err)
	}
	if record, _ := store.Load(); record.Generation != 2 {
		t.Fatalf("generation after reconnect = %d", record.Generation)
	}
	// Disconnecting bumps it too, and keeps it: a reconnect must not land
	// back on a number an outstanding approval was granted under.
	if err := auth.Logout(); err != nil {
		t.Fatal(err)
	}
	record, _ = store.Load()
	if record.Authorized() || record.Email != "" || record.Generation != 3 {
		t.Fatalf("record after logout=%#v", record)
	}
	if err := login(t, auth); err != nil {
		t.Fatal(err)
	}
	if record, _ := store.Load(); record.Generation != 4 {
		t.Fatalf("generation after reconnect = %d", record.Generation)
	}
	if got := auth.Generation(); got != 4 {
		t.Fatalf("Generation() = %d", got)
	}
}

func TestConnectingAnyOtherIdentityIsRefusedWithoutTouchingTheGrant(t *testing.T) {
	(&authServer{refreshToken: "refresh-token"}).start(t)
	identity := &identityServer{email: "eggy@example.com", subject: "sub-eggy", verified: true}
	identity.start(t)
	store := testStore(t)
	auth := expectingAuth(t, store, "eggy@example.com")
	if err := login(t, auth); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Load()

	// A user tapped through the consent screen as themselves.
	identity.email, identity.subject = "nigel@example.com", "sub-nigel"
	err := login(t, auth)
	if err == nil || !strings.Contains(err.Error(), "nigel@example.com") || !strings.Contains(err.Error(), "eggy@example.com") {
		t.Fatalf("personal account accepted: err=%v", err)
	}
	after, _ := store.Load()
	if after.RefreshToken != before.RefreshToken || after.Email != before.Email || after.Generation != before.Generation {
		t.Fatalf("refused connection altered the grant: before=%#v after=%#v", before, after)
	}
	if after.State != "" {
		t.Fatal("pending login survived the refusal")
	}
	// Nothing about the personal account was stored anywhere.
	if strings.Contains(string(mustRaw(t, store)), "nigel") {
		t.Fatal("personal identity reached the store")
	}

	// An unverified address is not an identity.
	identity.email, identity.subject, identity.verified = "eggy@example.com", "sub-eggy", false
	if err := login(t, auth); err == nil {
		t.Fatal("unverified email accepted")
	}
}

func TestAGrantWithoutIdentityMustVerifyBeforeItServes(t *testing.T) {
	(&authServer{refreshToken: "refresh-token"}).start(t)
	store := testStore(t)
	// A grant written by the previous release: tokens, no identity.
	if err := store.Save(TokenRecord{AccessToken: "access-token", RefreshToken: "refresh-token", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	identity := &identityServer{email: "nigel@example.com", subject: "sub-nigel", verified: true}
	identity.start(t)
	auth := expectingAuth(t, store, "eggy@example.com")
	if _, err := auth.Client(context.Background()); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("grant belonging to someone else served tools: err=%v", err)
	}
	// Refused, not deleted: the grant is still there for whoever decides.
	if record, _ := store.Load(); !record.Authorized() {
		t.Fatal("failed verification deleted the grant")
	}
	// The same grant, when it does belong to Eggy, verifies once and serves.
	identity.email, identity.subject = "eggy@example.com", "sub-eggy"
	if _, err := auth.Client(context.Background()); err != nil {
		t.Fatalf("verified grant refused: %v", err)
	}
	if record, _ := store.Load(); record.Email != "eggy@example.com" {
		t.Fatalf("verification not recorded: %#v", record)
	}
	asked := identity.asked
	if _, err := auth.Client(context.Background()); err != nil || identity.asked != asked {
		t.Fatalf("a verified grant was re-verified (asked=%d) err=%v", identity.asked, err)
	}
}

func TestStatusReportsTheVerifiedIdentityAndNeverTokens(t *testing.T) {
	(&authServer{refreshToken: "refresh-token"}).start(t)
	(&identityServer{email: "eggy@example.com", subject: "sub-eggy", verified: true}).start(t)
	store := testStore(t)
	auth := expectingAuth(t, store, "eggy@example.com")
	if err := login(t, auth); err != nil {
		t.Fatal(err)
	}
	status, err := auth.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Authorized || status.Email != "eggy@example.com" || status.ExpectedEmail != "eggy@example.com" || status.Generation != 1 {
		t.Fatalf("status=%#v", status)
	}
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), "access-token") || strings.Contains(string(encoded), "refresh-token") {
		t.Fatalf("status leaks tokens: %s", encoded)
	}
}

func TestLegacyDeploymentWithoutExpectedEmailStillConnects(t *testing.T) {
	(&authServer{refreshToken: "refresh-token"}).start(t)
	identity := &identityServer{email: "owner@example.com", subject: "sub-owner", verified: true}
	identity.start(t)
	store := testStore(t)
	auth := expectingAuth(t, store, "")
	if err := login(t, auth); err != nil {
		t.Fatal(err)
	}
	if record, _ := store.Load(); record.Email != "owner@example.com" {
		t.Fatalf("identity not recorded: %#v", record)
	}
	if _, err := auth.Client(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func mustRaw(t *testing.T, store *TokenStore) json.RawMessage {
	t.Helper()
	raw, err := store.records.Read(tokenSection, tokenKey)
	if err != nil {
		t.Fatal(err)
	}
	var record TokenRecord
	if err := store.sealer.Open(raw, associatedData(), &record); err != nil {
		t.Fatal(err)
	}
	plain, _ := json.Marshal(record)
	return plain
}
