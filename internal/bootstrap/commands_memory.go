package bootstrap

import (
	"context"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

func handleMemory(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.context == nil {
		return CommandResult{State: ResultInfo, Title: "Memory is not configured."}, nil
	}
	loaded, err := s.context.Load(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	detail := "Durable memory (USER.md / MEMORY.md) persists across restarts and context resets; /clear does not touch it."
	if strings.TrimSpace(loaded.Memory) == "" {
		detail += "\n\nNo durable memory yet."
	} else {
		detail += "\n\n" + loaded.Memory
	}
	return CommandResult{Title: "Durable memory", Detail: detail}, nil
}

func handleClear(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	conversationID := approvals.DestinationFromContext(ctx).ConversationID()
	if err := s.conversation.Reset(ctx, conversationID); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{
		Title:  "Cleared the disposable recent-conversation window.",
		Detail: "Durable memory (USER.md / MEMORY.md) is unchanged.",
	}, nil
}
