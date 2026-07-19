package telegram

import (
	"context"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// StartTyping sends Telegram's typing indicator immediately and then on
// every interval until the returned stop function is called, since
// Telegram's indicator expires a few seconds after each call. The stop
// function blocks until the background sender has fully exited.
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
