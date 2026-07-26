package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeChannel struct {
	name              string
	delivered         []string
	trackableID       string
	trackableErr      error
	editCalls         []string
	typingCalls       int
	approvalDelivered []approvals.Approval
	deliverErr        error
}

func (f *fakeChannel) Deliver(_ context.Context, text string) error {
	if f.deliverErr != nil {
		return f.deliverErr
	}
	f.delivered = append(f.delivered, text)
	return nil
}
func (f *fakeChannel) DeliverApproval(_ context.Context, approval approvals.Approval) error {
	f.approvalDelivered = append(f.approvalDelivered, approval)
	return nil
}
func (f *fakeChannel) DeliverTrackable(_ context.Context, text string) (string, error) {
	if f.trackableErr != nil {
		return "", f.trackableErr
	}
	f.delivered = append(f.delivered, text)
	return f.trackableID, nil
}
func (f *fakeChannel) EditText(_ context.Context, messageID, text string) error {
	f.editCalls = append(f.editCalls, messageID+":"+text)
	return nil
}
func (f *fakeChannel) SendTyping(context.Context) error {
	f.typingCalls++
	return nil
}

func telegramCtx() context.Context {
	return approvals.WithDestination(context.Background(), approvals.Destination{Kind: approvals.DestinationTelegram})
}

func webCtx(threadID string) context.Context {
	return approvals.WithDestination(context.Background(), approvals.Destination{Kind: approvals.DestinationWeb, ThreadID: threadID})
}

func TestRoutedChannelDeliverReachesOnlyTheDestinationsChannel(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newRoutedChannel(telegram, web)

	if err := channel.Deliver(webCtx("thread-1"), "hello"); err != nil {
		t.Fatal(err)
	}
	if len(web.delivered) != 1 {
		t.Fatalf("web=%#v", web)
	}
	if len(telegram.delivered) != 0 {
		t.Fatalf("telegram=%#v, want untouched", telegram)
	}
}

func TestRoutedChannelDeliverReachesTelegramForATelegramDestination(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newRoutedChannel(telegram, web)

	if err := channel.Deliver(telegramCtx(), "hello"); err != nil {
		t.Fatal(err)
	}
	if len(telegram.delivered) != 1 {
		t.Fatalf("telegram=%#v", telegram)
	}
	if len(web.delivered) != 0 {
		t.Fatalf("web=%#v, want untouched", web)
	}
}

func TestRoutedChannelDefaultsToTelegramWhenNoDestinationIsStamped(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newRoutedChannel(telegram, web)

	if err := channel.Deliver(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if len(telegram.delivered) != 1 {
		t.Fatalf("telegram=%#v, want the Telegram default", telegram)
	}
}

func TestRoutedChannelDeliverPropagatesTheUnderlyingChannelsError(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{deliverErr: errors.New("web down")}
	channel := newRoutedChannel(telegram, web)

	if err := channel.Deliver(webCtx("thread-1"), "hello"); err == nil {
		t.Fatal("expected the web channel's error to propagate")
	}
}

func TestRoutedChannelDeliverTrackableRoutesToTheDestination(t *testing.T) {
	telegram := &fakeChannel{trackableID: "123"}
	web := &fakeChannel{trackableID: "abc"}
	channel := newRoutedChannel(telegram, web)

	id, err := channel.DeliverTrackable(webCtx("thread-1"), "working...")
	if err != nil {
		t.Fatal(err)
	}
	if id != "abc" {
		t.Fatalf("id=%q, want the web channel's raw ID (no compound scheme)", id)
	}
}

func TestRoutedChannelEditTextRoutesToTheDestination(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newRoutedChannel(telegram, web)

	if err := channel.EditText(webCtx("thread-1"), "abc", "done"); err != nil {
		t.Fatal(err)
	}
	if len(web.editCalls) != 1 || web.editCalls[0] != "abc:done" {
		t.Fatalf("web.editCalls=%#v", web.editCalls)
	}
	if len(telegram.editCalls) != 0 {
		t.Fatalf("telegram.editCalls=%#v, want none", telegram.editCalls)
	}
}

func TestRoutedChannelSendTypingRoutesToTheDestination(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newRoutedChannel(telegram, web)

	if err := channel.SendTyping(webCtx("thread-1")); err != nil {
		t.Fatal(err)
	}
	if web.typingCalls != 1 {
		t.Fatalf("web.typingCalls=%d", web.typingCalls)
	}
	if telegram.typingCalls != 0 {
		t.Fatalf("telegram.typingCalls=%d, want none", telegram.typingCalls)
	}
}

func TestRoutedChannelDeliverApprovalRoutesToTheDestination(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newRoutedChannel(telegram, web)

	approval := approvals.Approval{ID: "approval-1"}
	if err := channel.DeliverApproval(webCtx("thread-1"), approval); err != nil {
		t.Fatal(err)
	}
	if len(web.approvalDelivered) != 1 {
		t.Fatalf("web=%#v", web)
	}
	if len(telegram.approvalDelivered) != 0 {
		t.Fatalf("telegram=%#v, want none", telegram)
	}
}

func TestNewRoutedChannelReturnsTheSingleChannelUnwrappedWhenOnlyOneIsConfigured(t *testing.T) {
	telegram := &fakeChannel{}
	channel := newRoutedChannel(telegram, nil)
	if channel != ports.Channel(telegram) {
		t.Fatal("expected newRoutedChannel to return the sole non-nil channel directly, not wrap it")
	}
}
