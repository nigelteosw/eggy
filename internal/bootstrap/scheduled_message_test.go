package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/core/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

// A reminder is delivered without a model call, so the owner's next plain
// message is the only place its text can be read back. It must land in the
// conversation the owner replies in, or "yeah just submitted" has nothing to
// refer to.
func TestScheduledMessageIsRecordedInOwnerConversation(t *testing.T) {
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("key"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.Message{Text: "Reminder: submit the NYC youth forum form tonight."})
	event := events.Event{ID: "reminder-1", Type: events.TypeScheduledMessage, Owner: "42", Destination: destination.Destination{Kind: destination.Telegram}, Payload: payload}
	if err := app.HandleEvent(ownerCtx(), event); err != nil {
		t.Fatal(err)
	}
	recent, err := app.conversation.RecentMessages(ownerCtx(), destination.Destination{Kind: destination.Telegram}.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) == 0 || recent[len(recent)-1].Role != ports.RoleAssistant || recent[len(recent)-1].Content != "Reminder: submit the NYC youth forum form tonight." {
		t.Fatalf("recent conversation = %#v, want the delivered reminder as the last assistant message", recent)
	}
}
