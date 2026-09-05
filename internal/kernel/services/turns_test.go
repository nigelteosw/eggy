package services

import (
	"bytes"
	"context"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestSteerCarriesAnImageMessage(t *testing.T) {
	turns := NewActiveTurns()
	ctx := webThread("thread-a")
	_, release := turns.Begin(ctx, true)
	defer release()
	message := ports.Message{Parts: []ports.ContentPart{{Type: ports.ContentTypeImage, MediaType: "image/png", Data: []byte("png")}}}

	if !turns.Steer(ctx, message) {
		t.Fatal("a steerable turn must accept an image without text")
	}
	pending := turns.Pending(ctx)
	if len(pending) != 1 || len(pending[0].Parts) != 1 || !bytes.Equal(pending[0].Parts[0].Data, []byte("png")) {
		t.Fatalf("pending=%#v", pending)
	}
}

func TestSteerJoinsTheRunningTurnAndDrainsExactlyOnce(t *testing.T) {
	turns := NewActiveTurns()
	ctx := webThread("thread-a")
	_, release := turns.Begin(ctx, true)
	defer release()

	if !turns.Steer(ctx, ports.Message{Content: "actually, skip the tests"}) {
		t.Fatal("a steerable turn must accept an owner message")
	}
	pending := turns.Pending(ctx)
	if len(pending) != 1 || pending[0].Content != "actually, skip the tests" {
		t.Fatalf("pending=%#v", pending)
	}
	if again := turns.Pending(ctx); len(again) != 0 {
		t.Fatalf("pending drained twice: %#v", again)
	}
}

// A scheduled or heartbeat turn is deliberately self-contained: folding an
// owner message into one would hand it exactly the ambient instruction that
// isolation exists to prevent.
func TestSteerIsRefusedByANonSteerableTurn(t *testing.T) {
	turns := NewActiveTurns()
	ctx := webThread("thread-a")
	_, release := turns.Begin(ctx, false)
	defer release()

	if turns.Steer(ctx, ports.Message{Content: "do this instead"}) {
		t.Fatal("a non-steerable turn must not accept an owner message")
	}
	if pending := turns.Pending(ctx); len(pending) != 0 {
		t.Fatalf("pending=%#v", pending)
	}
}

func TestSteerIsRefusedWhenNothingIsRunning(t *testing.T) {
	turns := NewActiveTurns()
	if turns.Steer(webThread("thread-a"), ports.Message{Content: "hello"}) {
		t.Fatal("steering with no active turn must report false so an ordinary turn starts")
	}
}

// Steering is scoped to its own conversation, exactly like cancellation:
// a message in one thread must never join a turn running in another.
func TestSteerIsScopedToItsOwnConversation(t *testing.T) {
	turns := NewActiveTurns()
	_, release := turns.Begin(webThread("thread-a"), true)
	defer release()

	if turns.Steer(webThread("thread-b"), ports.Message{Content: "not for you"}) {
		t.Fatal("a message must not join another conversation's turn")
	}
	if pending := turns.Pending(webThread("thread-a")); len(pending) != 0 {
		t.Fatalf("thread-a received another thread's message: %#v", pending)
	}
}

func TestStopCancelsOnlyTheCallingConversationsTurn(t *testing.T) {
	turns := NewActiveTurns()
	first, releaseFirst := turns.Begin(webThread("thread-a"), true)
	defer releaseFirst()
	second, releaseSecond := turns.Begin(webThread("thread-b"), true)
	defer releaseSecond()

	if !turns.Stop(webThread("thread-a")) {
		t.Fatal("expected the running turn to be stopped")
	}
	if first.Err() == nil {
		t.Fatal("the stopped conversation's turn must be cancelled")
	}
	if second.Err() != nil {
		t.Fatal("an unrelated conversation's turn must keep running")
	}
	if turns.Stop(webThread("thread-a")) {
		t.Fatal("stopping twice must report that nothing was running")
	}
}

// Releasing a finished turn must not clear a newer turn's registration, or a
// slow goroutine finishing late would make the live turn unstoppable.
func TestReleasingAFinishedTurnDoesNotDeregisterANewerOne(t *testing.T) {
	turns := NewActiveTurns()
	ctx := webThread("thread-a")
	_, releaseFirst := turns.Begin(ctx, true)
	_, releaseSecond := turns.Begin(ctx, true)
	defer releaseSecond()

	releaseFirst()
	if !turns.Active() {
		t.Fatal("the newer turn must still be registered")
	}
	if !turns.Steer(ctx, ports.Message{Content: "still steerable"}) {
		t.Fatal("the newer turn must still accept steering")
	}
}

func TestActiveReportsWhetherAnyTurnIsRunning(t *testing.T) {
	turns := NewActiveTurns()
	if turns.Active() {
		t.Fatal("no turn has begun")
	}
	_, release := turns.Begin(context.Background(), true)
	if !turns.Active() {
		t.Fatal("a begun turn must be reported active")
	}
	release()
	if turns.Active() {
		t.Fatal("a released turn must not be reported active")
	}
}
