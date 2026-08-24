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

// defaultHeartbeatInstruction is what a heartbeat asks when the owner has not
// said what to check. Deliberately open: the silence protocol in the system
// message is what keeps an open question from becoming chatter.
const defaultHeartbeatInstruction = "Check in: is there anything the owner needs to know about right now? Say nothing unless there is."

// heartbeatInstruction falls back to the built-in default rather than running
// a turn with no input at all.
func (a *App) heartbeatInstruction() string {
	if instruction := strings.TrimSpace(a.config.Heartbeat.Instruction); instruction != "" {
		return instruction
	}
	return defaultHeartbeatInstruction
}

// heartbeatTicks is the heartbeat's clock, separated from what a tick does
// (onHeartbeatTick) so each half is a decision with its own test: when Eggy
// wakes up, and what it does when it wakes.
//
// A nil channel blocks forever in a select, so an unconfigured heartbeat
// costs nothing at runtime: no ticker, no goroutine, no branch ever taken.
//
// A configured Telegram channel is required too. Unprompted output is
// addressed to proactiveDestination(), and newRoutedChannel gives a web-only
// deployment a noop Telegram deliberately -- so without this guard such a
// deployment would wake every interval, run a full model turn, and deliver it
// into the noop: a standing token cost that can never produce a visible
// message. Logged rather than fatal, because the rest of the deployment is
// valid and a silent no-op would be indistinguishable from a broken
// heartbeat.
func (a *App) heartbeatTicks() (<-chan time.Time, func()) {
	interval := a.config.Heartbeat.Interval.Value()
	if interval <= 0 {
		return nil, func() {}
	}
	if !a.config.Telegram.Configured() {
		slog.Warn("heartbeat.interval is set but no Telegram channel is configured; heartbeat disabled")
		return nil, func() {}
	}
	// NewTicker first fires one interval after start, so a restart produces
	// no boot-time heartbeat storm.
	ticker := time.NewTicker(interval)
	return ticker.C, ticker.Stop
}

// watchListIsEmpty reports whether the watch list holds nothing to check.
//
// Blank lines and Markdown headings do not count: a document that is only its
// own title is what a store returns before anyone has written to it, and
// beating on it would run a model call to look at nothing. An unreadable
// document is treated as non-empty so a store failure degrades into a beat
// rather than into silence.
func (a *App) watchListIsEmpty(ctx context.Context) bool {
	if a.context == nil {
		return true
	}
	agentContext, err := a.context.Load(ctx)
	if err != nil {
		slog.Error("watch list unreadable; beating anyway", "error", err)
		return false
	}
	for _, line := range strings.Split(agentContext.Watch, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return false
	}
	return true
}

// withinActiveHours reports whether now falls inside the configured window,
// read on the owner's clock rather than the host's. An unset window is always
// active, so an absent section changes nothing.
func (a *App) withinActiveHours() bool {
	hours := a.config.Heartbeat.ActiveHours
	if !hours.Configured() {
		return true
	}
	location := a.location
	if location == nil {
		location = time.UTC
	}
	now := time.Now
	if a.now != nil {
		now = a.now
	}
	return hours.Active(now().In(location))
}

// shouldWarnEmptyWatch reports whether this skip is the transition into the
// empty state, and records that it warned.
func (a *App) shouldWarnEmptyWatch() bool {
	if a.warnedEmptyWatch {
		return false
	}
	a.warnedEmptyWatch = true
	return true
}

// onHeartbeatTick is what one tick does. Today that is exactly one thing: run
// an isolated turn that is allowed to say nothing. It is a named function
// rather than an inline case so that a second thing a tick could do -- an
// outbound call to an external workflow runner, say -- is added here, beside
// the turn, instead of by growing the daemon loop.
func (a *App) onHeartbeatTick(ctx context.Context) {
	// Skipped while a turn is already running: ticks cannot pile up when a
	// heartbeat outlasts its interval, and a heartbeat never interrupts a
	// live owner conversation.
	if a.turnService.Active() {
		return
	}
	// Outside the owner's active hours nothing beats. Checked before the
	// watch list so a quiet-hours skip costs no store read, and before any
	// model call so a 03:00 tick costs nothing at all.
	if !a.withinActiveHours() {
		return
	}
	// An empty watch list means the owner has asked for nothing to be
	// watched, so there is nothing to check and no model call to justify.
	// Warned once on the way in, for the same reason the missing-Telegram
	// case warns: a silent no-op is indistinguishable from a broken
	// heartbeat.
	if a.watchListIsEmpty(ctx) {
		if a.shouldWarnEmptyWatch() {
			slog.Warn("heartbeat is configured but memories/WATCH.md is empty; add what Eggy should keep an eye on, or unset heartbeat.interval")
		}
		return
	}
	a.warnedEmptyWatch = false
	a.workers.Go(func() {
		// Not retried: the next tick is the retry, and a heartbeat has no
		// durable claim to release.
		if err := a.turnService.HeartbeatTurn(destination.With(ctx, proactiveDestination()), a.heartbeatInstruction(), a.config.Heartbeat.IncludeRecentHistory); err != nil {
			slog.Error("heartbeat failed", "error", err)
		}
	})
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
	heartbeat, stopHeartbeat := a.heartbeatTicks()
	defer stopHeartbeat()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-a.restart:
			// Deliberately not a cancellation: ctx stays live so the deferred
			// workers.Wait drains the turn that asked for the restart -- and
			// any other turn mid-flight -- instead of cutting it off. Only
			// once they are done do the stores and MCP clients close.
			slog.Info("restart requested, draining in-flight turns")
			return ErrRestart
		case <-heartbeat:
			a.onHeartbeatTick(ctx)
		case event := <-a.eventQueue:
			a.workers.Go(func() {
				if err := a.HandleEvent(ctx, event); err != nil {
					slog.Error("event failed", "event_id", event.ID, "correlation_id", event.CorrelationID, "error", err)
				}
			})
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
				a.workers.Go(func() {
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
				})
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
