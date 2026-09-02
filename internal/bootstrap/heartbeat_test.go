package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// The heartbeat's clock is two decisions, and both are guards rather than
// conveniences: an unconfigured capability must cost nothing at runtime, and
// a deployment that cannot receive unprompted output must not pay for turns
// nobody will ever see.
func TestHeartbeatTicksOnlyWhenConfiguredAndDeliverable(t *testing.T) {
	for name, tt := range map[string]struct {
		interval config.Duration
		telegram config.TelegramConfig
		wantTick bool
	}{
		"unset interval starts no ticker":          {telegram: config.TelegramConfig{OwnerID: 42}},
		"web-only deployment starts no ticker":     {interval: config.Duration(time.Millisecond), telegram: config.TelegramConfig{}},
		"interval plus Telegram starts the ticker": {interval: config.Duration(time.Millisecond), telegram: config.TelegramConfig{OwnerID: 42}, wantTick: true},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := appTestConfig(t.TempDir())
			cfg.Heartbeat = config.HeartbeatConfig{Interval: tt.interval}
			cfg.Telegram = tt.telegram
			app := &App{config: cfg}
			clock := app.heartbeatTicks()
			defer clock.Stop()
			if (clock != nil) != tt.wantTick {
				t.Fatalf("clock started=%v, want %v", clock != nil, tt.wantTick)
			}
			if !tt.wantTick {
				// A nil clock still answers, so the daemon loop needs no
				// special case for an unconfigured heartbeat: its channel
				// blocks forever rather than firing.
				if clock.C() != nil {
					t.Fatal("an unconfigured heartbeat produced a live channel")
				}
				return
			}
			select {
			case <-clock.C():
			case <-time.After(time.Second):
				t.Fatal("a configured heartbeat never fired")
			}
		})
	}
}

// The clock re-arms only when it is told to, which is what puts a full gap
// after every beat instead of letting the beat's own duration eat into it.
//
// The old ticker fired on a fixed phase, so a beat that outlasted a
// meaningful slice of the interval was followed by a short gap -- a slow
// model compressing the cadence without anyone asking it to.
func TestHeartbeatClockPutsAFullGapAfterABeat(t *testing.T) {
	const interval = 40 * time.Millisecond
	clock := &heartbeatClock{timer: time.NewTimer(interval)}
	defer clock.Stop()

	<-clock.C()
	// Stand in for a beat that takes a large fraction of the interval. A
	// ticker would already be most of the way to firing again by now.
	beat := interval / 2
	time.Sleep(beat)

	rearmed := time.Now()
	clock.Reset(interval)
	select {
	case <-clock.C():
		if elapsed := time.Since(rearmed); elapsed < interval-(interval/4) {
			t.Fatalf("the gap after the beat was %s, want about %s -- the beat's duration ate into it", elapsed, interval)
		}
	case <-time.After(time.Second):
		t.Fatal("the clock never fired after being re-armed")
	}
}

// A clock that is re-armed only after a beat completes has nothing to queue,
// so a long beat cannot be followed by a burst of catch-up beats.
func TestHeartbeatClockAccumulatesNoBacklog(t *testing.T) {
	const interval = 10 * time.Millisecond
	clock := &heartbeatClock{timer: time.NewTimer(interval)}
	defer clock.Stop()

	<-clock.C()
	// A beat far longer than the interval. A ticker would have queued a tick
	// behind this one; a timer that nobody re-armed has nothing pending.
	time.Sleep(interval * 6)
	select {
	case <-clock.C():
		t.Fatal("a tick arrived while no beat had re-armed the clock")
	default:
	}
}

// A nil clock is the unconfigured heartbeat. Every method tolerates one so the
// daemon loop needs no special case, and its channel blocks forever rather
// than firing -- which is what keeps an unconfigured heartbeat free.
func TestNilHeartbeatClockIsInert(t *testing.T) {
	var clock *heartbeatClock
	if clock.C() != nil {
		t.Fatal("a nil clock produced a live channel")
	}
	clock.Reset(time.Second)
	clock.Stop()
}

// A wake landing in quiet hours is moved to the window opening rather than
// dropped. Dropping it is what the ticker did, and it is why the first beat of
// the day could be a whole interval late: a wake computed for 07:00 against an
// 08:00 window was skipped, and the next did not arrive until 10:00.
func TestNextHeartbeatWakeSnapsToTheWindowOpening(t *testing.T) {
	singapore, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatal(err)
	}
	window := config.ActiveHours{Start: "08:00", End: "22:00"}

	for name, tt := range map[string]struct {
		hours     config.ActiveHours
		interval  time.Duration
		now       time.Time
		requested time.Duration
		want      time.Duration
	}{
		"a wake inside the window is left alone": {
			hours: window, interval: 3 * time.Hour,
			now:  time.Date(2026, 8, 24, 9, 0, 0, 0, singapore),
			want: 3 * time.Hour,
		},
		"an evening wake becomes tomorrow's opening": {
			// 20:00 + 3h is 23:00, outside the window. The next beat is at
			// 08:00 rather than at 23:00 or 02:00.
			hours: window, interval: 3 * time.Hour,
			now:  time.Date(2026, 8, 24, 20, 0, 0, 0, singapore),
			want: 12 * time.Hour,
		},
		"an early wake becomes this morning's opening": {
			hours: window, interval: 3 * time.Hour,
			now:  time.Date(2026, 8, 24, 4, 0, 0, 0, singapore),
			want: 4 * time.Hour,
		},
		"no window leaves every wake alone": {
			interval: 3 * time.Hour,
			now:      time.Date(2026, 8, 24, 4, 0, 0, 0, singapore),
			want:     3 * time.Hour,
		},
		// What a skipped or failed beat asks for: nothing. The configured
		// interval is the fallback, and it is still subject to the window.
		"no request falls back to the interval": {
			hours: window, interval: 90 * time.Minute,
			now:  time.Date(2026, 8, 24, 9, 0, 0, 0, singapore),
			want: 90 * time.Minute,
		},
		"a request is honoured over the interval": {
			hours: window, interval: 3 * time.Hour,
			now:       time.Date(2026, 8, 24, 9, 0, 0, 0, singapore),
			requested: 10 * time.Minute,
			want:      10 * time.Minute,
		},
		"an unset interval stays off": {
			hours: window,
			now:   time.Date(2026, 8, 24, 9, 0, 0, 0, singapore),
			want:  0,
		},
	} {
		t.Run(name, func(t *testing.T) {
			app := &App{
				config: config.Config{Heartbeat: config.HeartbeatConfig{
					Interval:    config.Duration(tt.interval),
					ActiveHours: tt.hours,
				}},
				location: singapore,
				now:      func() time.Time { return tt.now },
			}
			if got := app.nextHeartbeatWake(tt.requested); got != tt.want {
				t.Fatalf("nextHeartbeatWake=%s want %s", got, tt.want)
			}
		})
	}
}

// The bounds are a guard against one misjudged beat, not an opinion about
// pacing, so they are wide and the floor is flat.
func TestHeartbeatWakeBounds(t *testing.T) {
	const interval = 3 * time.Hour
	for name, tt := range map[string]struct {
		requested time.Duration
		want      time.Duration
	}{
		// The case the flat floor exists for. A floor of interval/6 would
		// round this up to 30m and the beat would miss the thing it knows is
		// landing -- which is the whole point of letting it choose.
		"a tight wake survives against a long interval": {requested: 7 * time.Minute, want: 7 * time.Minute},
		"an ordinary wake is untouched":                 {requested: 90 * time.Minute, want: 90 * time.Minute},
		"below the floor is raised to it":               {requested: 30 * time.Second, want: 5 * time.Minute},
		"the floor itself is kept":                      {requested: 5 * time.Minute, want: 5 * time.Minute},
		"a week is capped at eight intervals":           {requested: 7 * 24 * time.Hour, want: 24 * time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			if got := clampHeartbeatWake(tt.requested, interval); got != tt.want {
				t.Fatalf("clampHeartbeatWake(%s)=%s want %s", tt.requested, got, tt.want)
			}
		})
	}
}

// What the beat asked for reaches the clock, and the fallback is only for a
// beat that decided nothing -- one that failed, or answered in prose.
func TestNextHeartbeatWakeHonoursTheBeat(t *testing.T) {
	app := &App{
		config: config.Config{Heartbeat: config.HeartbeatConfig{Interval: config.Duration(3 * time.Hour)}},
		now:    func() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) },
	}
	if got := app.nextHeartbeatWake(20 * time.Minute); got != 20*time.Minute {
		t.Fatalf("nextHeartbeatWake=%s want the 20m the beat asked for", got)
	}
	if got := app.nextHeartbeatWake(0); got != 3*time.Hour {
		t.Fatalf("nextHeartbeatWake=%s want the configured interval as the fallback", got)
	}
}

// The window is the owner's, so the snap is computed on their clock. A
// deployment in UTC must not defer a beat to a window boundary that is nine
// hours off.
func TestNextWakeSnapsOnTheOwnerClock(t *testing.T) {
	singapore, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatal(err)
	}
	// 03:00 UTC is 11:00 in Singapore. Plus an hour it is still inside an
	// 08:00-22:00 window on the owner's clock, and outside it on the host's.
	app := &App{
		config: config.Config{Heartbeat: config.HeartbeatConfig{
			Interval:    config.Duration(time.Hour),
			ActiveHours: config.ActiveHours{Start: "08:00", End: "22:00"},
		}},
		location: singapore,
		now:      func() time.Time { return time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC) },
	}
	if got := app.nextHeartbeatWake(0); got != time.Hour {
		t.Fatalf("nextHeartbeatWake=%s want 1h -- the window was read on the host clock", got)
	}
}

// An empty configured instruction falls back to the built-in default rather
// than running a turn with no input at all.
func TestHeartbeatInstructionFallsBackToTheDefault(t *testing.T) {
	app := &App{config: config.Config{Heartbeat: config.HeartbeatConfig{Instruction: "   "}}}
	if got := app.heartbeatInstruction(); got != defaultHeartbeatInstruction {
		t.Fatalf("instruction=%q, want the built-in default", got)
	}
	app.config.Heartbeat.Instruction = "watch the deploy"
	if got := app.heartbeatInstruction(); got != "watch the deploy" {
		t.Fatalf("instruction=%q, want the configured one", got)
	}
}

type stubContextStore struct{ watch string }

func (s *stubContextStore) Load(context.Context) (ports.AgentContext, error) {
	return ports.AgentContext{Watch: s.watch}, nil
}
func (s *stubContextStore) AddEntry(context.Context, ports.ContextDocument, string) error { return nil }
func (s *stubContextStore) ReplaceEntry(context.Context, ports.ContextDocument, string, string) error {
	return nil
}
func (s *stubContextStore) RemoveEntry(context.Context, ports.ContextDocument, string) error {
	return nil
}
func (s *stubContextStore) ReplaceDocument(context.Context, ports.ContextDocument, string) error {
	return nil
}

// An empty watch list means the owner asked for nothing to be watched, so
// there is nothing to check and no model call to justify.
func TestWatchListEmptiness(t *testing.T) {
	for name, tt := range map[string]struct {
		watch string
		empty bool
	}{
		"absent":               {watch: "", empty: true},
		"whitespace":           {watch: "   \n\n\t\n", empty: true},
		"headings only":        {watch: "# Eggy Watch\n\n## Deploys\n", empty: true},
		"one entry":            {watch: "# Eggy Watch\n\nPR #18 open since Aug 20\n", empty: false},
		"entry under headings": {watch: "# Eggy Watch\n\n## Deploys\nRailway settles slowly\n", empty: false},
	} {
		t.Run(name, func(t *testing.T) {
			app := &App{context: &stubContextStore{watch: tt.watch}}
			if got := app.watchListIsEmpty(context.Background()); got != tt.empty {
				t.Fatalf("watchListIsEmpty=%v want %v", got, tt.empty)
			}
		})
	}
}

// A silent no-op is indistinguishable from a broken heartbeat, so the first
// skip says so -- but only the first, or a deployment that never adopts the
// watch list warns every interval forever.
func TestEmptyWatchListWarnsOnce(t *testing.T) {
	app := &App{context: &stubContextStore{watch: "# Eggy Watch\n"}}
	if !app.shouldWarnEmptyWatch() {
		t.Fatal("the first empty-watch skip did not warn")
	}
	if app.shouldWarnEmptyWatch() {
		t.Fatal("a consecutive empty-watch skip warned again")
	}
}

// Re-arming matters: an owner who writes a list, empties it, and forgets
// should be told again.
func TestEmptyWatchWarningRearmsAfterANonEmptyList(t *testing.T) {
	app := &App{context: &stubContextStore{watch: "# Eggy Watch\n"}}
	app.shouldWarnEmptyWatch()
	app.warnedEmptyWatch = false
	if !app.shouldWarnEmptyWatch() {
		t.Fatal("the warning did not re-arm")
	}
}

// A store failure must degrade into a beat rather than into silence: an
// unreadable watch list is a bug to surface, not a reason to stop checking.
func TestUnreadableWatchListStillBeats(t *testing.T) {
	app := &App{context: &failingContextStore{}}
	if app.watchListIsEmpty(context.Background()) {
		t.Fatal("an unreadable watch list suppressed the beat")
	}
}

type failingContextStore struct{ stubContextStore }

func (*failingContextStore) Load(context.Context) (ports.AgentContext, error) {
	return ports.AgentContext{}, errors.New("disk gone")
}

// Quiet hours are the v1 spec's one deferred omission: an interval-only
// heartbeat fires at 03:00, and muting the chat to survive that disables the
// feature.
func TestHeartbeatSkipsOutsideActiveHours(t *testing.T) {
	singapore, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatal(err)
	}
	for name, tt := range map[string]struct {
		hours  config.ActiveHours
		now    time.Time
		active bool
	}{
		"unset window is always active": {now: time.Date(2026, 8, 24, 3, 0, 0, 0, singapore), active: true},
		"inside the window": {
			hours: config.ActiveHours{Start: "08:00", End: "22:00"},
			now:   time.Date(2026, 8, 24, 12, 0, 0, 0, singapore), active: true,
		},
		"the 03:00 beat quiet hours exist to stop": {
			hours: config.ActiveHours{Start: "08:00", End: "22:00"},
			now:   time.Date(2026, 8, 24, 3, 0, 0, 0, singapore),
		},
	} {
		t.Run(name, func(t *testing.T) {
			app := &App{
				config:   config.Config{Heartbeat: config.HeartbeatConfig{ActiveHours: tt.hours}},
				location: singapore,
				now:      func() time.Time { return tt.now },
			}
			if got := app.withinActiveHours(); got != tt.active {
				t.Fatalf("withinActiveHours=%v want %v", got, tt.active)
			}
		})
	}
}

// The window is read in the owner's timezone, not the host's. A deployment in
// UTC serving an owner in Singapore must go quiet on the owner's clock.
func TestActiveHoursUseTheOwnerTimezone(t *testing.T) {
	singapore, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatal(err)
	}
	// 02:00 UTC is 10:00 in Singapore: inside an 08:00-22:00 window on the
	// owner's clock, outside it on the host's.
	app := &App{
		config:   config.Config{Heartbeat: config.HeartbeatConfig{ActiveHours: config.ActiveHours{Start: "08:00", End: "22:00"}}},
		location: singapore,
		now:      func() time.Time { return time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC) },
	}
	if !app.withinActiveHours() {
		t.Fatal("the window was read in the host timezone rather than the owner's")
	}
}
