package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestIdentityLinkClaimFinalizeAndReplay(t *testing.T) {
	store := newTestStore(t, 0)
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	hash := sha256.Sum256([]byte("raw-code-not-stored"))
	if err := store.CreateIdentityLink(context.Background(), "nigel", "discord", hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := store.db.QueryRow(`SELECT code_hash FROM identity_links WHERE account_id = ? AND connection = 'discord'`, "nigel").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(hash[:]) {
		t.Fatal("link did not store the supplied hash")
	}
	// The wrong connection finds nothing: a Discord token never redeems a
	// Telegram binding.
	if _, _, ok, err := store.ClaimIdentityLink(context.Background(), "telegram", hash, now); err != nil || ok {
		t.Fatalf("wrong-connection claim ok=%v err=%v", ok, err)
	}
	account, claim, ok, err := store.ClaimIdentityLink(context.Background(), "discord", hash, now)
	if err != nil || !ok || account != "nigel" || claim == [16]byte{} {
		t.Fatalf("claim account=%q claim=%x ok=%v err=%v", account, claim, ok, err)
	}
	if _, _, replay, err := store.ClaimIdentityLink(context.Background(), "discord", hash, now); err != nil || replay {
		t.Fatalf("claimed code replay=%v err=%v", replay, err)
	}
	if err := store.FinishIdentityLink(context.Background(), claim, true); err != nil {
		t.Fatal(err)
	}
	if _, _, replay, err := store.ClaimIdentityLink(context.Background(), "discord", hash, now); err != nil || replay {
		t.Fatalf("finalized code replay=%v err=%v", replay, err)
	}
}

func TestIdentityLinkReleaseReplacementExpiryAndDelete(t *testing.T) {
	store := newTestStore(t, 0)
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	oldHash := sha256.Sum256([]byte("old"))
	newHash := sha256.Sum256([]byte("new"))
	if err := store.CreateIdentityLink(context.Background(), "nigel", "telegram", oldHash, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateIdentityLink(context.Background(), "nigel", "telegram", newHash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := store.ClaimIdentityLink(context.Background(), "telegram", oldHash, now); ok {
		t.Fatal("replaced code remained claimable")
	}
	_, claim, ok, err := store.ClaimIdentityLink(context.Background(), "telegram", newHash, now)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if err := store.FinishIdentityLink(context.Background(), claim, false); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := store.ClaimIdentityLink(context.Background(), "telegram", newHash, now); !ok {
		t.Fatal("released code was not claimable")
	}
	expired := sha256.Sum256([]byte("expired"))
	if err := store.CreateIdentityLink(context.Background(), "partner", "discord", expired, now); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := store.ClaimIdentityLink(context.Background(), "discord", expired, now); ok {
		t.Fatal("expired code was claimable")
	}
	// One account may hold a token per connection; deleting one leaves the
	// other.
	discordHash := sha256.Sum256([]byte("nigel-discord"))
	if err := store.CreateIdentityLink(context.Background(), "nigel", "discord", discordHash, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteIdentityLinks(context.Background(), "nigel", "telegram"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := store.ClaimIdentityLink(context.Background(), "telegram", newHash, now); ok {
		t.Fatal("deleted link was claimable")
	}
	if _, _, ok, _ := store.ClaimIdentityLink(context.Background(), "discord", discordHash, now); !ok {
		t.Fatal("the other connection's token was deleted too")
	}
	if err := store.DeleteIdentityLinks(context.Background(), "nigel", ""); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := store.db.QueryRow(`SELECT count(*) FROM identity_links WHERE account_id = 'nigel' AND claim_id IS NULL`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("remaining=%d, want every unclaimed token gone", remaining)
	}
}

func TestIdentityLinkConcurrentClaimsHaveOneWinner(t *testing.T) {
	store := newTestStore(t, 0)
	now := time.Now().UTC()
	hash := sha256.Sum256([]byte("one-winner"))
	if err := store.CreateIdentityLink(context.Background(), "nigel", "discord", hash, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	winners := make(chan bool, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, ok, _ := store.ClaimIdentityLink(context.Background(), "discord", hash, now)
			winners <- ok
		}()
	}
	wg.Wait()
	close(winners)
	count := 0
	for won := range winners {
		if won {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("winners=%d", count)
	}
}

// A home from the previous version carries its pending Telegram pairings
// into the generalised table as telegram links, keeps every other private
// record, and stamps the new version. Reopening afterwards is a no-op.
func TestOpenMigratesPendingTelegramPairingsIntoIdentityLinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eggy.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	if err := store.CreateSession(context.Background(), "session-hash", "nigel", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("pending"))
	// Reconstruct the previous version's shape by hand.
	if _, err := store.db.Exec(`DROP TABLE identity_links`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TABLE telegram_pairings (account_id TEXT PRIMARY KEY, code_hash BLOB UNIQUE NOT NULL, expires_at INTEGER NOT NULL, claim_id BLOB UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO telegram_pairings (account_id, code_hash, expires_at, claim_id) VALUES (?, ?, ?, NULL)`, "nigel", hash[:], now.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE schema_meta SET value = ? WHERE key = ?`, strconv.Itoa(MachineStateVersion-1), machineStateVersionKey); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		store, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		account, _, ok, err := store.ClaimIdentityLink(context.Background(), "telegram", hash, now)
		if err != nil || !ok || account != "nigel" {
			t.Fatalf("migrated pairing account=%q ok=%v err=%v", account, ok, err)
		}
		if err := store.FinishIdentityLink(context.Background(), [16]byte{}, false); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`UPDATE identity_links SET claim_id = NULL`); err != nil {
			t.Fatal(err)
		}
		var version string
		if err := store.db.QueryRow(`SELECT value FROM schema_meta WHERE key = ?`, machineStateVersionKey).Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != strconv.Itoa(MachineStateVersion) {
			t.Fatalf("version=%s", version)
		}
		if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE name = 'telegram_pairings'`).Scan(new(string)); err != sql.ErrNoRows {
			t.Fatalf("old table still present: %v", err)
		}
		if account, err := store.SessionAccount(context.Background(), "session-hash", now); err != nil || account != "nigel" {
			t.Fatalf("session lost across migration: account=%q err=%v", account, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
