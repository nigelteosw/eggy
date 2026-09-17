package destination

import (
	"context"
	"encoding/json"
	"testing"
)

func TestConversationIDKeepsTelegramAndWebEncodingsUnchanged(t *testing.T) {
	if got := (Destination{Kind: Telegram}).ConversationID(); got != "telegram" {
		t.Fatalf("telegram conversation=%q", got)
	}
	if got := (Destination{}).ConversationID(); got != "telegram" {
		t.Fatalf("legacy empty destination conversation=%q, want the Telegram default", got)
	}
	if got := (Destination{Kind: Web, ThreadID: "thread-1"}).ConversationID(); got != "thread-1" {
		t.Fatalf("web conversation=%q", got)
	}
}

func TestDiscordDMConversationIsKeyedByChannel(t *testing.T) {
	dest := Destination{Kind: Discord, ChannelID: "123"}
	if got := dest.ConversationID(); got != "discord:dm:123" {
		t.Fatalf("discord conversation=%q", got)
	}
	other := Destination{Kind: Discord, ChannelID: "456"}
	if other.ConversationID() == dest.ConversationID() {
		t.Fatal("two DMs must not share a conversation")
	}
}

func TestValidateRefusesDiscordWithoutAChannelAndUnknownKinds(t *testing.T) {
	for _, dest := range []Destination{{Kind: Telegram}, {}, {Kind: Web, ThreadID: "t"}, {Kind: Discord, ChannelID: "1"}} {
		if err := dest.Validate(); err != nil {
			t.Fatalf("%+v: unexpected error %v", dest, err)
		}
	}
	if err := (Destination{Kind: Discord}).Validate(); err == nil {
		t.Fatal("Discord without a channel must be refused")
	}
	if err := (Destination{Kind: "slack"}).Validate(); err == nil {
		t.Fatal("an unknown destination kind must be refused")
	}
}

// A stored approval keeps its exact DM across a JSON round trip, so the
// decision made after a restart still reaches the originating DM.
func TestDiscordDestinationRoundTripsThroughJSON(t *testing.T) {
	encoded, err := json.Marshal(Destination{Kind: Discord, ChannelID: "789"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded Destination
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != (Destination{Kind: Discord, ChannelID: "789"}) {
		t.Fatalf("decoded=%+v", decoded)
	}
	// Existing Telegram and web records decode as before.
	var legacy Destination
	if err := json.Unmarshal([]byte(`{"kind":"web","thread_id":"t1"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy != (Destination{Kind: Web, ThreadID: "t1"}) {
		t.Fatalf("legacy=%+v", legacy)
	}
}

func TestFromContextReturnsTheStampedDiscordDestination(t *testing.T) {
	ctx := With(context.Background(), Destination{Kind: Discord, ChannelID: "5"})
	if got := FromContext(ctx); got != (Destination{Kind: Discord, ChannelID: "5"}) {
		t.Fatalf("got=%+v", got)
	}
}
