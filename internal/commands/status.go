package commands

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nigelteosw/eggy/internal/core/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// statusListLimit bounds every list /status prints. The report is read on a
// phone, so a long queue is named in part and counted, and /web holds the rest.
const statusListLimit = 5

// mcpAttentionLimit bounds the servers named as needing attention: a broken
// fleet stays one line.
const mcpAttentionLimit = 3

// status is /status: one short report, each section read on its own. A read
// that fails marks its section unavailable rather than failing the command,
// because /status is what the owner reaches for when something is wrong.
// Integrations that are not configured are left out.
func (s *CommandService) status(ctx context.Context) (string, bool, error) {
	lines := []string{"**Eggy status**", ""}
	lines = append(lines, s.statusModel(ctx))
	lines = append(lines, s.statusApprovals(ctx)...)
	if s.RestartPending != nil && s.RestartPending() {
		lines = append(lines, "**Waiting for a restart:** a config change is not live yet. `/restart` applies it.")
	}
	// The mode is always reported now that there are three of them. A bypass
	// the owner switched on days ago and forgot is still the failure mode worth
	// the line, but with a middle rung "no news" no longer means one specific
	// state, so saying nothing would leave the owner guessing which.
	if s.Approvals != nil {
		mode, err := s.Approvals.Mode(ctx)
		if err != nil {
			lines = append(lines, "**Approvals:** unavailable")
		} else {
			line := "**Approvals:** " + ModeMessage(mode)
			if mode == ports.ModeAuto {
				line += " `/mode normal` restores the gate."
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, s.statusMCP()...)
	lines = append(lines, s.statusGoogle()...)
	return strings.Join(lines, "\n"), true, nil
}

// statusModel is the calling account's model and how it reasons.
func (s *CommandService) statusModel(ctx context.Context) string {
	if s.AgentRuntime == nil {
		return "**Active model:** unconfigured"
	}
	model, err := s.AgentRuntime.SelectedModel(ctx)
	if err != nil {
		return "**Active model:** unavailable"
	}
	if model == "" {
		model = "unconfigured"
	}
	line := "**Active model:** " + model
	if effort, err := s.AgentRuntime.ReasoningEffort(ctx); err == nil {
		if effort == "" {
			effort = "default"
		}
		line += " · effort " + effort
	}
	if shown, err := s.AgentRuntime.ShowThinking(ctx); err == nil {
		if shown {
			line += " · thinking shown"
		} else {
			line += " · thinking hidden"
		}
	}
	return line
}

// statusApprovals names what is waiting on the calling account, oldest first.
// The store keeps each account's approvals apart, so this reads only the
// caller's. An approval past its expiry is treated as the approval service
// treats it -- it can no longer be approved -- so it is counted separately
// rather than listed as waiting.
func (s *CommandService) statusApprovals(ctx context.Context) []string {
	if s.Store == nil {
		return []string{"**Pending approvals:** 0"}
	}
	state, err := s.Store.Load(ctx)
	if err != nil {
		return []string{"**Pending approvals:** unavailable"}
	}
	now := s.Now()
	var waiting []approvals.Approval
	expired := 0
	for _, approval := range state.Approvals {
		if approval.Status != approvals.Pending {
			continue
		}
		if !now.Before(approval.ExpiresAt) {
			expired++
			continue
		}
		waiting = append(waiting, approval)
	}
	// Oldest first, with the ID breaking ties: state stores approvals in a
	// map, so without both the order would differ between two runs.
	slices.SortFunc(waiting, func(a, b approvals.Approval) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	lines := []string{fmt.Sprintf("**Pending approvals:** %d", len(waiting))}
	for i, approval := range waiting {
		if i == statusListLimit {
			lines = append(lines, fmt.Sprintf("- …and %d more. /web shows them all.", len(waiting)-statusListLimit))
			break
		}
		summary := approval.Summary
		if strings.TrimSpace(summary) == "" {
			summary = string(approval.Action)
		}
		lines = append(lines, fmt.Sprintf("- %s (`%s`, requested %s)", summary, approval.ID, approval.CreatedAt.Format("2 Jan 15:04")))
	}
	if expired > 0 {
		lines = append(lines, fmt.Sprintf("%d expired without a decision; ask again if it still matters.", expired))
	}
	return lines
}

// statusMCP is the shared servers' readiness, or nothing when none is
// configured.
func (s *CommandService) statusMCP() []string {
	if s.MCP == nil {
		return nil
	}
	statuses := s.MCP.Statuses()
	if len(statuses) == 0 {
		return nil
	}
	ready, tools := 0, 0
	var attention []string
	for _, status := range statuses {
		tools += status.Tools
		if status.State == mcpStateReady {
			ready++
			continue
		}
		attention = append(attention, status.Name+" ("+status.State+")")
	}
	lines := []string{fmt.Sprintf("**MCP:** %d/%d ready, %d tools", ready, len(statuses), tools)}
	if len(attention) > 0 {
		slices.Sort(attention)
		line := "**Needs attention:** " + strings.Join(attention[:min(len(attention), mcpAttentionLimit)], ", ")
		if extra := len(attention) - mcpAttentionLimit; extra > 0 {
			line += fmt.Sprintf(", and %d more. `/mcp` lists them.", extra)
		}
		lines = append(lines, line)
	}
	return lines
}

// statusGoogle is the shared Google connection in one line, with the command
// that fixes it when it needs fixing, or nothing when Google is not
// configured. /google status has the detail.
func (s *CommandService) statusGoogle() []string {
	if s.Google == nil {
		return nil
	}
	status, err := s.Google.Status()
	switch {
	case err != nil:
		return []string{"**Google:** unavailable. `/google status` shows why."}
	case !status.Authorized:
		return []string{"**Google:** not connected. `/google login` connects it."}
	case status.ExpectedEmail != "" && status.Email != "" && status.Email != status.ExpectedEmail:
		return []string{"**Google:** ⚠ connected as " + status.Email + ", expected " + status.ExpectedEmail + ". `/google login` reconnects as Eggy."}
	case status.Email != "":
		return []string{"**Google:** connected as " + status.Email + " (shared with all Eggy users)"}
	default:
		return []string{"**Google:** connected; identity not yet verified"}
	}
}
