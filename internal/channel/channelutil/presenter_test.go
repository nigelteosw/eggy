package channelutil

import (
	"context"
	"errors"
	"testing"

	"github.com/nigelteosw/eggy/internal/core/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// messageProgressChannel records what the presenter does to its live progress message.
type messageProgressChannel struct {
	deleted   []string
	edited    []string
	deleteErr error
}

func (c *messageProgressChannel) Deliver(context.Context, string) error { return nil }
func (c *messageProgressChannel) DeliverApproval(context.Context, approvals.Approval) error {
	return nil
}
func (c *messageProgressChannel) DeliverTrackable(context.Context, string) (string, error) {
	return "progress-1", nil
}
func (c *messageProgressChannel) EditText(_ context.Context, id, text string) error {
	c.edited = append(c.edited, id+": "+text)
	return nil
}
func (c *messageProgressChannel) DeleteMessage(_ context.Context, id string) error {
	c.deleted = append(c.deleted, id)
	return c.deleteErr
}

// When the reply arrives the progress line is gone, not left behind as a record
// of what happened: the owner has the answer, and the steps are noise.
func TestFinishedProgressIsDeletedWhenTheChannelCanDeleteIt(t *testing.T) {
	channel := &messageProgressChannel{}
	onToolCall, finish := Presenter{Channel: channel}.ShowToolCalls(context.Background())
	onToolCall(ports.ToolCall{Name: "web_search"})
	onToolCall(ports.ToolCall{Name: "current_time"})
	editsBeforeFinish := len(channel.edited)
	finish()
	if len(channel.deleted) != 1 || channel.deleted[0] != "progress-1" {
		t.Fatalf("deleted=%v, want the progress message", channel.deleted)
	}
	if len(channel.edited) != editsBeforeFinish {
		t.Fatalf("finishing revised the message it then deleted: %v", channel.edited[editsBeforeFinish:])
	}
}

// A channel that cannot delete, or refuses to, keeps the old summary so the
// steps are still accounted for.
func TestFinishedProgressFallsBackToASummaryWhenItCannotBeDeleted(t *testing.T) {
	channel := &messageProgressChannel{deleteErr: errors.New("message is too old")}
	onToolCall, finish := Presenter{Channel: channel}.ShowToolCalls(context.Background())
	onToolCall(ports.ToolCall{Name: "web_search"})
	finish()
	if len(channel.edited) != 1 || channel.edited[0] != "progress-1: Searching the web." {
		t.Fatalf("edits=%v: want the summary in place of the progress line", channel.edited)
	}
}
