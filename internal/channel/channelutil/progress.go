package channelutil

import (
	"context"
	"errors"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ErrProgressUnsupported is returned by ShowProgress when the channel is not
// a ports.ProgressChannel, so a caller can fall back to a trackable message
// for the same status rather than lose it.
var ErrProgressUnsupported = errors.New("channel does not support progress status")

// ShowProgress draws a transient status line on a channel that has one, and
// reports ErrProgressUnsupported on a channel that does not. Unlike the
// other helpers here it does not degrade to Deliver itself: a status line
// is only worth posting as a message if the caller can keep editing that
// message, which is a decision the caller owns.
func ShowProgress(ctx context.Context, channel ports.Channel, text string) error {
	progress, ok := channel.(ports.ProgressChannel)
	if !ok {
		return ErrProgressUnsupported
	}
	return progress.ShowProgress(ctx, text)
}
