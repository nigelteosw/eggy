package services

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/ports"
	sqlitestore "github.com/nigelteosw/eggy/plugins/store/sqlite"
)

func asAccount(id string) context.Context {
	return ports.WithPrincipal(context.Background(), ports.Principal{AccountID: id})
}

// realStateStore backs the ownership tests with the SQLite store, because
// ownership is enforced by the per-account rows there, not by a fake.
func realStateStore(t *testing.T) ports.StateStore {
	t.Helper()
	database, err := sqlitestore.Open(filepath.Join(t.TempDir(), "eggy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database.State()
}

func TestApprovalsBindTheRequestingAccountInEveryMode(t *testing.T) {
	for _, mode := range []ports.ApprovalMode{ports.ModeNormal, ports.ModeStrict, ports.ModeAuto} {
		t.Run(string(mode), func(t *testing.T) {
			service := NewApprovalService(realStateStore(t), time.Now, 30*time.Minute, mode)
			a, b := asAccount("a"), asAccount("b")
			if err := service.SetMode(a, mode); err != nil {
				t.Fatal(err)
			}
			approval, err := service.Request(a, "tool_call", map[string]string{"thread": "private-a"}, "Do a thing")
			if err != nil {
				t.Fatal(err)
			}
			if approval.AccountID != "a" {
				t.Fatalf("AccountID = %q", approval.AccountID)
			}
			// b cannot see, decide, or consume a's approval.
			if pending, _ := service.Pending(b); len(pending) != 0 {
				t.Fatalf("b sees a's pending approvals: %+v", pending)
			}
			if err := service.Decide(b, approval.ID, true); !errors.Is(err, approvals.ErrNotAuthorized) {
				t.Fatalf("cross-account decide err = %v", err)
			}
			if err := service.Authorize(b, "tool_call", map[string]string{"thread": "private-a"}, approval.ID); !errors.Is(err, approvals.ErrNotAuthorized) {
				t.Fatalf("cross-account authorize err = %v", err)
			}
			// And a's approval is still pending and usable by a.
			if err := service.Decide(a, approval.ID, true); err != nil {
				t.Fatal(err)
			}
			if err := service.Authorize(a, "tool_call", map[string]string{"thread": "private-a"}, approval.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestApprovalsAreBoundToTheIntegrationGeneration(t *testing.T) {
	generation := uint64(3)
	service := NewApprovalService(realStateStore(t), time.Now, 30*time.Minute, ports.ModeNormal)
	service.BindGeneration(func(context.Context) uint64 { return generation })
	a := asAccount("a")
	approval, err := service.Request(a, "tool_call", map[string]string{"send": "mail"}, "Send mail")
	if err != nil {
		t.Fatal(err)
	}
	if approval.IntegrationGeneration != 3 {
		t.Fatalf("IntegrationGeneration = %d", approval.IntegrationGeneration)
	}
	if err := service.Decide(a, approval.ID, true); err != nil {
		t.Fatal(err)
	}
	// The shared Google connection was replaced between approval and
	// execution: the approval was for the old identity and must not run
	// against the new one, whoever that is.
	generation = 4
	if err := service.Authorize(a, "tool_call", map[string]string{"send": "mail"}, approval.ID); !errors.Is(err, approvals.ErrStaleGeneration) {
		t.Fatalf("stale generation err = %v", err)
	}
}

func TestApprovalRequestRequiresAPrincipal(t *testing.T) {
	service := NewApprovalService(realStateStore(t), time.Now, 30*time.Minute, ports.ModeAuto)
	if _, err := service.Request(context.Background(), "tool_call", map[string]string{}, "x"); !errors.Is(err, ports.ErrNoPrincipal) {
		t.Fatalf("err = %v", err)
	}
}

func TestDispatcherRefusesRemovedAndUnknownOwners(t *testing.T) {
	store := realStateStore(t)
	accounts := map[string]bool{"a": true, "b": true}
	handled := map[string]string{}
	dispatcher := NewDispatcher(func(id string) bool { return accounts[id] }, store, map[events.Type]EventHandler{
		events.TypeMessage: func(ctx context.Context, event events.Event) error {
			principal, err := ports.PrincipalFromContext(ctx)
			if err != nil {
				return err
			}
			handled[event.ID] = principal.AccountID
			return nil
		},
	})
	ctx := context.Background()
	if err := dispatcher.Handle(ctx, events.Event{ID: "1", Type: events.TypeMessage, Owner: "a"}); err != nil {
		t.Fatal(err)
	}
	if handled["1"] != "a" {
		t.Fatalf("handler ran as %q", handled["1"])
	}
	if err := dispatcher.Handle(ctx, events.Event{ID: "2", Type: events.TypeMessage, Owner: "stranger"}); !errors.Is(err, ErrOwnerDenied) {
		t.Fatalf("unknown owner err = %v", err)
	}
	if err := dispatcher.Handle(ctx, events.Event{ID: "3", Type: events.TypeMessage, Owner: ""}); !errors.Is(err, ErrOwnerDenied) {
		t.Fatalf("empty owner err = %v", err)
	}
	delete(accounts, "b")
	if err := dispatcher.Handle(ctx, events.Event{ID: "4", Type: events.TypeMessage, Owner: "b"}); !errors.Is(err, ErrOwnerDenied) {
		t.Fatalf("removed owner err = %v", err)
	}
	if _, ran := handled["4"]; ran {
		t.Fatal("handler ran for a removed account")
	}
}

func TestActiveTurnsAreKeyedByAccountAndConversation(t *testing.T) {
	turns := NewActiveTurns()
	conversation := destination.With(context.Background(), destination.Destination{Kind: destination.Telegram})
	a := ports.WithPrincipal(conversation, ports.Principal{AccountID: "a"})
	b := ports.WithPrincipal(conversation, ports.Principal{AccountID: "b"})
	owner, err := turns.Admit(a, ports.Message{Content: "original"}, true, func(bool) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer turns.Release(owner.Context)
	// Same conversation ID, different account: b can neither steer nor
	// stop a's turn, and an unauthenticated context can do neither either.
	bOwner, err := turns.Admit(b, ports.Message{Content: "not for a"}, true, func(bool) error { return nil })
	if err != nil || !bOwner.Owner {
		t.Fatalf("b admission=%+v err=%v, want an independent owner", bOwner, err)
	}
	turns.Release(bOwner.Context)
	if turns.Stop(b) {
		t.Fatal("b stopped a's turn")
	}
	if turns.Stop(conversation) {
		t.Fatal("an unauthenticated context stopped a's turn")
	}
	if owner.Context.Err() != nil {
		t.Fatal("a's turn was cancelled")
	}
	joined, err := turns.Admit(a, ports.Message{Content: "mine"}, true, func(bool) error { return nil })
	if err != nil || joined.Owner {
		t.Fatalf("a follow-up admission=%+v err=%v, want joined", joined, err)
	}
	if pending := turns.Pending(owner.Context); len(pending) != 1 || pending[0].Content != "mine" {
		t.Fatalf("pending=%+v", pending)
	}
	if !turns.Stop(a) {
		t.Fatal("a could not stop its own turn")
	}
}
