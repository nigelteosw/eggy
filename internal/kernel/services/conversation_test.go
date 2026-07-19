package services

import (
	"context"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestConversationKeepsBoundedRecentMessagesAndCanReset(t *testing.T) {
	store := newMemoryStore()
	service := NewConversationService(store, 3)
	for _, text := range []string{"one", "two", "three", "four"} {
		if err := service.Record(context.Background(), ports.Message{Role: ports.RoleUser, Content: text}); err != nil {
			t.Fatal(err)
		}
	}
	state, _ := store.Load(context.Background())
	if len(state.RecentMessages) != 3 || state.RecentMessages[0].Content != "two" {
		t.Fatalf("messages=%#v", state.RecentMessages)
	}
	if err := service.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, _ = store.Load(context.Background())
	if len(state.RecentMessages) != 0 || state.ConversationSummary != "" {
		t.Fatalf("state=%#v", state)
	}
}
