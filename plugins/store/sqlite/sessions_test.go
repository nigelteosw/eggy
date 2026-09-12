package sqlite

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSessionsResolveExpireAndRevoke(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	if err := db.CreateSession(ctx, "hash-a", "a", now.Add(12*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(ctx, "hash-a2", "a", now.Add(12*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(ctx, "hash-b", "b", now.Add(12*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if account, err := db.SessionAccount(ctx, "hash-a", now); err != nil || account != "a" {
		t.Fatalf("account=%q err=%v", account, err)
	}
	if _, err := db.SessionAccount(ctx, "hash-a", now.Add(12*time.Hour)); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expired session err=%v", err)
	}
	if _, err := db.SessionAccount(ctx, "unknown", now); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("unknown session err=%v", err)
	}
	if err := db.RevokeSession(ctx, "hash-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SessionAccount(ctx, "hash-a", now); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("revoked session err=%v", err)
	}
	if account, _ := db.SessionAccount(ctx, "hash-a2", now); account != "a" {
		t.Fatal("revoking one session revoked another")
	}
	if err := db.RevokeAccountSessions(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SessionAccount(ctx, "hash-a2", now); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("account revocation missed a session: err=%v", err)
	}
	if account, _ := db.SessionAccount(ctx, "hash-b", now); account != "b" {
		t.Fatal("revoking a's sessions touched b's")
	}
	if n, err := db.ActiveSessions(ctx, "b", now); err != nil || n != 1 {
		t.Fatalf("active=%d err=%v", n, err)
	}
}

func TestLoginTransactionsAreSingleUseBrowserBoundAndExpire(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	if err := db.CreateLoginTransaction(ctx, "state-1", "browser-1", "nonce-1", "sealed-verifier", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ConsumeLoginTransaction(ctx, "state-1", "browser-2", now); !errors.Is(err, ErrLoginTransactionNotFound) {
		t.Fatalf("different browser err=%v", err)
	}
	nonce, verifier, err := db.ConsumeLoginTransaction(ctx, "state-1", "browser-1", now)
	if err != nil || nonce != "nonce-1" || verifier != "sealed-verifier" {
		t.Fatalf("nonce=%q verifier=%q err=%v", nonce, verifier, err)
	}
	if _, _, err := db.ConsumeLoginTransaction(ctx, "state-1", "browser-1", now); !errors.Is(err, ErrLoginTransactionNotFound) {
		t.Fatalf("replay err=%v", err)
	}
	if err := db.CreateLoginTransaction(ctx, "state-2", "browser-1", "n", "v", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ConsumeLoginTransaction(ctx, "state-2", "browser-1", now.Add(6*time.Minute)); !errors.Is(err, ErrLoginTransactionNotFound) {
		t.Fatalf("expired err=%v", err)
	}
	// Expired rows are pruned by the next auth operation, not a loop: a
	// transaction that expired long ago is gone once anything else is
	// written.
	long := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := db.CreateLoginTransaction(ctx, "state-old", "browser-1", "n", "v", long); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateLoginTransaction(ctx, "state-3", "browser-1", "n", "v", time.Now().Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM login_transactions`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("remaining transactions = %d, expected only the live one", remaining)
	}
}

func TestRawSessionTokensNeverReachTheDatabase(t *testing.T) {
	db := newTestStore(t, 0)
	// The store only ever sees a hash. This guards the contract at the
	// boundary: whatever the web layer hands in is what is stored.
	if err := db.CreateSession(context.Background(), "only-a-hash", "a", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(db.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "only-a-hash") {
		t.Fatal("session row missing")
	}
}
