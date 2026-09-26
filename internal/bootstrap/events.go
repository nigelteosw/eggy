package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/core/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

// App's runtime behavior once NewApp has wired it is split across three
// files: this one is event dispatch (HandleEvent/Enqueue/processEvent), run.go
// is the daemon loop (Run) and the schedule tick, and heartbeat.go is the
// periodic check-in. What happens *inside* a turn -- the tool allowlists, the
// context it is built from, and the owner/scheduled distinction -- is
// internal/core/turns, which these files only route into. See app.go for
// construction, and turn_presenter.go for the surface-side rendering that
// package asks for.

func (a *App) HandleEvent(ctx context.Context, event events.Event) error {
	return a.dispatcher.Handle(ctx, event)
}

// Enqueue hands an event to the loop without blocking: a web request or a
// webhook must not park waiting for queue space.
//
// ctx is checked explicitly rather than as a third select case. With default
// present, a `case <-ctx.Done()` can never fire -- select takes default the
// moment no other case is ready -- so a cancelled context reported "event
// queue is full", which is both wrong and misleading when debugging.
func (a *App) Enqueue(ctx context.Context, event events.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case a.eventQueue <- event:
		return nil
	default:
		return errors.New("event queue is full")
	}
}

func (a *App) processEvent(ctx context.Context, event events.Event) error {
	switch event.Type {
	case events.TypeMessage:
		// Ingress is where a destination is checked, so a producer that
		// stamped something undeliverable fails here rather than after a
		// model call whose reply then has nowhere honest to go.
		if err := event.Destination.Validate(); err != nil {
			return err
		}
		message, err := decodeMessage(event)
		if err != nil {
			return err
		}
		source := strings.TrimSpace(event.Source)
		if source == "" {
			source = "telegram"
		}
		// A verified Telegram sender may ask /web for a browser login.
		// The mark needs all three: an explicit Telegram source, an
		// explicit Telegram destination, and the sender the webhook
		// verified. The empty-source default above is a delivery fallback,
		// not evidence of where anything came from.
		if event.Source == "telegram" && event.Destination.Kind == destination.Telegram && event.SenderID != "" {
			ctx = commands.WithWebLoginSender(ctx, event.SenderID)
		}
		return a.turnService.OwnerMessage(destination.With(ctx, event.Destination), ports.Message{
			Role: ports.RoleUser, Content: message.Prompt(), Parts: message.Parts,
		}, source)
	case events.TypeSchedule:
		message, err := decodeMessage(event)
		if err != nil {
			return err
		}
		return a.turnService.ScheduledTurn(destination.With(ctx, proactiveDestination()), message.Text)
	case events.TypeScheduledMessage:
		// A deterministic, pre-rendered notification (a reminder or
		// watchdog-style check-in): delivered verbatim with no model call at
		// all, as distinct from TypeSchedule above.
		message, err := decodeMessage(event)
		if err != nil {
			return err
		}
		return a.channel.Deliver(destination.With(ctx, proactiveDestination()), message.Text)
	case events.TypeApproval:
		var decision events.ApprovalDecision
		if err := json.Unmarshal(event.Payload, &decision); err != nil {
			return err
		}
		return a.turnService.Approval(ctx, decision)
	default:
		return errors.New("unsupported event type")
	}
}

func decodeMessage(event events.Event) (events.Message, error) {
	var message events.Message
	if err := json.Unmarshal(event.Payload, &message); err != nil {
		return events.Message{}, err
	}
	return message, nil
}

// proactiveDestination is where scheduled agent turns and messages report.
// Telegram's, deliberately and for now: the web UI is a pull surface the
// owner opens, not one Eggy pushes to, and one proactive channel keeps the
// rather than per-channel.
//
// This is a decision, not a default. Every proactive path stamps it on ctx
// explicitly instead of relying on destination.FromContext's Telegram
// fallback, so making the surface configurable later means changing this
// one function rather than finding the paths that silently fell through.
func proactiveDestination() destination.Destination {
	return destination.Destination{Kind: destination.Telegram}
}

func newRunID() string {
	data := make([]byte, 6)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}
