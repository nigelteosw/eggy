package telegram

import (
	"context"
	"sync"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ProgressTracker keeps one live Telegram message per Codex run, editing it
// in place as new progress events arrive instead of sending a message per
// step. Tracking is in-memory only: if an edit fails (e.g. the message is
// too old, or Eggy restarted and lost the mapping) it falls back to sending
// a fresh message and tracks that one going forward.
type ProgressTracker struct {
	channel ports.Channel
	owner   string

	mu     sync.Mutex
	active map[string]string
}

func NewProgressTracker(channel ports.Channel, owner string) *ProgressTracker {
	return &ProgressTracker{channel: channel, owner: owner, active: map[string]string{}}
}

func (t *ProgressTracker) Deliver(progress ports.CodingProgress) {
	if progress.Message == "" {
		return
	}
	ctx := context.Background()
	t.mu.Lock()
	messageID, tracked := t.active[progress.RunID]
	t.mu.Unlock()
	if tracked && t.channel.EditText(ctx, t.owner, messageID, progress.Message) == nil {
		t.clearIfTerminal(progress)
		return
	}
	if id, err := t.channel.DeliverTrackable(ctx, t.owner, progress.Message); err == nil && id != "" {
		t.mu.Lock()
		t.active[progress.RunID] = id
		t.mu.Unlock()
	}
	t.clearIfTerminal(progress)
}

func (t *ProgressTracker) clearIfTerminal(progress ports.CodingProgress) {
	if progress.Kind != "completed" && progress.Kind != "error" {
		return
	}
	t.mu.Lock()
	delete(t.active, progress.RunID)
	t.mu.Unlock()
}
