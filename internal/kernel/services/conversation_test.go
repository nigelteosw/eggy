package services

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestConversationKeepsBoundedRecentMessagesAndCanReset(t *testing.T) {
	store := newMemoryStore()
	service := NewConversationService(store, nil, 3, time.Now, nil)
	for _, text := range []string{"one", "two", "three", "four"} {
		if err := service.Record(context.Background(), ports.Message{Role: ports.RoleUser, Content: text}, "telegram"); err != nil {
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
	if len(state.RecentMessages) != 0 {
		t.Fatalf("state=%#v", state)
	}
}

func TestConversationRecordsDurableMessageWithSourceAndInjectedClock(t *testing.T) {
	stateStore := newMemoryStore()
	memoryStore := &conversationMemoryStore{}
	now := time.Date(2026, 7, 23, 10, 11, 12, 0, time.UTC)
	service := NewConversationService(stateStore, memoryStore, 3, func() time.Time { return now }, nil)

	message := ports.Message{Role: ports.RoleUser, Content: "remember this"}
	if err := service.Record(context.Background(), message, "telegram"); err != nil {
		t.Fatal(err)
	}

	if len(memoryStore.messages) != 1 {
		t.Fatalf("durable messages=%#v", memoryStore.messages)
	}
	got := memoryStore.messages[0]
	if got.Role != message.Role || got.Content != message.Content || got.Source != "telegram" || !got.CreatedAt.Equal(now) {
		t.Fatalf("durable message=%#v", got)
	}
}

func TestConversationDurableFailureIsLoggedWithoutBlockingRecentState(t *testing.T) {
	stateStore := newMemoryStore()
	memoryStore := &conversationMemoryStore{writeErr: errors.New("disk unavailable")}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	service := NewConversationService(stateStore, memoryStore, 1, time.Now, logger)

	if err := service.Record(context.Background(), ports.Message{Role: ports.RoleUser, Content: "first"}, "telegram"); err != nil {
		t.Fatalf("Record returned durable failure: %v", err)
	}
	if err := service.Record(context.Background(), ports.Message{Role: ports.RoleAssistant, Content: "second"}, "telegram"); err != nil {
		t.Fatalf("Record returned durable failure: %v", err)
	}

	state, err := stateStore.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.RecentMessages) != 1 || state.RecentMessages[0].Content != "second" {
		t.Fatalf("recent messages=%#v", state.RecentMessages)
	}
	if !bytes.Contains(logs.Bytes(), []byte("durable conversation write failed")) || !bytes.Contains(logs.Bytes(), []byte("disk unavailable")) {
		t.Fatalf("logs=%s", logs.String())
	}
}

type conversationMemoryStore struct {
	messages []ports.StoredMessage
	writeErr error
}

func (s *conversationMemoryStore) WriteMessage(_ context.Context, message ports.StoredMessage) error {
	s.messages = append(s.messages, message)
	return s.writeErr
}

func (*conversationMemoryStore) SearchText(context.Context, string, int) ([]ports.StoredMessage, error) {
	return nil, nil
}

func (*conversationMemoryStore) SearchSimilar(context.Context, []float32, int) ([]ports.StoredMessage, error) {
	return nil, nil
}

func (*conversationMemoryStore) PendingEmbeddings(context.Context, int) ([]ports.StoredMessage, error) {
	return nil, nil
}

func (*conversationMemoryStore) SetEmbedding(context.Context, int64, []float32) error {
	return nil
}
