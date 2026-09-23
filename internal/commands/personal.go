// /soul and /heartbeat: who Eggy is, and whether it checks in on you.
package commands

import (
	"context"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// SoulReader is the slice of the context store /soul needs. Reading only:
// SOUL.md is changed by asking Eggy or in the web panel, and multi-line prose
// typed on a phone keyboard is what the first of those is for.
type SoulReader interface {
	Load(context.Context) (ports.AgentContext, error)
}

const heartbeatUsage = "Usage: /heartbeat [on|off]"

func (s *CommandService) soulCommand(ctx context.Context) (string, bool, error) {
	if s.Soul == nil {
		return "SOUL.md is unavailable.", true, nil
	}
	agentContext, err := s.Soul.Load(ctx)
	if err != nil {
		return "", true, err
	}
	return "Eggy's soul (SOUL.md, shared by everyone who uses Eggy):\n\n" + strings.TrimSpace(agentContext.Soul) +
		"\n\nTo change it, tell me how you'd like me to behave, or edit it in the web panel (/web).", true, nil
}

// heartbeatCommand reads or sets the acting account's own heartbeat switch.
// Bare /heartbeat reports rather than toggling, for the reason /mode does: a
// tap should never change a setting its sender did not name.
func (s *CommandService) heartbeatCommand(ctx context.Context, args []string) (string, bool, error) {
	if s.AgentRuntime == nil {
		return "Heartbeat is unavailable.", true, nil
	}
	if len(args) > 1 {
		return heartbeatUsage, true, nil
	}
	if len(args) == 0 {
		on, err := s.AgentRuntime.Heartbeat(ctx)
		if err != nil {
			return "", true, err
		}
		if on {
			return s.heartbeatOnMessage() + "\n\nTurn it off with /heartbeat off.", true, nil
		}
		return "Heartbeat is off: Eggy does not message you unprompted.\n\nTurn it on with /heartbeat on.", true, nil
	}
	var on bool
	switch strings.ToLower(args[0]) {
	case "on":
		on = true
	case "off":
	default:
		return heartbeatUsage, true, nil
	}
	if err := s.AgentRuntime.SetHeartbeat(ctx, on); err != nil {
		return "", true, err
	}
	if !on {
		return "Heartbeat off. Eggy will not message you unprompted.", true, nil
	}
	return s.heartbeatOnMessage(), true, nil
}

// heartbeatOnMessage says what an on switch actually does here, including
// when it does nothing yet: the switch is personal, the cadence is the
// deployment's.
func (s *CommandService) heartbeatOnMessage() string {
	if s.HeartbeatCadence == "" {
		return "Heartbeat is on for you, but this deployment has no heartbeat interval set, so nothing runs until one is set in the web panel (Settings → Automation)."
	}
	return "Heartbeat is on. Eggy checks your watch list " + s.HeartbeatCadence + " and messages you only when something needs you. Mention anything you're waiting on and it goes on the list."
}
