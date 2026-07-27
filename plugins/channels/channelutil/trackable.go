package channelutil

import (
	"context"
	"errors"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ErrEditsUnsupported is returned by EditText when the channel is not a
// ports.TrackableChannel, so a caller that keeps a live message can take the
// same fallback path it already takes when an edit is refused (e.g. the
// message is too old, or the ID was lost across a restart).
var ErrEditsUnsupported = errors.New("channel does not support editing messages")

// DeliverTrackable sends text and returns a handle for editing it later. On
// a channel without in-place edits the text is still delivered, and the
// empty message ID tells the caller there is nothing to edit.
func DeliverTrackable(ctx context.Context, channel ports.Channel, text string) (string, error) {
	trackable, ok := channel.(ports.TrackableChannel)
	if !ok {
		return "", channel.Deliver(ctx, text)
	}
	return trackable.DeliverTrackable(ctx, text)
}

// EditText rewrites a previously delivered message in place, reporting
// ErrEditsUnsupported when the channel has no such notion.
func EditText(ctx context.Context, channel ports.Channel, messageID, text string) error {
	trackable, ok := channel.(ports.TrackableChannel)
	if !ok {
		return ErrEditsUnsupported
	}
	return trackable.EditText(ctx, messageID, text)
}
