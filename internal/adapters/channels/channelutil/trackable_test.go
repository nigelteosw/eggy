package channelutil

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// plainChannel implements only ports.Channel -- no in-place edits, no typing
// indicator -- standing in for a surface that cannot honour the optional
// extensions.
type plainChannel struct {
	delivered []string
}

func (c *plainChannel) Deliver(_ context.Context, text string) error {
	c.delivered = append(c.delivered, text)
	return nil
}

func (c *plainChannel) DeliverApproval(context.Context, approvals.Approval) error { return nil }

func TestDeliverTrackableUsesTheChannelsHandleWhenItHasOne(t *testing.T) {
	channel := &recordingChannel{}

	id, err := DeliverTrackable(context.Background(), channel, "working...")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("want a message ID from a trackable channel")
	}
}

func TestDeliverTrackableStillDeliversOnAChannelWithoutEdits(t *testing.T) {
	channel := &plainChannel{}

	id, err := DeliverTrackable(context.Background(), channel, "working...")
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("id=%q, want no handle when there is nothing to edit later", id)
	}
	if len(channel.delivered) != 1 || channel.delivered[0] != "working..." {
		t.Fatalf("delivered=%#v, want the text sent as an ordinary message", channel.delivered)
	}
}

func TestEditTextReportsUnsupportedOnAChannelWithoutEdits(t *testing.T) {
	err := EditText(context.Background(), &plainChannel{}, "1", "done")
	if !errors.Is(err, ErrEditsUnsupported) {
		t.Fatalf("err=%v, want ErrEditsUnsupported", err)
	}
}

func TestDeliverOutcomeSendsAFreshMessageOnAChannelWithoutEdits(t *testing.T) {
	channel := &plainChannel{}

	if err := DeliverOutcome(context.Background(), channel, "1", "Approved action completed."); err != nil {
		t.Fatal(err)
	}
	if len(channel.delivered) != 1 || channel.delivered[0] != "Approved action completed." {
		t.Fatalf("delivered=%#v, want the outcome sent as a new message", channel.delivered)
	}
}

func TestProgressTrackerFallsBackToAMessagePerStepOnAChannelWithoutEdits(t *testing.T) {
	channel := &plainChannel{}
	tracker := NewProgressTracker(channel)

	tracker.Deliver(context.Background(), ports.CodingProgress{RunID: "run-1", Message: "cloning"})
	tracker.Deliver(context.Background(), ports.CodingProgress{RunID: "run-1", Message: "building"})

	if len(channel.delivered) != 2 {
		t.Fatalf("delivered=%#v, want both steps to still reach the user", channel.delivered)
	}
}

func TestStartTypingIsANoopOnAChannelWithoutATypingIndicator(t *testing.T) {
	var channel ports.Channel = &plainChannel{}

	stop := StartTyping(context.Background(), channel, time.Millisecond)
	stop()

	if len(channel.(*plainChannel).delivered) != 0 {
		t.Fatal("a missing typing indicator must not turn into delivered messages")
	}
}
