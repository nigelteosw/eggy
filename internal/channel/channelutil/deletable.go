package channelutil

import (
	"context"
	"errors"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ErrDeletesUnsupported is returned by DeleteText when the channel cannot remove
// a message, so the caller can fall back to revising the message instead.
var ErrDeletesUnsupported = errors.New("channel does not support deleting messages")

// DeleteText removes a previously delivered message, reporting
// ErrDeletesUnsupported when the channel has no way to do that.
func DeleteText(ctx context.Context, channel ports.Channel, messageID string) error {
	deletable, ok := channel.(ports.DeletableChannel)
	if !ok {
		return ErrDeletesUnsupported
	}
	return deletable.DeleteMessage(ctx, messageID)
}
