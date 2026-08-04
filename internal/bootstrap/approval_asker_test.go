package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

// Recording an approval and asking for it are two steps, and for a while only
// the first happened: the gate wrote a pending record, the model was told the
// owner had been asked, and no message with approve and reject buttons was
// ever sent. A gate that swallows the question is worse than no gate -- the
// call does not run, and nobody knows why.
func TestGatedCallActuallyAsksTheOwner(t *testing.T) {
	store := newAskerStateStore()
	service := services.NewApprovalService(store, time.Now, 30*time.Minute, ports.ModeNormal)
	channel := &fakeChannel{}
	asker := &approvalAsker{service: service, channel: channel}
	inner := &askerTool{}
	gated := services.NewApprovalGatedToolIf(inner, asker, service, services.RuleFor(inner.Definition()))

	raw, err := gated.Execute(context.Background(), json.RawMessage(`{"to":"someone@example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(inner.calls) != 0 {
		t.Fatalf("the call ran without approval: %v", inner.calls)
	}
	if len(channel.approvalDelivered) != 1 {
		t.Fatalf("the owner was never asked: %+v", channel.approvalDelivered)
	}
	// The delivered approval has to be the one that was recorded, or the tap
	// authorizes an approval nobody is holding.
	pending, err := service.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || channel.approvalDelivered[0].ID != pending[0].ID {
		t.Fatalf("delivered %+v, pending %+v", channel.approvalDelivered, pending)
	}
	if !strings.Contains(string(raw), "awaiting_approval") {
		t.Fatalf("result=%s", raw)
	}
}

// A question that could not be delivered is reported, not swallowed. The model
// must never tell the owner their approval is waiting on a message that never
// arrived -- and the record survives, so the panel and /status can still find
// it.
func TestUndeliverableApprovalIsReportedAndStillRecorded(t *testing.T) {
	store := newAskerStateStore()
	service := services.NewApprovalService(store, time.Now, 30*time.Minute, ports.ModeNormal)
	channel := &fakeChannel{}
	channel.approvalErr = errors.New("telegram unreachable")
	asker := &approvalAsker{service: service, channel: channel}
	inner := &askerTool{}
	gated := services.NewApprovalGatedToolIf(inner, asker, service, services.RuleFor(inner.Definition()))

	if _, err := gated.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("an undeliverable approval was reported as waiting")
	}
	if len(inner.calls) != 0 {
		t.Fatalf("the call ran anyway: %v", inner.calls)
	}
	pending, err := service.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("the approval was lost with the message: %+v", pending)
	}
}

type askerTool struct{ calls []string }

func (t *askerTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: "sender", Description: "Send", Schema: json.RawMessage(`{"type":"object"}`)}
}

func (t *askerTool) Execute(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	t.calls = append(t.calls, string(raw))
	return json.RawMessage(`{"ok":true}`), nil
}

type askerStateStore struct{ state ports.State }

func newAskerStateStore() *askerStateStore { return &askerStateStore{} }

func (s *askerStateStore) Load(context.Context) (ports.State, error) { return s.state, nil }

func (s *askerStateStore) Update(_ context.Context, _ uint64, mutate func(*ports.State) error) (ports.State, error) {
	next := s.state
	if err := mutate(&next); err != nil {
		return ports.State{}, err
	}
	next.Version++
	s.state = next
	return next, nil
}

var _ approvals.Action = services.ApprovalToolCall
