package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/nigelteosw/eggy/internal/core/approvals"
)

// progressChannel records what the presenter does to its live progress message.
type progressChannel struct {
	deleted   []string
	edited    []string
	deleteErr error
}

func (c *progressChannel) Deliver(context.Context, string) error                     { return nil }
func (c *progressChannel) DeliverApproval(context.Context, approvals.Approval) error { return nil }
func (c *progressChannel) DeliverTrackable(context.Context, string) (string, error) {
	return "progress-1", nil
}
func (c *progressChannel) EditText(_ context.Context, id, text string) error {
	c.edited = append(c.edited, id+": "+text)
	return nil
}
func (c *progressChannel) DeleteMessage(_ context.Context, id string) error {
	c.deleted = append(c.deleted, id)
	return c.deleteErr
}

// When the reply arrives the progress line is gone, not left behind as a record
// of what happened: the owner has the answer, and the steps are noise.
func TestFinishedProgressIsDeletedWhenTheChannelCanDeleteIt(t *testing.T) {
	channel := &progressChannel{}
	onToolCall, finish := turnPresenter{channel: channel}.ShowToolCalls(context.Background())
	onToolCall("web_search")
	onToolCall("current_time")
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
	channel := &progressChannel{deleteErr: errors.New("message is too old")}
	onToolCall, finish := turnPresenter{channel: channel}.ShowToolCalls(context.Background())
	onToolCall("web_search")
	finish()
	if len(channel.edited) != 1 || channel.edited[0] != "progress-1: Searching the web." {
		t.Fatalf("edits=%v: want the summary in place of the progress line", channel.edited)
	}
}
