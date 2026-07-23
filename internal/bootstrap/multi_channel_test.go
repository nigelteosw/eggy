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
	answerCalls       []string
	approvalDelivered []approvals.Approval
	deliverErr        error
}

func (f *fakeChannel) Deliver(_ context.Context, _ string, text string) error {
	if f.deliverErr != nil {
		return f.deliverErr
	}
	f.delivered = append(f.delivered, text)
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
func (f *fakeChannel) SendTyping(context.Context, string) error {
	f.typingCalls++
	return nil
}

func TestMultiChannelDeliverFansOutToBoth(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	if err := channel.Deliver(context.Background(), "chat", "hello"); err != nil {
		t.Fatal(err)
	}
	if len(telegram.delivered) != 1 || len(web.delivered) != 1 {
		t.Fatalf("telegram=%#v web=%#v", telegram, web)
	}
}

func TestMultiChannelDeliverSucceedsIfOnlyOneChannelSucceeds(t *testing.T) {
	telegram := &fakeChannel{deliverErr: errors.New("telegram down")}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	if err := channel.Deliver(context.Background(), "chat", "hello"); err != nil {
		t.Fatalf("expected nil error when at least one channel succeeds, got %v", err)
	}
	if len(web.delivered) != 1 {
		t.Fatalf("web=%#v", web)
	}
}

func TestMultiChannelDeliverFailsIfBothChannelsFail(t *testing.T) {
	telegram := &fakeChannel{deliverErr: errors.New("telegram down")}
	web := &fakeChannel{deliverErr: errors.New("web down")}
	channel := newMultiChannel(telegram, web)

	if err := channel.Deliver(context.Background(), "chat", "hello"); err == nil {
		t.Fatal("expected an error when both channels fail")
	}
}

func TestMultiChannelDeliverTrackableEncodesACompoundID(t *testing.T) {
	telegram := &fakeChannel{trackableID: "123"}
	web := &fakeChannel{trackableID: "abc"}
	channel := newMultiChannel(telegram, web)

	id, err := channel.DeliverTrackable(context.Background(), "chat", "working...")
	if err != nil {
		t.Fatal(err)
	}
	if id != "telegram:123|web:abc" {
		t.Fatalf("id=%q", id)
	}
}

func TestMultiChannelDeliverTrackableOmitsAFailingChannelsHalf(t *testing.T) {
	telegram := &fakeChannel{trackableErr: errors.New("telegram down")}
	web := &fakeChannel{trackableID: "abc"}
	channel := newMultiChannel(telegram, web)

	id, err := channel.DeliverTrackable(context.Background(), "chat", "working...")
	if err != nil {
		t.Fatal(err)
	}
	if id != "web:abc" {
		t.Fatalf("id=%q", id)
	}
}

func TestMultiChannelEditTextRoutesEachHalfToItsChannel(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	if err := channel.EditText(context.Background(), "chat", "telegram:123|web:abc", "done"); err != nil {
		t.Fatal(err)
	}
	if len(telegram.editCalls) != 1 || telegram.editCalls[0] != "123:done" {
		t.Fatalf("telegram.editCalls=%#v", telegram.editCalls)
	}
	if len(web.editCalls) != 1 || web.editCalls[0] != "abc:done" {
		t.Fatalf("web.editCalls=%#v", web.editCalls)
	}
}

func TestMultiChannelEditTextHandlesASingleHalfID(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	if err := channel.EditText(context.Background(), "chat", "web:abc", "done"); err != nil {
		t.Fatal(err)
	}
	if len(telegram.editCalls) != 0 {
		t.Fatalf("telegram.editCalls=%#v, want none", telegram.editCalls)
	}
	if len(web.editCalls) != 1 || web.editCalls[0] != "abc:done" {
		t.Fatalf("web.editCalls=%#v", web.editCalls)
	}
}

func TestMultiChannelAnswerCallbackOnlyReachesTelegram(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	if err := channel.AnswerCallback(context.Background(), "callback-1"); err != nil {
		t.Fatal(err)
	}
	if len(telegram.answerCalls) != 1 {
		t.Fatalf("telegram.answerCalls=%#v", telegram.answerCalls)
	}
	if len(web.answerCalls) != 0 {
		t.Fatalf("web.answerCalls=%#v, want none", web.answerCalls)
	}
}

func TestMultiChannelSendTypingReachesBoth(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	if err := channel.SendTyping(context.Background(), "chat"); err != nil {
		t.Fatal(err)
	}
	if telegram.typingCalls != 1 || web.typingCalls != 1 {
		t.Fatalf("telegram=%d web=%d", telegram.typingCalls, web.typingCalls)
	}
}

func TestMultiChannelDeliverApprovalReachesBoth(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	approval := approvals.Approval{ID: "approval-1"}
	if err := channel.DeliverApproval(context.Background(), "chat", approval); err != nil {
		t.Fatal(err)
	}
	if len(telegram.approvalDelivered) != 1 || len(web.approvalDelivered) != 1 {
		t.Fatalf("telegram=%#v web=%#v", telegram, web)
	}
}

func TestNewMultiChannelReturnsTheSingleChannelUnwrappedWhenOnlyOneIsConfigured(t *testing.T) {
	telegram := &fakeChannel{}
	channel := newMultiChannel(telegram, nil)
	if channel != ports.Channel(telegram) {
		t.Fatal("expected newMultiChannel to return the sole non-nil channel directly, not wrap it")
	}
}
