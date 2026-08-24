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
			ticks, stop := app.heartbeatTicks()
			defer stop()
			if (ticks != nil) != tt.wantTick {
				t.Fatalf("ticker started=%v, want %v", ticks != nil, tt.wantTick)
			}
			if !tt.wantTick {
				return
			}
			select {
			case <-ticks:
			case <-time.After(time.Second):
				t.Fatal("a configured heartbeat never fired")
			}
		})
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
