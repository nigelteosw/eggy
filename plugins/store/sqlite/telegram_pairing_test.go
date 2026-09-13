package sqlite

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"
)

func TestTelegramPairingClaimFinalizeAndReplay(t *testing.T) {
	store := newTestStore(t, 0)
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	hash := sha256.Sum256([]byte("raw-code-not-stored"))
	if err := store.CreateTelegramPairing(context.Background(), "nigel", hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := store.db.QueryRow(`SELECT code_hash FROM telegram_pairings WHERE account_id = ?`, "nigel").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(hash[:]) {
		t.Fatal("pairing did not store the supplied hash")
	}
	account, claim, ok, err := store.ClaimTelegramPairing(context.Background(), hash, now)
	if err != nil || !ok || account != "nigel" || claim == [16]byte{} {
		t.Fatalf("claim account=%q claim=%x ok=%v err=%v", account, claim, ok, err)
	}
	if _, _, replay, err := store.ClaimTelegramPairing(context.Background(), hash, now); err != nil || replay {
		t.Fatalf("claimed code replay=%v err=%v", replay, err)
	}
	if err := store.FinishTelegramPairing(context.Background(), claim, true); err != nil {
		t.Fatal(err)
	}
	if _, _, replay, err := store.ClaimTelegramPairing(context.Background(), hash, now); err != nil || replay {
		t.Fatalf("finalized code replay=%v err=%v", replay, err)
	}
}

func TestTelegramPairingReleaseReplacementExpiryAndDelete(t *testing.T) {
	store := newTestStore(t, 0)
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	oldHash := sha256.Sum256([]byte("old"))
	newHash := sha256.Sum256([]byte("new"))
	if err := store.CreateTelegramPairing(context.Background(), "nigel", oldHash, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTelegramPairing(context.Background(), "nigel", newHash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := store.ClaimTelegramPairing(context.Background(), oldHash, now); ok {
		t.Fatal("replaced code remained claimable")
	}
	_, claim, ok, err := store.ClaimTelegramPairing(context.Background(), newHash, now)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if err := store.FinishTelegramPairing(context.Background(), claim, false); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := store.ClaimTelegramPairing(context.Background(), newHash, now); !ok {
		t.Fatal("released code was not claimable")
	}
	expired := sha256.Sum256([]byte("expired"))
	if err := store.CreateTelegramPairing(context.Background(), "partner", expired, now); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := store.ClaimTelegramPairing(context.Background(), expired, now); ok {
		t.Fatal("expired code was claimable")
	}
	if err := store.DeleteTelegramPairings(context.Background(), "nigel"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := store.ClaimTelegramPairing(context.Background(), newHash, now); ok {
		t.Fatal("deleted pairing was claimable")
	}
}

func TestTelegramPairingConcurrentClaimsHaveOneWinner(t *testing.T) {
	store := newTestStore(t, 0)
	now := time.Now().UTC()
	hash := sha256.Sum256([]byte("one-winner"))
	if err := store.CreateTelegramPairing(context.Background(), "nigel", hash, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	winners := make(chan bool, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, ok, _ := store.ClaimTelegramPairing(context.Background(), hash, now)
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
