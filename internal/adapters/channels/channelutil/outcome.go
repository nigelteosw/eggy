package channelutil

import (
	"context"

	"github.com/nigelteosw/eggy/internal/ports"
)

// DeliverOutcome edits the original message in place when a message ID was
// captured for it, falling back to a new message when no ID is available
// (e.g. after a restart) or the edit itself fails (e.g. the message is too
// old for the channel to edit).
func DeliverOutcome(ctx context.Context, channel ports.Channel, chatID, messageID, text string) error {
	if messageID != "" && channel.EditText(ctx, chatID, messageID, text) == nil {
		return nil
	}
	return channel.Deliver(ctx, chatID, text)
}
