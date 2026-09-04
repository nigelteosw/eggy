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

func TestConversationRecordAndRecentMessagesRoundTripBoundedToRecentLimit(t *testing.T) {
	memory := &conversationMemoryStore{}
	service := NewConversationService(memory, 3, time.Now, nil)
	for _, text := range []string{"one", "two", "three", "four"} {
		if err := service.Record(context.Background(), "thread-a", ports.Message{Role: ports.RoleUser, Content: text}, "telegram"); err != nil {
			t.Fatal(err)
		}
	}

	messages, err := service.RecentMessages(context.Background(), "thread-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || messages[0].Content != "two" || messages[2].Content != "four" {
		t.Fatalf("messages=%#v", messages)
	}
}

func TestConversationRecentMessagesIsScopedPerConversationID(t *testing.T) {
	memory := &conversationMemoryStore{}
	service := NewConversationService(memory, 20, time.Now, nil)
	if err := service.Record(context.Background(), "thread-a", ports.Message{Role: ports.RoleUser, Content: "for a"}, "web"); err != nil {
		t.Fatal(err)
	}
	if err := service.Record(context.Background(), "thread-b", ports.Message{Role: ports.RoleUser, Content: "for b"}, "web"); err != nil {
		t.Fatal(err)
	}

	messages, err := service.RecentMessages(context.Background(), "thread-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "for a" {
		t.Fatalf("messages=%#v, want only thread-a's message", messages)
	}
}

func TestConversationResetHidesEarlierMessagesForThatConversationOnly(t *testing.T) {
	memory := &conversationMemoryStore{}
	service := NewConversationService(memory, 20, time.Now, nil)
	if err := service.Record(context.Background(), "thread-a", ports.Message{Role: ports.RoleUser, Content: "before reset"}, "web"); err != nil {
		t.Fatal(err)
	}
	if err := service.Record(context.Background(), "thread-b", ports.Message{Role: ports.RoleUser, Content: "untouched"}, "web"); err != nil {
		t.Fatal(err)
	}
	if err := service.Reset(context.Background(), "thread-a"); err != nil {
		t.Fatal(err)
	}
	if err := service.Record(context.Background(), "thread-a", ports.Message{Role: ports.RoleUser, Content: "after reset"}, "web"); err != nil {
		t.Fatal(err)
	}

	threadA, err := service.RecentMessages(context.Background(), "thread-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(threadA) != 1 || threadA[0].Content != "after reset" {
		t.Fatalf("thread-a messages=%#v, want only the post-reset message", threadA)
	}

	threadB, err := service.RecentMessages(context.Background(), "thread-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(threadB) != 1 || threadB[0].Content != "untouched" {
		t.Fatalf("thread-b messages=%#v, want unaffected by thread-a's reset", threadB)
	}
}

func TestConversationRecordsDurableMessageWithSourceAndInjectedClock(t *testing.T) {
	memory := &conversationMemoryStore{}
	now := time.Date(2026, 7, 23, 10, 11, 12, 0, time.UTC)
	service := NewConversationService(memory, 3, func() time.Time { return now }, nil)

	message := ports.Message{Role: ports.RoleUser, Content: "remember this"}
	if err := service.Record(context.Background(), "telegram", message, "telegram"); err != nil {
		t.Fatal(err)
	}

	if len(memory.messages) != 1 {
		t.Fatalf("durable messages=%#v", memory.messages)
	}
	got := memory.messages[0]
	if got.ConversationID != "telegram" || got.Role != message.Role || got.Content != message.Content || got.Source != "telegram" || !got.CreatedAt.Equal(now) {
		t.Fatalf("durable message=%#v", got)
	}
}

func TestConversationDurableWriteFailureIsLoggedAndSwallowed(t *testing.T) {
	memory := &conversationMemoryStore{writeErr: errors.New("disk unavailable")}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	service := NewConversationService(memory, 1, time.Now, logger)

	if err := service.Record(context.Background(), "telegram", ports.Message{Role: ports.RoleUser, Content: "first"}, "telegram"); err != nil {
		t.Fatalf("Record returned durable failure: %v", err)
	}
	if len(memory.messages) != 0 {
		t.Fatalf("messages=%#v, want nothing recorded when the write fails", memory.messages)
	}
	if !bytes.Contains(logs.Bytes(), []byte("durable conversation write failed")) || !bytes.Contains(logs.Bytes(), []byte("disk unavailable")) {
		t.Fatalf("logs=%s", logs.String())
	}
}

type conversationMemoryStore struct {
	messages  []ports.StoredMessage
	clearedAt map[string]time.Time
	writeErr  error
}

func (s *conversationMemoryStore) WriteMessage(_ context.Context, message ports.StoredMessage) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	s.messages = append(s.messages, message)
	return nil
}

func (s *conversationMemoryStore) RecentMessages(_ context.Context, conversationID string, limit int) ([]ports.StoredMessage, error) {
	cutoff, hasCutoff := s.clearedAt[conversationID]
	var filtered []ports.StoredMessage
	for _, message := range s.messages {
		if message.ConversationID != conversationID {
			continue
		}
		if hasCutoff && !message.CreatedAt.After(cutoff) {
			continue
		}
		filtered = append(filtered, message)
	}
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered, nil
}

func (s *conversationMemoryStore) ResetConversation(_ context.Context, conversationID string, at time.Time) error {
	if s.clearedAt == nil {
		s.clearedAt = map[string]time.Time{}
	}
	s.clearedAt[conversationID] = at
	return nil
}

func (s *conversationMemoryStore) ConversationResetAt(_ context.Context, conversationID string) (time.Time, bool, error) {
	at, found := s.clearedAt[conversationID]
	return at, found, nil
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

// /clear is what separates one stretch of Telegram's single conversation from
// the next, so the session it names has to move when the conversation is
// cleared -- and stay put while it is not.
func TestConversationSessionIDFollowsTheLastReset(t *testing.T) {
	memory := &conversationMemoryStore{}
	cleared := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	service := NewConversationService(memory, 10, func() time.Time { return cleared }, nil)
	ctx := context.Background()

	first, err := service.SessionID(ctx, "telegram")
	if err != nil || first != "" {
		t.Fatalf("session=%q err=%v before any clear", first, err)
	}

	if err := service.Reset(ctx, "telegram"); err != nil {
		t.Fatal(err)
	}
	second, err := service.SessionID(ctx, "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("session is still %q after a clear", second)
	}
	if again, _ := service.SessionID(ctx, "telegram"); again != second {
		t.Fatalf("session moved to %q without a clear", again)
	}
	if other, _ := service.SessionID(ctx, "thread-1"); other != "" {
		t.Fatalf("clearing telegram moved thread-1's session to %q", other)
	}
}
