package services

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func admitTurn(t *testing.T, turns *ActiveTurns, ctx context.Context, message ports.Message, steerable bool) TurnAdmission {
	t.Helper()
	admission, err := turns.Admit(ctx, message, steerable, func(bool) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return admission
}

func activeThread(id string) context.Context {
	return ports.WithPrincipal(webThread(id), ports.Principal{AccountID: "42"})
}

func waitForQueuedAdmissions(t *testing.T, turns *ActiveTurns, ctx context.Context, want int) {
	t.Helper()
	key, err := keyOf(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		turns.mu.Lock()
		entry := turns.entries[key]
		turns.mu.Unlock()
		if entry != nil {
			entry.mu.Lock()
			queued := len(entry.waiters)
			entry.mu.Unlock()
			if queued == want {
				return
			}
		}
		runtime.Gosched()
	}
	t.Fatalf("queued admissions never reached %d", want)
}

func TestAdmissionJoinsTheRunningTurnAndDrainsExactlyOnce(t *testing.T) {
	turns := NewActiveTurns()
	ctx := activeThread("thread-a")
	owner := admitTurn(t, turns, ctx, ports.Message{Content: "original"}, true)
	defer turns.Release(owner.Context)
	joined := admitTurn(t, turns, ctx, ports.Message{Content: "actually, skip the tests"}, true)
	if joined.Owner {
		t.Fatal("follow-up became a competing execution owner")
	}
	pending := turns.Pending(owner.Context)
	if len(pending) != 1 || pending[0].Content != "actually, skip the tests" || pending[0].Role != ports.RoleUser {
		t.Fatalf("pending=%#v", pending)
	}
	if again := turns.Pending(owner.Context); len(again) != 0 {
		t.Fatalf("pending drained twice: %#v", again)
	}
}

func TestAdmissionCarriesAnImageMessage(t *testing.T) {
	turns := NewActiveTurns()
	ctx := activeThread("thread-a")
	owner := admitTurn(t, turns, ctx, ports.Message{Content: "original"}, true)
	defer turns.Release(owner.Context)
	message := ports.Message{Parts: []ports.ContentPart{{Type: ports.ModalityImage, MediaType: "image/png", Data: []byte("png")}}}
	if joined := admitTurn(t, turns, ctx, message, true); joined.Owner {
		t.Fatal("image follow-up became a competing execution owner")
	}
	pending := turns.Pending(owner.Context)
	if len(pending) != 1 || len(pending[0].Parts) != 1 || !bytes.Equal(pending[0].Parts[0].Data, []byte("png")) {
		t.Fatalf("pending=%#v", pending)
	}
}

func TestAdmissionReleaseReportsUndrainedInputOnce(t *testing.T) {
	turns := NewActiveTurns()
	ctx := activeThread("thread-a")
	owner := admitTurn(t, turns, ctx, ports.Message{Content: "original"}, true)
	admitTurn(t, turns, ctx, ports.Message{Content: "and push it"}, true)
	undrained := turns.Release(owner.Context)
	if len(undrained) != 1 || undrained[0].Content != "and push it" {
		t.Fatalf("undrained=%#v", undrained)
	}
	if again := turns.Release(owner.Context); len(again) != 0 {
		t.Fatalf("undrained reported twice: %#v", again)
	}
}

func TestAdmissionStaleGenerationCannotDrainOrReleaseANewerOwner(t *testing.T) {
	turns := NewActiveTurns()
	ctx := activeThread("thread-a")
	first := admitTurn(t, turns, ctx, ports.Message{Content: "first"}, true)
	turns.Release(first.Context)
	second := admitTurn(t, turns, ctx, ports.Message{Content: "second"}, true)
	defer turns.Release(second.Context)
	admitTurn(t, turns, ctx, ports.Message{Content: "for second"}, true)
	if pending := turns.Pending(first.Context); len(pending) != 0 {
		t.Fatalf("stale generation drained newer input: %#v", pending)
	}
	if undrained := turns.Release(first.Context); len(undrained) != 0 {
		t.Fatalf("stale generation released newer input: %#v", undrained)
	}
	if pending := turns.Pending(second.Context); len(pending) != 1 || pending[0].Content != "for second" {
		t.Fatalf("new owner pending=%#v", pending)
	}
}

func TestAdmissionAllowsIndependentAccountsAndConversationsDuringPreparation(t *testing.T) {
	turns := NewActiveTurns()
	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan TurnAdmission, 1)
	go func() {
		admission, _ := turns.Admit(activeThread("thread-a"), ports.Message{Content: "first"}, true, func(bool) error {
			close(started)
			<-release
			return nil
		})
		firstDone <- admission
	}()
	<-started
	otherConversation := admitTurn(t, turns, activeThread("thread-b"), ports.Message{Content: "second"}, true)
	otherAccount := admitTurn(t, turns, ports.WithPrincipal(webThread("thread-a"), ports.Principal{AccountID: "other"}), ports.Message{Content: "third"}, true)
	turns.Release(otherConversation.Context)
	turns.Release(otherAccount.Context)
	close(release)
	first := <-firstDone
	turns.Release(first.Context)
}

func TestAdmissionWaitsBehindANonSteerableOwnerInOrder(t *testing.T) {
	turns := NewActiveTurns()
	ctx := activeThread("thread-a")
	owner := admitTurn(t, turns, ctx, ports.Message{Content: "scheduled"}, false)
	firstStarted := make(chan struct{})
	firstDone := make(chan TurnAdmission, 1)
	go func() {
		admission, _ := turns.Admit(ctx, ports.Message{Content: "first"}, true, func(bool) error {
			close(firstStarted)
			return nil
		})
		firstDone <- admission
	}()
	waitForQueuedAdmissions(t, turns, ctx, 1)
	secondDone := make(chan TurnAdmission, 1)
	go func() {
		admission, _ := turns.Admit(ctx, ports.Message{Content: "second"}, true, func(bool) error { return nil })
		secondDone <- admission
	}()
	waitForQueuedAdmissions(t, turns, ctx, 2)
	turns.Release(owner.Context)
	first := <-firstDone
	<-firstStarted
	turns.Release(first.Context)
	second := <-secondDone
	turns.Release(second.Context)
}

func TestAdmissionCancelledWaiterDoesNotBlockTheNextRequest(t *testing.T) {
	turns := NewActiveTurns()
	ctx := activeThread("thread-a")
	owner := admitTurn(t, turns, ctx, ports.Message{Content: "scheduled"}, false)
	waitCtx, cancel := context.WithCancel(ctx)
	waiterDone := make(chan error, 1)
	go func() {
		_, err := turns.Admit(waitCtx, ports.Message{Content: "cancelled"}, true, func(bool) error { return nil })
		waiterDone <- err
	}()
	cancel()
	if err := <-waiterDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter err=%v", err)
	}
	turns.Release(owner.Context)
	next := admitTurn(t, turns, ctx, ports.Message{Content: "next"}, true)
	turns.Release(next.Context)
}

func TestAdmissionPreparationFailureLeavesNoGhostOwner(t *testing.T) {
	turns := NewActiveTurns()
	ctx := activeThread("thread-a")
	want := errors.New("prepare failed")
	if _, err := turns.Admit(ctx, ports.Message{Content: "first"}, true, func(bool) error { return want }); !errors.Is(err, want) {
		t.Fatalf("admit err=%v", err)
	}
	if turns.Active() {
		t.Fatal("failed preparation left an active owner")
	}
	next := admitTurn(t, turns, ctx, ports.Message{Content: "next"}, true)
	turns.Release(next.Context)
}

func TestAdmissionStopKeepsTheOwnerRegisteredUntilRelease(t *testing.T) {
	turns := NewActiveTurns()
	ctx := activeThread("thread-a")
	owner := admitTurn(t, turns, ctx, ports.Message{Content: "first"}, true)
	if !turns.Stop(ctx) || owner.Context.Err() == nil {
		t.Fatal("stop did not cancel the execution owner")
	}
	if !turns.Active() {
		t.Fatal("stopping owner disappeared before its worker released")
	}
	turns.Release(owner.Context)
	if turns.Active() {
		t.Fatal("released owner remained active")
	}
}

func TestAdmissionCleansUpIdleEntries(t *testing.T) {
	turns := NewActiveTurns()
	owner := admitTurn(t, turns, activeThread("thread-a"), ports.Message{Content: "first"}, true)
	turns.Release(owner.Context)
	turns.mu.Lock()
	defer turns.mu.Unlock()
	if len(turns.entries) != 0 {
		t.Fatalf("idle entries=%d", len(turns.entries))
	}
}

func TestActiveReportsWhetherAnyTurnIsRunning(t *testing.T) {
	turns := NewActiveTurns()
	if turns.Active() {
		t.Fatal("no turn has begun")
	}
	owner := admitTurn(t, turns, asAccount("42"), ports.Message{Content: "first"}, true)
	if !turns.Active() {
		t.Fatal("an admitted owner must be reported active")
	}
	turns.Release(owner.Context)
	if turns.Active() {
		t.Fatal("a released owner must not be reported active")
	}
}
