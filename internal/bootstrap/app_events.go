package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

// This file is App's runtime behavior once NewApp has wired it: the daemon
// loop (Run), event dispatch (HandleEvent/Enqueue/processEvent), and the
// background workers. What happens *inside* a turn -- the tool allowlists,
// the context it is built from, and the owner/scheduled
// distinction -- is internal/kernel/turns, which this file only routes into.
// See app.go for construction, and turn_presenter.go for the surface-side
// rendering that package asks for.

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
		message, err := decodeMessage(event)
		if err != nil {
			return err
		}
		source := strings.TrimSpace(event.Source)
		if source == "" {
			source = "telegram"
		}
		return a.turnService.OwnerMessage(destination.With(ctx, event.Destination), message.Text, source)
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

func (a *App) Run(ctx context.Context) error {
	if a.memory != nil {
		defer a.memory.Close()
	}
	defer a.workers.Wait()
	if a.mcp != nil {
		defer a.mcp.Close()
	}
	// Thread-attached checkouts are durable, so a restart inherits whatever
	// the last process left on the volume. Reconcile before serving a turn:
	// a binding whose directory is gone must not resolve.
	if a.workspaces != nil {
		if _, err := a.workspaces.Recover(ctx); err != nil {
			return err
		}
	}
	if err := a.scheduler.Recover(ctx); err != nil {
		return err
	}
	scheduleTicker := time.NewTicker(time.Minute)
	defer scheduleTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-a.eventQueue:
			a.workers.Add(1)
			go func() {
				defer a.workers.Done()
				if err := a.HandleEvent(ctx, event); err != nil {
					slog.Error("event failed", "event_id", event.ID, "correlation_id", event.CorrelationID, "error", err)
				}
			}()
		case now := <-scheduleTicker.C:
			cutoff := now.Add(-a.config.Runner.Retention.Value())
			// A checkout belongs to its thread, so there is exactly one
			// reaper for it: the change that branched it never owned it and
			// has nothing to release.
			if a.workspaces != nil {
				if _, err := a.workspaces.CleanupIdle(ctx, cutoff); err != nil {
					return err
				}
			}
			due, err := a.scheduler.Due(ctx, now)
			if err != nil {
				return err
			}
			for _, schedule := range due {
				schedule := schedule
				// A ScheduleExecutionMessage schedule is a deterministic,
				// pre-rendered notification (reminder or watchdog): it is
				// delivered verbatim on TypeScheduledMessage with no model
				// call. Everything else starts a self-contained,
				// no-ambient-history agent turn on TypeSchedule.
				eventType := events.TypeSchedule
				if schedule.Execution == ports.ScheduleExecutionMessage {
					eventType = events.TypeScheduledMessage
				}
				payload, _ := json.Marshal(events.Message{Text: schedule.Instruction})
				event := events.Event{ID: "schedule:" + schedule.ID + ":" + schedule.PendingRun.Format(time.RFC3339Nano), Type: eventType, Owner: a.config.Owner.ID, Timestamp: now, Destination: proactiveDestination(), Payload: payload}
				a.workers.Add(1)
				go func() {
					defer a.workers.Done()
					if err := a.HandleEvent(ctx, event); err != nil {
						if failErr := a.scheduler.Fail(ctx, schedule.ID, schedule.PendingRun); failErr != nil {
							slog.Error("schedule failure acknowledgement failed", "schedule_id", schedule.ID, "error", failErr)
						}
						slog.Error("scheduled event failed", "schedule_id", schedule.ID, "error", err)
						return
					}
					if err := a.scheduler.Complete(ctx, schedule.ID, schedule.PendingRun, a.now()); err != nil {
						slog.Error("schedule completion acknowledgement failed", "schedule_id", schedule.ID, "error", err)
					}
				}()
			}
		}
	}
}

func newRunID() string {
	data := make([]byte, 6)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}

func repositoryNamesFromState(state ports.State) []string {
	names := make([]string, 0, len(state.Repositories))
	for name := range state.Repositories {
		names = append(names, name)
	}
	return names
}
