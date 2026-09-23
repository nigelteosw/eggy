package bootstrap

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

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

// heartbeatCadence says when beats run, such as "every 3h, 08:00-22:00
// Asia/Singapore". It is empty exactly when heartbeatTicks returns no clock,
// so neither the prompt nor /heartbeat promises a check-in that cannot run.
func heartbeatCadence(cfg config.Config) string {
	interval := cfg.Heartbeat.Interval.Value()
	if interval <= 0 || !cfg.TelegramEnabled() {
		return ""
	}
	when := "every " + compactDuration(interval)
	if hours := cfg.Heartbeat.ActiveHours; hours.Configured() {
		when += ", " + hours.Start + "-" + hours.End + " " + cfg.Agent.Timezone
	}
	return when
}

// heartbeatLine is the runtime line telling an owner turn that check-ins
// happen, when, and what feeds them. Turns keep it only for an account that
// switched its heartbeat on.
func heartbeatLine(cfg config.Config) string {
	when := heartbeatCadence(cfg)
	if when == "" {
		return ""
	}
	return "heartbeat: you check in on Telegram " + when + " and speak only when something needs the owner. " +
		"When the owner mentions something they are waiting on or a deadline, add it to the watch list with the memory tool, without asking; that list is what you check."
}

// compactDuration drops the zero units time.Duration.String spells out, so
// three hours reads "3h" rather than "3h0m0s".
func compactDuration(d time.Duration) string {
	text := strings.TrimSuffix(d.String(), "0s")
	if strings.HasSuffix(text, "h0m") {
		text = strings.TrimSuffix(text, "0m")
	}
	return text
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
	if !a.config.TelegramEnabled() {
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

// watchListIsEmpty reports whether the account's watch list holds nothing
// to check.
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

// heartbeatAccounts is who a tick beats for: every account that has switched
// its heartbeat on, can be reached on Telegram, where unprompted output goes,
// and whose watch list holds something. Each gets its own turn under its own principal, with its
// own watch list and preferences; a person with nothing to watch costs no
// model call, and a web-only person gets no beat because there is nowhere to
// deliver one.
func (a *App) heartbeatAccounts(ctx context.Context) []context.Context {
	var beats []context.Context
	for _, account := range a.accountRecords() {
		if account.TelegramUserID == 0 {
			continue
		}
		accountCtx := ports.WithPrincipal(ctx, ports.Principal{AccountID: account.ID})
		if !a.heartbeatSwitchedOn(accountCtx) || a.watchListIsEmpty(accountCtx) {
			continue
		}
		beats = append(beats, accountCtx)
	}
	return beats
}

// heartbeatSwitchedOn reports whether the account has turned its check-ins
// on. An unreadable state reads as off: a beat nobody asked for is the
// failure the switch exists to prevent.
func (a *App) heartbeatSwitchedOn(ctx context.Context) bool {
	if a.store == nil {
		return false
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		slog.Error("heartbeat switch unreadable; not beating", "error", err)
		return false
	}
	return state.Agent.Heartbeat
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
	// An empty watch list means the person has asked for nothing to be
	// watched, so there is nothing to check and no model call to justify.
	// Warned once on the way in, for the same reason the missing-Telegram
	// case warns: a silent no-op is indistinguishable from a broken
	// heartbeat.
	beats := a.heartbeatAccounts(ctx)
	if len(beats) == 0 {
		if a.shouldWarnEmptyWatch() {
			slog.Warn("heartbeat is configured but no account has it switched on with anything on its watch list; /heartbeat on turns it on for you")
		}
		return false
	}
	a.warnedEmptyWatch = false
	a.workers.Go(func() {
		var requested time.Duration
		// Re-arming is deferred so it happens however the beats end, failure
		// included. A beat that returned without re-arming would stop the
		// heartbeat permanently, which is a worse failure than the one that
		// caused it.
		defer func() { a.finishHeartbeat(ctx, requested) }()
		// One after another rather than in parallel: the Active guard admits
		// one beat at a time, and a person's beat must not run beside another
		// person's on the same loop. The soonest requested check wins the
		// clock, so nobody's shorter interval is stretched by a neighbour's.
		for _, beatCtx := range beats {
			// Not retried: the next tick is the retry, and a heartbeat has no
			// durable claim to release.
			response, err := a.turnService.HeartbeatTurn(destination.With(beatCtx, proactiveDestination()), a.heartbeatInstruction(), a.config.Heartbeat.IncludeRecentHistory)
			if err != nil {
				slog.Error("heartbeat failed", "error", err)
				continue
			}
			if response.NextCheck > 0 && (requested == 0 || response.NextCheck < requested) {
				requested = response.NextCheck
			}
		}
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
