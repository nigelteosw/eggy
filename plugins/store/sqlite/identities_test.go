package sqlite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestIdentityBindingIsUniquePerAccountAndPerSubject(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	if err := db.BindIdentity(ctx, "nigel", "https://accounts.google.com", "sub-1"); err != nil {
		t.Fatal(err)
	}
	if err := db.BindIdentity(ctx, "nigel", "https://accounts.google.com", "sub-2"); !errors.Is(err, ErrIdentityBound) {
		t.Fatalf("second identity on one account err=%v", err)
	}
	if err := db.BindIdentity(ctx, "partner", "https://accounts.google.com", "sub-1"); !errors.Is(err, ErrIdentityBound) {
		t.Fatalf("one identity on two accounts err=%v", err)
	}
	if account, found, _ := db.AccountForIdentity(ctx, "https://accounts.google.com", "sub-1"); !found || account != "nigel" {
		t.Fatalf("account=%q found=%v", account, found)
	}
	if _, _, bound, _ := db.IdentityOf(ctx, "partner"); bound {
		t.Fatal("partner reported bound")
	}
}

func TestResetIdentityUnbindsAndRevokesSessions(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	now := time.Now()
	if err := db.BindIdentity(ctx, "nigel", "iss", "sub-1"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(ctx, "h1", "nigel", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.ResetIdentity(ctx, "nigel"); err != nil {
		t.Fatal(err)
	}
	if _, _, bound, _ := db.IdentityOf(ctx, "nigel"); bound {
		t.Fatal("still bound after reset")
	}
	if _, err := db.SessionAccount(ctx, "h1", now); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("session survived reset: err=%v", err)
	}
	// Re-enrollment works, including with a different subject.
	if err := db.BindIdentity(ctx, "nigel", "iss", "sub-9"); err != nil {
		t.Fatalf("re-enroll: %v", err)
	}
}

func TestConcurrentFirstLoginBindsExactlyOnce(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- db.BindIdentity(ctx, "nigel", "iss", "sub-"+string(rune('a'+i)))
		}(i)
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, ErrIdentityBound) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d bindings succeeded", succeeded)
	}
}
