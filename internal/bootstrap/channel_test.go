package bootstrap

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nigelteosw/eggy/internal/channel/router"
	"github.com/nigelteosw/eggy/internal/core/approvals"
	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/core/events"
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

func TestProcessEventRefusesAnInvalidDestinationBeforeStartingATurn(t *testing.T) {
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	telegram := &fakeChannel{name: "telegram"}
	app.channel = router.New(telegram, &fakeChannel{}, nil)
	payload, _ := json.Marshal(events.Message{Text: "hello"})
	for _, dest := range []destination.Destination{{Kind: destination.Discord}, {Kind: "slack"}} {
		event := events.Event{ID: "discord:message:1", Type: events.TypeMessage, Owner: "42", Source: "discord", Destination: dest, Payload: payload}
		if err := app.processEvent(ownerCtx(), event); err == nil {
			t.Fatalf("destination %+v: expected an ingress error", dest)
		}
	}
	if len(telegram.delivered) != 0 {
		t.Fatalf("telegram delivered=%v, want nothing", telegram.delivered)
	}
}
