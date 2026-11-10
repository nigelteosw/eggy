// /mode: how much the owner is asked. strict gates every tool call, normal
// gates what writes, auto gates nothing. The wording lives here because both
// this command and /status report it, and they must not describe the same
// state two different ways.
package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// modeCommand reads or sets how much the owner is asked.
//
// Bare /mode reports rather than cycling. A toggle was defensible when there
// were two states; with three, a tap that advances to whichever comes next is
// a way to end up in auto without meaning to, and auto is the one state nobody
// should reach by accident.
func (s *CommandService) modeCommand(ctx context.Context, argument string) (string, bool, error) {
	if s.Approvals == nil {
		return "Approvals are unavailable.", true, nil
	}
	current, err := s.Approvals.Mode(ctx)
	if err != nil {
		return "", true, err
	}
	requested := ports.ApprovalMode(strings.ToLower(strings.TrimSpace(argument)))
	if requested == "" {
		return ModeReport(current), true, nil
	}
	if !requested.Valid() {
		return fmt.Sprintf("%q is not a mode. Use /mode strict, /mode normal or /mode auto.", argument), true, nil
	}
	if err := s.Approvals.SetMode(ctx, requested); err != nil {
		return "", true, err
	}
	return ModeMessage(requested), true, nil
}

// ModeMessage is the one wording for a mode's meaning, so Telegram and the web
// panel cannot describe the same setting two different ways. The names are the
// panel's: Every time, Before changes, Never.
func ModeMessage(mode ports.ApprovalMode) string {
	return "When I ask: " + ModeLabel(mode) + ". " + mode.Describe()
}

// ModeLabel is a mode's name in the owner's words, as the panel shows it.
func ModeLabel(mode ports.ApprovalMode) string {
	switch mode {
	case ports.ModeStrict:
		return "Every time"
	case ports.ModeAuto:
		return "Never"
	default:
		return "Before changes"
	}
}

// ModeReport is what a bare /mode says: the mode and the way out of it.
func ModeReport(mode ports.ApprovalMode) string {
	others := make([]string, 0, 2)
	for _, candidate := range []ports.ApprovalMode{ports.ModeStrict, ports.ModeNormal, ports.ModeAuto} {
		if candidate != mode {
			others = append(others, "/mode "+string(candidate))
		}
	}
	return ModeMessage(mode) + "\n\nChange it with " + strings.Join(others, " or ") + "."
}
