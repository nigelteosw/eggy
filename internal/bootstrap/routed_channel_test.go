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
	deliveredChatIDs  []string
	trackableID       string
	trackableErr      error
	editCalls         []string
	typingCalls       []string
	answerCalls       []string
	approvalDelivered []approvals.Approval
	deliverErr        error
}

func (f *fakeChannel) Deliver(_ context.Context, chatID string, text string) error {
	if f.deliverErr != nil {
		return f.deliverErr
	}
	f.delivered = append(f.delivered, text)
	f.deliveredChatIDs = append(f.deliveredChatIDs, chatID)
	return nil
}
func (f *fakeChannel) DeliverApproval(_ context.Context, _ string, approval approvals.Approval) error {
	f.approvalDelivered = append(f.approvalDelivered, approval)
	return nil
}
func (f *fakeChannel) DeliverTrackable(_ context.Context, _ string, text string) (string, error) {
	if f.trackableErr != nil {
		return "", f.trackableErr
	}
	f.delivered = append(f.delivered, text)
	return f.trackableID, nil
}
func (f *fakeChannel) EditText(_ context.Context, _ string, messageID, text string) error {
	f.editCalls = append(f.editCalls, messageID+":"+text)
	return nil
}
func (f *fakeChannel) AnswerCallback(_ context.Context, callbackQueryID string) error {
	f.answerCalls = append(f.answerCalls, callbackQueryID)
	return nil
}
func (f *fakeChannel) SendTyping(_ context.Context, chatID string) error {
	f.typingCalls = append(f.typingCalls, chatID)
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
	channel := newRoutedChannel(telegram, web, "owner-42")

	if err := channel.Deliver(webCtx("thread-1"), "ignored", "hello"); err != nil {
		t.Fatal(err)
	}
	if len(web.delivered) != 1 || web.deliveredChatIDs[0] != "thread-1" {
		t.Fatalf("web=%#v", web)
	}
	if len(telegram.delivered) != 0 {
		t.Fatalf("telegram=%#v, want untouched", telegram)
	}
}

func TestRoutedChannelDeliverResolvesTelegramsOwnerChatIDRegardlessOfCallerArgument(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newRoutedChannel(telegram, web, "owner-42")

	if err := channel.Deliver(telegramCtx(), "whatever-the-caller-passed", "hello"); err != nil {
		t.Fatal(err)
	}
	if len(telegram.delivered) != 1 || telegram.deliveredChatIDs[0] != "owner-42" {
		t.Fatalf("telegram=%#v, want the configured owner chat ID substituted", telegram)
	}
	if len(web.delivered) != 0 {
		t.Fatalf("web=%#v, want untouched", web)
	}
}

func TestRoutedChannelDefaultsToTelegramWhenNoDestinationIsStamped(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newRoutedChannel(telegram, web, "owner-42")

	if err := channel.Deliver(context.Background(), "chat", "hello"); err != nil {
		t.Fatal(err)
	}
	if len(telegram.delivered) != 1 {
		t.Fatalf("telegram=%#v, want the Telegram default", telegram)
	}
}

func TestRoutedChannelDeliverPropagatesTheUnderlyingChannelsError(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{deliverErr: errors.New("web down")}
	channel := newRoutedChannel(telegram, web, "owner-42")

	if err := channel.Deliver(webCtx("thread-1"), "chat", "hello"); err == nil {
		t.Fatal("expected the web channel's error to propagate")
	}
}

func TestRoutedChannelDeliverTrackableRoutesToTheDestination(t *testing.T) {
	telegram := &fakeChannel{trackableID: "123"}
	web := &fakeChannel{trackableID: "abc"}
	channel := newRoutedChannel(telegram, web, "owner-42")

	id, err := channel.DeliverTrackable(webCtx("thread-1"), "chat", "working...")
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
	channel := newRoutedChannel(telegram, web, "owner-42")

	if err := channel.EditText(webCtx("thread-1"), "chat", "abc", "done"); err != nil {
		t.Fatal(err)
	}
	if len(web.editCalls) != 1 || web.editCalls[0] != "abc:done" {
		t.Fatalf("web.editCalls=%#v", web.editCalls)
	}
	if len(telegram.editCalls) != 0 {
		t.Fatalf("telegram.editCalls=%#v, want none", telegram.editCalls)
	}
}

func TestRoutedChannelAnswerCallbackOnlyReachesTelegram(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newRoutedChannel(telegram, web, "owner-42")

	if err := channel.AnswerCallback(webCtx("thread-1"), "callback-1"); err != nil {
		t.Fatal(err)
	}
	if len(telegram.answerCalls) != 1 {
		t.Fatalf("telegram.answerCalls=%#v", telegram.answerCalls)
	}
	if len(web.answerCalls) != 0 {
		t.Fatalf("web.answerCalls=%#v, want none", web.answerCalls)
	}
}

func TestRoutedChannelSendTypingRoutesToTheDestination(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newRoutedChannel(telegram, web, "owner-42")

	if err := channel.SendTyping(webCtx("thread-1"), "chat"); err != nil {
		t.Fatal(err)
	}
	if len(web.typingCalls) != 1 || web.typingCalls[0] != "thread-1" {
		t.Fatalf("web.typingCalls=%#v", web.typingCalls)
	}
	if len(telegram.typingCalls) != 0 {
		t.Fatalf("telegram.typingCalls=%#v, want none", telegram.typingCalls)
	}
}

func TestRoutedChannelDeliverApprovalRoutesToTheDestination(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newRoutedChannel(telegram, web, "owner-42")

	approval := approvals.Approval{ID: "approval-1"}
	if err := channel.DeliverApproval(webCtx("thread-1"), "chat", approval); err != nil {
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
	channel := newRoutedChannel(telegram, nil, "owner-42")
	if channel != ports.Channel(telegram) {
		t.Fatal("expected newRoutedChannel to return the sole non-nil channel directly, not wrap it")
	}
}
