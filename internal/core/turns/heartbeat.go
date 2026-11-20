package turns

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// DefaultHeartbeatInstruction is what a heartbeat asks when the owner has not
// said what to check. Deliberately open: the silence protocol in the system
// message is what keeps an open question from becoming chatter.
const DefaultHeartbeatInstruction = "Check in: is there anything the owner needs to know about right now? Say nothing unless there is."

// HeartbeatInstruction falls back to the built-in default rather than running
// a turn with no input at all.
func HeartbeatInstruction(configured string) string {
	if instruction := strings.TrimSpace(configured); instruction != "" {
		return instruction
	}
	return DefaultHeartbeatInstruction
}

// HeartbeatWindow is the owner's active hours. config.ActiveHours satisfies
// it; the core takes the interface so it never reads the config package.
type HeartbeatWindow interface {
	Configured() bool
	Active(time.Time) bool
	NextOpen(time.Time) (time.Duration, bool)
}

// HeartbeatSchedule decides when beats may run and how long to wait between
// them. Now is the owner's clock, not the host's: a deployment in UTC
// serving an owner elsewhere must go quiet on theirs. A nil Window is always
// open.
type HeartbeatSchedule struct {
	Interval time.Duration
	Window   HeartbeatWindow
	Now      func() time.Time
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

// Active reports whether now falls inside the window. An unset window is
// always active, so an absent section changes nothing.
func (s HeartbeatSchedule) Active() bool {
	if s.Window == nil || !s.Window.Configured() {
		return true
	}
	return s.Window.Active(s.now())
}

// NextWake is how long to wait before the next beat, given what the last one
// asked for.
//
// A non-positive request falls back to the configured interval, which is what
// a beat that made no decision gets: a skipped beat, a failed beat, or the
// first beat after a restart.
//
// A wake landing in quiet hours is moved to the window opening rather than
// dropped. Dropping it is what the ticker did, and it is why the first beat of
// the day could be a whole interval late.
func (s HeartbeatSchedule) NextWake(requested time.Duration) time.Duration {
	wake := requested
	if wake <= 0 {
		wake = s.Interval
	}
	if wake <= 0 {
		return 0
	}
	wake = clampHeartbeatWake(wake, s.Interval)
	if s.Window == nil {
		return wake
	}
	wait, configured := s.Window.NextOpen(s.now().Add(wake))
	if !configured {
		return wake
	}
	return wake + wait
}

func (s HeartbeatSchedule) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// HeartbeatEligible reports whether the account on ctx should get a beat: it
// switched its heartbeat on, and its watch list holds something to check.
//
// An unreadable switch reads as off: a beat nobody asked for is the failure
// the switch exists to prevent. An unreadable watch list reads as non-empty,
// so a store failure degrades into a beat rather than into silence. Blank
// lines and headings do not count as entries (ports.WatchListIsEmpty): beating
// on a document that is only its own title would run a model call to look at
// nothing.
func HeartbeatEligible(ctx context.Context, state ports.StateStore, watch ports.ContextStore) bool {
	if state == nil || watch == nil {
		return false
	}
	current, err := state.Load(ctx)
	if err != nil {
		slog.Error("heartbeat switch unreadable; not beating", "error", err)
		return false
	}
	if !current.Agent.Heartbeat {
		return false
	}
	agentContext, err := watch.Load(ctx)
	if err != nil {
		slog.Error("watch list unreadable; beating anyway", "error", err)
		return true
	}
	return !ports.WatchListIsEmpty(agentContext.Watch)
}
