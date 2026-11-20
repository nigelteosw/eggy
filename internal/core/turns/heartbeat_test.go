package turns

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

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

func TestHeartbeatScheduleHonoursTheBeat(t *testing.T) {
	schedule := HeartbeatSchedule{
		Interval: 3 * time.Hour,
		Now:      func() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) },
	}
	if got := schedule.NextWake(20 * time.Minute); got != 20*time.Minute {
		t.Fatalf("NextWake=%s want the 20m the beat asked for", got)
	}
	if got := schedule.NextWake(0); got != 3*time.Hour {
		t.Fatalf("NextWake=%s want the configured interval as the fallback", got)
	}
	if got := (HeartbeatSchedule{}).NextWake(0); got != 0 {
		t.Fatalf("NextWake=%s want 0 for an unset interval", got)
	}
	if !(HeartbeatSchedule{}).Active() {
		t.Fatal("a schedule with no window must always be active")
	}
}

func TestHeartbeatInstructionFallsBackToTheDefault(t *testing.T) {
	if got := HeartbeatInstruction("   "); got != DefaultHeartbeatInstruction {
		t.Fatalf("instruction=%q, want the built-in default", got)
	}
	if got := HeartbeatInstruction("watch the deploy"); got != "watch the deploy" {
		t.Fatalf("instruction=%q, want the configured one", got)
	}
}

func TestHeartbeatEligibility(t *testing.T) {
	for name, tt := range map[string]struct {
		on       bool
		stateErr error
		watch    string
		watchErr error
		want     bool
	}{
		"switched on with an entry":         {on: true, watch: "# Eggy Watch\n\nPR #18 open\n", want: true},
		"entry under headings":              {on: true, watch: "# Eggy Watch\n\n## Deploys\nRailway settles slowly\n", want: true},
		"switched off":                      {watch: "PR #18 open\n"},
		"absent watch list":                 {on: true},
		"whitespace":                        {on: true, watch: "   \n\n\t\n"},
		"headings only":                     {on: true, watch: "# Eggy Watch\n\n## Deploys\n"},
		"unreadable switch reads as off":    {on: true, stateErr: errors.New("disk"), watch: "PR #18 open\n"},
		"unreadable watch list still beats": {on: true, watchErr: errors.New("disk"), want: true},
	} {
		t.Run(name, func(t *testing.T) {
			state := heartbeatStateStore{on: tt.on, err: tt.stateErr}
			watch := heartbeatContextStore{watch: tt.watch, err: tt.watchErr}
			if got := HeartbeatEligible(context.Background(), state, watch); got != tt.want {
				t.Fatalf("HeartbeatEligible=%v want %v", got, tt.want)
			}
		})
	}
	if HeartbeatEligible(context.Background(), nil, heartbeatContextStore{watch: "x"}) {
		t.Fatal("no state store must read as switched off")
	}
	if HeartbeatEligible(context.Background(), heartbeatStateStore{on: true}, nil) {
		t.Fatal("no context store must read as nothing to watch")
	}
}

type heartbeatStateStore struct {
	on  bool
	err error
}

func (s heartbeatStateStore) Load(context.Context) (ports.State, error) {
	var state ports.State
	state.Agent.Heartbeat = s.on
	return state, s.err
}

func (s heartbeatStateStore) Update(context.Context, uint64, func(*ports.State) error) (ports.State, error) {
	return ports.State{}, errors.New("not used")
}

type heartbeatContextStore struct {
	ports.ContextStore
	watch string
	err   error
}

func (s heartbeatContextStore) Load(context.Context) (ports.AgentContext, error) {
	return ports.AgentContext{Watch: s.watch}, s.err
}
