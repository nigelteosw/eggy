package channelutil

import (
	"context"
	"errors"
	"testing"
)

type progressChannel struct {
	plainChannel
	shown []string
}

func (c *progressChannel) ShowProgress(_ context.Context, text string) error {
	c.shown = append(c.shown, text)
	return nil
}

func TestShowProgressUsesTheChannelStatusLine(t *testing.T) {
	channel := &progressChannel{}
	if err := ShowProgress(context.Background(), channel, "Calling current_time..."); err != nil {
		t.Fatal(err)
	}
	if len(channel.shown) != 1 || channel.shown[0] != "Calling current_time..." {
		t.Fatalf("shown=%v", channel.shown)
	}
}

func TestShowProgressReportsUnsupportedWithoutDelivering(t *testing.T) {
	channel := &plainChannel{}
	err := ShowProgress(context.Background(), channel, "Calling current_time...")
	if !errors.Is(err, ErrProgressUnsupported) {
		t.Fatalf("err=%v", err)
	}
	if len(channel.delivered) != 0 {
		t.Fatalf("delivered=%v", channel.delivered)
	}
}
