package webchat

import (
	"context"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

func recv(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func TestChannelDeliverBroadcastsAMessageEventToTheGivenThread(t *testing.T) {
	hub := NewHub()
	channel := New(hub)
	_, events, unregister := hub.Register("thread-1")
	defer unregister()

	if err := channel.Deliver(context.Background(), "thread-1", "hello"); err != nil {
		t.Fatal(err)
	}
	event := recv(t, events)
	if event.Kind != EventMessage || event.Text != "hello" {
		t.Fatalf("event=%#v", event)
	}
}

func TestChannelDeliverNeverReachesADifferentThread(t *testing.T) {
	hub := NewHub()
	channel := New(hub)
	_, events, unregister := hub.Register("thread-2")
	defer unregister()

	if err := channel.Deliver(context.Background(), "thread-1", "hello"); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		t.Fatalf("expected no event for an unrelated thread, got %#v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestChannelDeliverTrackableReturnsAUsableEditableID(t *testing.T) {
	hub := NewHub()
	channel := New(hub)
	_, events, unregister := hub.Register("thread-1")
	defer unregister()

	id, err := channel.DeliverTrackable(context.Background(), "thread-1", "starting...")
	if err != nil || id == "" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	recv(t, events) // the initial message

	if err := channel.EditText(context.Background(), "thread-1", id, "done"); err != nil {
		t.Fatal(err)
	}
	event := recv(t, events)
	if event.Kind != EventEdit || event.ID != id || event.Text != "done" {
		t.Fatalf("event=%#v", event)
	}
}

func TestChannelSendTypingBroadcastsATypingEvent(t *testing.T) {
	hub := NewHub()
	channel := New(hub)
	_, events, unregister := hub.Register("thread-1")
	defer unregister()

	if err := channel.SendTyping(context.Background(), "thread-1"); err != nil {
		t.Fatal(err)
	}
	if event := recv(t, events); event.Kind != EventTyping {
		t.Fatalf("event=%#v", event)
	}
}

func TestChannelDeliverApprovalBroadcastsAnApprovalEvent(t *testing.T) {
	hub := NewHub()
	channel := New(hub)
	_, events, unregister := hub.Register("thread-1")
	defer unregister()

	approval := approvals.Approval{ID: "approval-1", Summary: "Add repository eggy"}
	if err := channel.DeliverApproval(context.Background(), "thread-1", approval); err != nil {
		t.Fatal(err)
	}
	event := recv(t, events)
	if event.Kind != EventApproval || event.Approval == nil || event.Approval.ID != "approval-1" || event.Approval.Summary != "Add repository eggy" {
		t.Fatalf("event=%#v", event)
	}
}

func TestChannelAnswerCallbackIsANoOp(t *testing.T) {
	channel := New(NewHub())
	if err := channel.AnswerCallback(context.Background(), "unused"); err != nil {
		t.Fatal(err)
	}
}
