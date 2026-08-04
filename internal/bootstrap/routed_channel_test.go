package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/channels/channelutil"
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
	approvalErr       error
}

func (f *fakeChannel) Deliver(_ context.Context, text string) error {
	if f.deliverErr != nil {
		return f.deliverErr
	}
	f.delivered = append(f.delivered, text)
	return nil
}
func (f *fakeChannel) DeliverApproval(_ context.Context, approval approvals.Approval) error {
	if f.approvalErr != nil {
		return f.approvalErr
	}
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

// baseChannel implements only ports.Channel: no in-place edits, no typing
// indicator. It stands in for a surface that cannot honour the optional
// extensions.
type baseChannel struct {
	delivered []string
}

func (b *baseChannel) Deliver(_ context.Context, text string) error {
	b.delivered = append(b.delivered, text)
	return nil
}
func (b *baseChannel) DeliverApproval(context.Context, approvals.Approval) error { return nil }

func telegramCtx() context.Context {
	return destination.With(context.Background(), destination.Destination{Kind: destination.Telegram})
}

func webCtx(threadID string) context.Context {
	return destination.With(context.Background(), destination.Destination{Kind: destination.Web, ThreadID: threadID})
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

	id, err := channel.(ports.TrackableChannel).DeliverTrackable(webCtx("thread-1"), "working...")
	if err != nil {
		t.Fatal(err)
	}
	if id != "abc" {
		t.Fatalf("id=%q, want the web channel's raw ID (no compound scheme)", id)
	}
}

func TestRoutedChannelDeliverTrackableFallsBackToPlainDeliveryForABaseChannel(t *testing.T) {
	telegram := &fakeChannel{trackableID: "123"}
	web := &baseChannel{}
	channel := newRoutedChannel(telegram, web)

	id, err := channel.(ports.TrackableChannel).DeliverTrackable(webCtx("thread-1"), "working...")
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("id=%q, want no handle from a channel that cannot edit", id)
	}
	if len(web.delivered) != 1 || web.delivered[0] != "working..." {
		t.Fatalf("web.delivered=%#v, want the text delivered anyway", web.delivered)
	}
}

func TestRoutedChannelEditTextReportsUnsupportedForABaseChannel(t *testing.T) {
	channel := newRoutedChannel(&fakeChannel{}, &baseChannel{})

	err := channel.(ports.TrackableChannel).EditText(webCtx("thread-1"), "abc", "done")
	if !errors.Is(err, channelutil.ErrEditsUnsupported) {
		t.Fatalf("err=%v, want ErrEditsUnsupported so callers take the fallback path", err)
	}
}

func TestRoutedChannelSendTypingIsANoopForABaseChannel(t *testing.T) {
	channel := newRoutedChannel(&fakeChannel{}, &baseChannel{})

	if err := channel.(ports.TypingChannel).SendTyping(webCtx("thread-1")); err != nil {
		t.Fatalf("err=%v, want a silent no-op for a channel with no typing indicator", err)
	}
}

func TestRoutedChannelEditTextRoutesToTheDestination(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newRoutedChannel(telegram, web)

	if err := channel.(ports.TrackableChannel).EditText(webCtx("thread-1"), "abc", "done"); err != nil {
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

	if err := channel.(ports.TypingChannel).SendTyping(webCtx("thread-1")); err != nil {
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

// A web-only deployment must not have unprompted output redirected into a
// web thread: with no Telegram configured, Telegram-addressed delivery is
// dropped, while web-addressed delivery still routes normally.
func TestWebOnlyDeploymentDropsTelegramAddressedDeliveryInsteadOfRedirectingIt(t *testing.T) {
	web := &fakeChannel{name: "web"}
	channel := newRoutedChannel(nil, web)

	proactive := destination.With(context.Background(), destination.Destination{Kind: destination.Telegram})
	if err := channel.Deliver(proactive, "heartbeat check-in"); err != nil {
		t.Fatal(err)
	}
	if err := channel.DeliverApproval(proactive, approvals.Approval{ID: "approval-1"}); err != nil {
		t.Fatal(err)
	}
	if len(web.delivered) != 0 || len(web.approvalDelivered) != 0 {
		t.Fatalf("web delivered=%v approvals=%v, want unprompted output dropped, not redirected", web.delivered, web.approvalDelivered)
	}

	owner := destination.With(context.Background(), destination.Destination{Kind: destination.Web, ThreadID: "thread-a"})
	if err := channel.Deliver(owner, "reply"); err != nil {
		t.Fatal(err)
	}
	if len(web.delivered) != 1 || web.delivered[0] != "reply" {
		t.Fatalf("web delivered=%v, want owner-initiated web turns unaffected", web.delivered)
	}
}
