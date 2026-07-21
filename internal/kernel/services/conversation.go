package services

import (
	"context"

	"github.com/nigelteosw/eggy/internal/ports"
)

type ConversationService struct {
	store       ports.StateStore
	recentLimit int
}

func NewConversationService(store ports.StateStore, recentLimit int) *ConversationService {
	if recentLimit <= 0 {
		recentLimit = 20
	}
	return &ConversationService{store: store, recentLimit: recentLimit}
}

func (s *ConversationService) Record(ctx context.Context, message ports.Message) error {
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
	return err
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
