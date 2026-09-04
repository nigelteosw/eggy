package services

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ConversationService records and recalls one thread's live turn-context
// window. SQLite (ports.MemoryStore) is its only dependency: since it is
// already mandatory (always opened in NewApp, never a feature flag), it is
// the single source of truth for both the durable log and the live
// recent-window, scoped per conversationID -- there is no separate
// unpartitioned store that could drift from it.
type ConversationService struct {
	memory      ports.MemoryStore
	recentLimit int
	now         func() time.Time
	logger      *slog.Logger
}

func NewConversationService(memory ports.MemoryStore, recentLimit int, now func() time.Time, logger *slog.Logger) *ConversationService {
	if recentLimit <= 0 {
		recentLimit = 20
	}
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ConversationService{memory: memory, recentLimit: recentLimit, now: now, logger: logger}
}

// RecentMessages returns conversationID's live turn-context window, oldest
// first, bounded to recentLimit.
func (s *ConversationService) RecentMessages(ctx context.Context, conversationID string) ([]ports.Message, error) {
	if s.memory == nil {
		return nil, nil
	}
	stored, err := s.memory.RecentMessages(ctx, conversationID, s.recentLimit)
	if err != nil {
		return nil, err
	}
	messages := make([]ports.Message, 0, len(stored))
	for _, message := range stored {
		messages = append(messages, ports.Message{Role: message.Role, Content: message.Content})
	}
	return messages, nil
}

// Record durably persists message under conversationID. A write failure is
// logged, never returned: a flaky durable store should never fail the turn
// that produced the message.
func (s *ConversationService) Record(ctx context.Context, conversationID string, message ports.Message, source string) error {
	if s.memory == nil {
		return nil
	}
	if err := s.memory.WriteMessage(ctx, ports.StoredMessage{
		ConversationID: conversationID, Role: message.Role, Content: message.Content, Source: source, CreatedAt: s.now(),
	}); err != nil {
		s.logger.Error("durable conversation write failed", "conversation_id", conversationID, "role", message.Role, "source", source, "error", err)
	}
	return nil
}

// SessionID names the stretch of conversationID that is running now: the
// last reset, or "" for a conversation nobody has cleared. Traces carry it so
// the panel can separate one line of work from the next in a conversation
// whose ID never changes -- Telegram's.
func (s *ConversationService) SessionID(ctx context.Context, conversationID string) (string, error) {
	if s.memory == nil {
		return "", nil
	}
	clearedAt, found, err := s.memory.ConversationResetAt(ctx, conversationID)
	if err != nil || !found {
		return "", err
	}
	return strconv.FormatInt(clearedAt.UnixNano(), 10), nil
}

// Reset clears conversationID's live turn-context window without deleting
// its durable history: later RecentMessages calls for this conversation
// only see messages recorded after this point, while recall/search keeps
// finding everything.
func (s *ConversationService) Reset(ctx context.Context, conversationID string) error {
	if s.memory == nil {
		return nil
	}
	return s.memory.ResetConversation(ctx, conversationID, s.now())
}
