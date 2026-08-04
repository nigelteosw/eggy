package approvals_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	approvalspkg "github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestApprovalBindsActionPayloadAndExpiry(t *testing.T) {
	now := time.Date(2026, 7, 19, 1, 0, 0, 0, time.UTC)
	store := newMemoryStateStore()
	service := services.NewApprovalService(store, func() time.Time { return now }, 10*time.Minute, ports.ModeNormal)
	writeAction := approvalspkg.Action("write")
	deleteAction := approvalspkg.Action("delete")
	approval, err := service.Request(context.Background(), writeAction, map[string]any{"name": "test", "body": "abc"}, "Write skill")
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := store.Load(context.Background())
	if string(stored.Approvals[approval.ID].Payload) != `{"body":"abc","name":"test"}` {
		t.Fatalf("approval payload not durably canonicalized: %s", stored.Approvals[approval.ID].Payload)
	}
	if err := service.Decide(context.Background(), approval.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := service.Authorize(context.Background(), writeAction, map[string]any{"body": "abc", "name": "test"}, approval.ID); err != nil {
		t.Fatalf("equivalent canonical payload rejected: %v", err)
	}
	if err := service.Authorize(context.Background(), deleteAction, map[string]any{"body": "abc", "name": "test"}, approval.ID); !errors.Is(err, approvalspkg.ErrNotAuthorized) {
		t.Fatalf("reused/wrong action should fail, got %v", err)
	}

	expiring, _ := service.Request(context.Background(), writeAction, map[string]any{"title": "Lunch"}, "Create Lunch")
	_ = service.Decide(context.Background(), expiring.ID, true)
	now = now.Add(11 * time.Minute)
	if err := service.Authorize(context.Background(), writeAction, map[string]any{"title": "Lunch"}, expiring.ID); !errors.Is(err, approvalspkg.ErrExpired) {
		t.Fatalf("expected expiry, got %v", err)
	}
}

func TestApprovalRejectsChangedPayload(t *testing.T) {
	store := newMemoryStateStore()
	service := services.NewApprovalService(store, time.Now, time.Hour, ports.ModeNormal)
	action := approvalspkg.Action("write")
	approval, _ := service.Request(context.Background(), action, map[string]any{"name": "test", "body": "abc"}, "Write skill")
	_ = service.Decide(context.Background(), approval.ID, true)
	if err := service.Authorize(context.Background(), action, map[string]any{"name": "test", "body": "changed"}, approval.ID); !errors.Is(err, approvalspkg.ErrPayloadMismatch) {
		t.Fatalf("expected changed payload, got %v", err)
	}
}

type memoryStateStore struct {
	mu    sync.Mutex
	state ports.State
}

func newMemoryStateStore() *memoryStateStore {
	return &memoryStateStore{state: ports.State{SchemaVersion: 1, Approvals: map[string]approvalspkg.Approval{}}}
}

func (s *memoryStateStore) Load(context.Context) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}

func (s *memoryStateStore) Update(_ context.Context, expected uint64, fn func(*ports.State) error) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Version != expected {
		return ports.State{}, errors.New("conflict")
	}
	if err := fn(&s.state); err != nil {
		return ports.State{}, err
	}
	s.state.Version++
	return s.state, nil
}
