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
func (a *App) heartbeatTicks() *heartbeatClock {
	interval := a.config.Heartbeat.Interval.Value()
	if interval <= 0 {
		return nil
	}
	if !a.config.Telegram.Configured() {
		slog.Warn("heartbeat.interval is set but no Telegram channel is configured; heartbeat disabled")
		return nil
	}
	// NewTimer first fires one interval after start, so a restart produces
	// no boot-time heartbeat storm.
	return &heartbeatClock{timer: time.NewTimer(interval)}
}

// heartbeatClock is the heartbeat's wake-up, a timer rather than a ticker
// because the gap between beats is no longer a constant.
//
// A ticker fires on a fixed phase, so a beat that takes four minutes is
// followed by one only interval-4m later: the beat's own duration eats into
// the gap the owner configured, and a slow model compresses the cadence
// without anyone asking it to. A timer re-armed once the beat has finished
// puts a full gap after every beat instead.
//
// It also cannot produce a backlog. A ticker drops ticks its receiver is not
// ready for, which is nearly the same thing by accident; a timer that is only
// ever re-armed after a beat completes has nothing to drop in the first place.
//
// A nil clock is the unconfigured heartbeat, and every method tolerates one so
// the daemon loop needs no special case. C() on a nil clock is a nil channel,
// which blocks forever in a select -- the same property the old nil tick
// channel had, and the reason an unconfigured heartbeat still costs nothing at
// runtime.
type heartbeatClock struct{ timer *time.Timer }

func (c *heartbeatClock) C() <-chan time.Time {
	if c == nil {
		return nil
	}
	return c.timer.C
}

// Reset re-arms the clock for the next beat. The timer has always fired by the
// time this is called -- the loop resets only in response to a fire -- so
// there is no pending send to drain first.
func (c *heartbeatClock) Reset(d time.Duration) {
	if c == nil {
		return
	}
	if d <= 0 {
		return
	}
	c.timer.Reset(d)
}

func (c *heartbeatClock) Stop() {
	if c == nil {
		return
	}
	c.timer.Stop()
}

// ownerNow is the current time on the owner's clock. The heartbeat's window is
// the owner's, not the host's: a deployment in UTC serving an owner elsewhere
// must go quiet on theirs.
func (a *App) ownerNow() time.Time {
	now := time.Now
	if a.now != nil {
		now = a.now
	}
	location := a.location
	if location == nil {
		location = time.UTC
	}
	return now().In(location)
}

// The bounds on what a beat may ask for. Wide on purpose: they are a guard
// against a beat that misjudges once, not an opinion about pacing.
//
// The floor is flat rather than a fraction of the interval, because a fraction
// forbids the tight end of the range -- with a 3h interval it would rule out
// the beat that knows a deploy lands in four minutes, which is the case that
// matters most. Five minutes is what stops a misjudged beat becoming a hot
// loop, and nothing else.
//
// The ceiling is relative because its job is different: it prevents a beat
// talking itself into a week of quiet, and what counts as too quiet is exactly
// what the owner expressed by configuring an interval.
const (
	minHeartbeatWake      = 5 * time.Minute
	maxHeartbeatWakeScale = 8
)

// clampHeartbeatWake holds a beat's request inside those bounds. Silently: the
// model's judgement about direction is worth keeping even when its magnitude
// is off, and rejecting would cost a round trip to learn a bound it cannot
// see.
func clampHeartbeatWake(wake, interval time.Duration) time.Duration {
	if wake < minHeartbeatWake {
		wake = minHeartbeatWake
	}
	if ceiling := interval * maxHeartbeatWakeScale; ceiling > 0 && wake > ceiling {
		wake = ceiling
	}
	return wake
}

// nextHeartbeatWake is how long to wait before the next beat, given what the
// last one asked for.
//
// A non-positive request falls back to the configured interval, which is what
// a beat that made no decision gets: a skipped beat, a failed beat, or the
// first beat after a restart.
//
// A wake landing in quiet hours is moved to the window opening rather than
// dropped. Dropping it is what the ticker did, and it is why the first beat of
// the day could be a whole interval late.
func (a *App) nextHeartbeatWake(requested time.Duration) time.Duration {
	interval := a.config.Heartbeat.Interval.Value()
	wake := requested
	if wake <= 0 {
		wake = interval
	}
	if wake <= 0 {
		return 0
	}
	wake = clampHeartbeatWake(wake, interval)
	wait, configured := a.config.Heartbeat.ActiveHours.NextOpen(a.ownerNow().Add(wake))
	if !configured {
		return wake
	}
	return wake + wait
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
	return ports.WatchListIsEmpty(agentContext.Watch)
}

// withinActiveHours reports whether now falls inside the configured window,
// read on the owner's clock rather than the host's. An unset window is always
// active, so an absent section changes nothing.
func (a *App) withinActiveHours() bool {
	hours := a.config.Heartbeat.ActiveHours
	if !hours.Configured() {
		return true
	}
	return hours.Active(a.ownerNow())
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
//
// It reports whether a beat was dispatched, which is what tells the daemon
// loop who re-arms the clock. A dispatched beat re-arms it when it finishes,
// so the next gap starts from the end of the beat; a skipped tick has nothing
// to wait for and is re-armed by the loop immediately. Without the
// distinction a single skip would leave the timer un-armed and the heartbeat
// would stop for good.
func (a *App) onHeartbeatTick(ctx context.Context) bool {
	// Skipped while a turn is already running: ticks cannot pile up when a
	// heartbeat outlasts its interval, and a heartbeat never interrupts a
	// live owner conversation.
	if a.turnService.Active() {
		return false
	}
	// Outside the owner's active hours nothing beats. Checked before the
	// watch list so a quiet-hours skip costs no store read, and before any
	// model call so a 03:00 tick costs nothing at all.
	if !a.withinActiveHours() {
		return false
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
		return false
	}
	a.warnedEmptyWatch = false
	a.workers.Go(func() {
		var requested time.Duration
		// Re-arming is deferred so it happens however the beat ends, failure
		// included. A beat that returned without re-arming would stop the
		// heartbeat permanently, which is a worse failure than the one that
		// caused it.
		defer func() { a.finishHeartbeat(ctx, requested) }()
		// Not retried: the next tick is the retry, and a heartbeat has no
		// durable claim to release.
		response, err := a.turnService.HeartbeatTurn(destination.With(ctx, proactiveDestination()), a.heartbeatInstruction(), a.config.Heartbeat.IncludeRecentHistory)
		if err != nil {
			slog.Error("heartbeat failed", "error", err)
		}
		requested = response.NextCheck
	})
	return true
}

// finishHeartbeat hands the finished beat's next wake back to the daemon loop,
// which owns the clock.
//
// A channel rather than a direct Reset because the beat runs in a worker
// goroutine while the loop owns the timer. The buffer of one is always free:
// the Active() guard admits one beat at a time, and the clock is not re-armed
// until this send lands, so a second beat cannot be dispatched while this one
// is outstanding. The ctx case is a shutdown guard, not a dropped-send path --
// it stops the worker outliving a cancelled loop that will never read.
//
// A zero requested duration is a beat that made no decision -- it failed, or
// answered in prose without calling heartbeat_respond -- and falls back to the
// configured interval in nextHeartbeatWake.
func (a *App) finishHeartbeat(ctx context.Context, requested time.Duration) {
	select {
	case a.heartbeatWake <- requested:
	case <-ctx.Done():
	}
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
	heartbeat := a.heartbeatTicks()
	defer heartbeat.Stop()
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
		case <-heartbeat.C():
			// A dispatched beat re-arms the clock when it finishes, so the
			// next gap starts from the end of the beat rather than from a
			// fixed phase the beat's own duration eats into. A skipped tick
			// has nothing to wait for.
			if !a.onHeartbeatTick(ctx) {
				heartbeat.Reset(a.nextHeartbeatWake(0))
			}
		case requested := <-a.heartbeatWake:
			wake := a.nextHeartbeatWake(requested)
			// The one record that a beat happened and what it decided.
			// Without it a self-paced heartbeat is unobservable: a quiet beat
			// writes nothing anywhere, so nobody can tell good pacing from a
			// stopped clock.
			slog.Info("heartbeat paced", "requested", requested, "next", wake)
			heartbeat.Reset(wake)
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
