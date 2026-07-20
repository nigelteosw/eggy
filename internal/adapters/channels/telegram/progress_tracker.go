package telegram

import (
	"context"
	"strings"
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
	active map[string]trackedProgress
}

type trackedProgress struct {
	messageID string
	entries   []string
}

func NewProgressTracker(channel ports.Channel, owner string) *ProgressTracker {
	return &ProgressTracker{channel: channel, owner: owner, active: map[string]trackedProgress{}}
}

func (t *ProgressTracker) Deliver(progress ports.CodingProgress) {
	if progress.Message == "" {
		return
	}
	ctx := context.Background()
	t.mu.Lock()
	tracked, exists := t.active[progress.RunID]
	if exists {
		tracked.entries = appendTimelineEntry(tracked.entries, progress.Message)
		t.active[progress.RunID] = tracked
	}
	t.mu.Unlock()
	if exists && t.channel.EditText(ctx, t.owner, tracked.messageID, renderTimeline(progress.RunID, tracked.entries)) == nil {
		t.clearIfTerminal(progress)
		return
	}
	entries := []string{progress.Message}
	if exists {
		entries = tracked.entries
	}
	if id, err := t.channel.DeliverTrackable(ctx, t.owner, renderTimeline(progress.RunID, entries)); err == nil && id != "" {
		t.mu.Lock()
		t.active[progress.RunID] = trackedProgress{messageID: id, entries: entries}
		t.mu.Unlock()
	}
	t.clearIfTerminal(progress)
}

const maxTimelineEntries = 8

func appendTimelineEntry(entries []string, message string) []string {
	message = strings.TrimSpace(message)
	if message == "" || (len(entries) > 0 && entries[len(entries)-1] == message) {
		return entries
	}
	entries = append(entries, message)
	if len(entries) > maxTimelineEntries {
		entries = entries[len(entries)-maxTimelineEntries:]
	}
	return entries
}

func renderTimeline(runID string, entries []string) string {
	lines := make([]string, 0, len(entries)+1)
	lines = append(lines, "Implementation run "+runID)
	for _, entry := range entries {
		lines = append(lines, "• "+entry)
	}
	return strings.Join(lines, "\n")
}

func (t *ProgressTracker) clearIfTerminal(progress ports.CodingProgress) {
	if progress.Kind != "completed" && progress.Kind != "error" {
		return
	}
	t.mu.Lock()
	delete(t.active, progress.RunID)
	t.mu.Unlock()
}
