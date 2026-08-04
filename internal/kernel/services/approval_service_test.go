package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// fakeStateStore is an in-memory ports.StateStore. Approvals are the only
// state these tests touch, so nothing else is modelled.
type fakeStateStore struct {
	mu    sync.Mutex
	state ports.State
}

func newFakeStateStore() *fakeStateStore {
	return &fakeStateStore{state: ports.State{Approvals: map[string]approvals.Approval{}}}
}

func (s *fakeStateStore) Load(context.Context) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}

func (s *fakeStateStore) Update(_ context.Context, _ uint64, mutate func(*ports.State) error) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.state
	if err := mutate(&next); err != nil {
		return ports.State{}, err
	}
	next.Version++
	s.state = next
	return next, nil
}

// Pending is what the panel reads. It reports expired-but-undecided approvals
// too: those still count as pending in state, so filtering them here would
// leave the owner with a count that matches no list they can see.
func TestApprovalServicePendingReportsUndecidedIncludingExpired(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	clock := now
	store := newFakeStateStore()
	service := NewApprovalService(store, func() time.Time { return clock }, 30*time.Minute, ports.ModeNormal)

	older, err := service.Request(context.Background(), "calendar.create", map[string]string{"title": "standup"}, "Book standup")
	if err != nil {
		t.Fatal(err)
	}
	clock = now.Add(time.Hour)
	newer, err := service.Request(context.Background(), "calendar.delete", map[string]string{"id": "42"}, "Delete the 3pm sync")
	if err != nil {
		t.Fatal(err)
	}

	pending, err := service.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending=%#v, want both", pending)
	}
	// Oldest first, and the first one's window has already closed.
	if pending[0].ID != older.ID || pending[1].ID != newer.ID {
		t.Fatalf("order=%#v, want oldest first", pending)
	}
	if clock.Before(pending[0].ExpiresAt) {
		t.Fatal("expected the older approval's window to have closed")
	}

	if err := service.Decide(context.Background(), newer.ID, true); err != nil {
		t.Fatal(err)
	}
	remaining, err := service.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != older.ID {
		t.Fatalf("remaining=%#v, want the decided one gone", remaining)
	}
}

// Deciding an expired approval retires it. The transition used to be thrown
// away with the error that carried it -- a store discards the whole update
// when the mutation fails -- so an expired approval stayed Pending forever
// and the "approvals waiting" count could only grow.
func TestApprovalServiceDecideRetiresAnExpiredApproval(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	clock := now
	service := NewApprovalService(newFakeStateStore(), func() time.Time { return clock }, 30*time.Minute, ports.ModeNormal)

	approval, err := service.Request(context.Background(), "calendar.delete", map[string]string{"id": "42"}, "Delete the 3pm sync")
	if err != nil {
		t.Fatal(err)
	}
	clock = now.Add(time.Hour)

	if err := service.Decide(context.Background(), approval.ID, true); !errors.Is(err, approvals.ErrExpired) {
		t.Fatalf("err=%v, want ErrExpired", err)
	}
	pending, err := service.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending=%#v, want the expired approval retired", pending)
	}
	// And it is not silently approvable on a second attempt.
	if err := service.Decide(context.Background(), approval.ID, true); !errors.Is(err, approvals.ErrNotAuthorized) {
		t.Fatalf("err=%v, want ErrNotAuthorized once retired", err)
	}
}
