package services

import (
	"context"
	"log/slog"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type ConversationService struct {
	store       ports.StateStore
	memory      ports.MemoryStore
	recentLimit int
	now         func() time.Time
	logger      *slog.Logger
}

func NewConversationService(store ports.StateStore, memory ports.MemoryStore, recentLimit int, now func() time.Time, logger *slog.Logger) *ConversationService {
	if recentLimit <= 0 {
		recentLimit = 20
	}
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ConversationService{store: store, memory: memory, recentLimit: recentLimit, now: now, logger: logger}
}

func (s *ConversationService) Record(ctx context.Context, message ports.Message, source string) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		state.RecentMessages = append(state.RecentMessages, message)
		if excess := len(state.RecentMessages) - s.recentLimit; excess > 0 {
			state.RecentMessages = append([]ports.Message(nil), state.RecentMessages[excess:]...)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.memory == nil {
		return nil
	}
	if err := s.memory.WriteMessage(ctx, ports.StoredMessage{
		Role: message.Role, Content: message.Content, Source: source, CreatedAt: s.now(),
	}); err != nil {
		s.logger.Error("durable conversation write failed", "role", message.Role, "source", source, "error", err)
	}
	return nil
}

func (s *ConversationService) Reset(ctx context.Context) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		state.RecentMessages = nil
		return nil
	})
	return err
}
