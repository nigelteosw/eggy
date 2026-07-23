package webchat

import (
	"testing"
	"time"
)

func TestHubBroadcastsToEveryRegisteredConnection(t *testing.T) {
	hub := NewHub()
	_, eventsA, unregisterA := hub.Register()
	defer unregisterA()
	_, eventsB, unregisterB := hub.Register()
	defer unregisterB()

	hub.Broadcast(Event{Kind: EventMessage, Text: "hello"})

	for _, events := range []<-chan Event{eventsA, eventsB} {
		select {
		case event := <-events:
			if event.Kind != EventMessage || event.Text != "hello" {
				t.Fatalf("event=%#v", event)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for broadcast")
		}
	}
}

func TestHubUnregisterStopsDelivery(t *testing.T) {
	hub := NewHub()
	_, events, unregister := hub.Register()
	unregister()

	hub.Broadcast(Event{Kind: EventMessage, Text: "after unregister"})

	select {
	case event, ok := <-events:
		if ok {
			t.Fatalf("expected closed channel, got event=%#v", event)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected the channel to be closed promptly after unregister")
	}
}

func TestHubBroadcastNeverBlocksOnASlowReader(t *testing.T) {
	hub := NewHub()
	_, _, unregister := hub.Register() // never read from this connection's channel
	defer unregister()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			hub.Broadcast(Event{Kind: EventMessage, Text: "spam"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked on a slow/unread connection")
	}
}

func TestHubNextMessageIDNeverContainsReservedSeparators(t *testing.T) {
	hub := NewHub()
	for i := 0; i < 100; i++ {
		id := hub.NextMessageID()
		if id == "" {
			t.Fatal("expected a non-empty ID")
		}
		for _, reserved := range []byte{':', '|'} {
			for _, b := range []byte(id) {
				if b == reserved {
					t.Fatalf("id %q contains reserved separator %q", id, reserved)
				}
			}
		}
	}
}
