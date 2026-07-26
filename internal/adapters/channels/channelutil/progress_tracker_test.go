package channelutil

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// recordingChannel stands in for whatever channel a run's progress is routed
// to. It records the destination carried on each delivering ctx, so these
// tests can assert that a run reports back to the surface that started it
// rather than to a fixed default.
type recordingChannel struct {
	editFails    bool
	sent         []string
	edited       []string
	destinations []approvals.Destination
	nextID       int
}

func (c *recordingChannel) record(ctx context.Context) {
	c.destinations = append(c.destinations, approvals.DestinationFromContext(ctx))
}

func (c *recordingChannel) DeliverTrackable(ctx context.Context, _, text string) (string, error) {
	c.record(ctx)
	c.sent = append(c.sent, text)
	c.nextID++
	return string(rune('0' + c.nextID)), nil
}

func (c *recordingChannel) EditText(ctx context.Context, _, _, text string) error {
	c.record(ctx)
	if c.editFails {
		return errors.New("message to edit not found")
	}
	c.edited = append(c.edited, text)
	return nil
}

func (c *recordingChannel) Deliver(context.Context, string, string) error { return nil }
func (c *recordingChannel) DeliverApproval(context.Context, string, approvals.Approval) error {
	return nil
}
func (c *recordingChannel) AnswerCallback(context.Context, string) error { return nil }
func (c *recordingChannel) SendTyping(context.Context, string) error     { return nil }

func TestProgressTrackerDeliversOnTheTurnsDestination(t *testing.T) {
	channel := &recordingChannel{}
	tracker := NewProgressTracker(channel, "42")
	ctx := approvals.WithDestination(context.Background(), approvals.Destination{Kind: approvals.DestinationWeb, ThreadID: "thread-1"})
	tracker.Deliver(ctx, ports.CodingProgress{RunID: "run-1", Kind: "started", Message: "run started"})
	tracker.Deliver(ctx, ports.CodingProgress{RunID: "run-1", Kind: "command", Message: "go test ./..."})
	if len(channel.destinations) != 2 {
		t.Fatalf("destinations=%v", channel.destinations)
	}
	for _, destination := range channel.destinations {
		if destination.Kind != approvals.DestinationWeb || destination.ThreadID != "thread-1" {
			t.Fatalf("progress escaped the turn's destination: %+v", destination)
		}
	}
}

func TestProgressTrackerKeepsAConciseTimelineForEachRun(t *testing.T) {
	channel := &recordingChannel{}
	tracker := NewProgressTracker(channel, "42")
	tracker.Deliver(context.Background(), ports.CodingProgress{RunID: "run-1", Kind: "started", Message: "Codex run started"})
	tracker.Deliver(context.Background(), ports.CodingProgress{RunID: "run-1", Kind: "command", Message: "go test ./..."})
	if len(channel.sent) != 1 || len(channel.edited) != 1 {
		t.Fatalf("sent=%v edited=%v", channel.sent, channel.edited)
	}
	if !strings.Contains(channel.edited[0], "Codex run started") || !strings.Contains(channel.edited[0], "go test ./...") {
		t.Fatalf("edited=%v", channel.edited)
	}
}

func TestProgressTrackerClearsTrackingOnTerminalKind(t *testing.T) {
	channel := &recordingChannel{}
	tracker := NewProgressTracker(channel, "42")
	tracker.Deliver(context.Background(), ports.CodingProgress{RunID: "run-1", Kind: "started", Message: "started"})
	tracker.Deliver(context.Background(), ports.CodingProgress{RunID: "run-1", Kind: "completed", Message: "done"})
	tracker.Deliver(context.Background(), ports.CodingProgress{RunID: "run-1", Kind: "started", Message: "started again"})
	if len(channel.sent) != 2 {
		t.Fatalf("expected a fresh message after the terminal event, got %v", channel.sent)
	}
	if len(channel.edited) != 1 || !strings.Contains(channel.edited[0], "started") || !strings.Contains(channel.edited[0], "done") {
		t.Fatalf("expected the terminal event to edit the existing message before clearing tracking, got %v", channel.edited)
	}
}

func TestProgressTrackerTracksSeparateRunsIndependently(t *testing.T) {
	channel := &recordingChannel{}
	tracker := NewProgressTracker(channel, "42")
	tracker.Deliver(context.Background(), ports.CodingProgress{RunID: "run-1", Kind: "started", Message: "run one started"})
	tracker.Deliver(context.Background(), ports.CodingProgress{RunID: "run-2", Kind: "started", Message: "run two started"})
	tracker.Deliver(context.Background(), ports.CodingProgress{RunID: "run-1", Kind: "command", Message: "run one step"})
	if len(channel.sent) != 2 || len(channel.edited) != 1 {
		t.Fatalf("sent=%v edited=%v", channel.sent, channel.edited)
	}
}

func TestProgressTrackerFallsBackToNewMessageWhenEditFails(t *testing.T) {
	channel := &recordingChannel{editFails: true}
	tracker := NewProgressTracker(channel, "42")
	tracker.Deliver(context.Background(), ports.CodingProgress{RunID: "run-1", Kind: "started", Message: "started"})
	tracker.Deliver(context.Background(), ports.CodingProgress{RunID: "run-1", Kind: "command", Message: "step"})
	if len(channel.sent) != 2 {
		t.Fatalf("expected a fallback new message when editing failed, got %v", channel.sent)
	}
}

func TestProgressTrackerIgnoresEmptyMessages(t *testing.T) {
	channel := &recordingChannel{}
	tracker := NewProgressTracker(channel, "42")
	tracker.Deliver(context.Background(), ports.CodingProgress{RunID: "run-1", Kind: "diagnostic", Message: ""})
	if len(channel.sent) != 0 || len(channel.edited) != 0 {
		t.Fatalf("unexpected delivery for an empty message: sent=%v edited=%v", channel.sent, channel.edited)
	}
}
