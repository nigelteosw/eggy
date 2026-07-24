// Package channelutil holds small behaviors built only on ports.Channel:
// generic enough that no specific channel adapter should own them, but
// still adapter-shaped (they know about delivery mechanics like typing
// indicators and message edits), so they don't belong in kernel either.
package channelutil

import (
	"context"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// StartTyping sends a channel's typing indicator immediately and then on
// every interval until the returned stop function is called, since a
// typing indicator typically expires a few seconds after each call. The
// stop function blocks until the background sender has fully exited.
func StartTyping(ctx context.Context, channel ports.Channel, chatID string, interval time.Duration) func() {
	typingCtx, cancel := context.WithCancel(ctx)
	_ = channel.SendTyping(typingCtx, chatID)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				_ = channel.SendTyping(typingCtx, chatID)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
