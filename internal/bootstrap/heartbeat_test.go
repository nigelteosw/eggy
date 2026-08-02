package bootstrap

import (
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
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
