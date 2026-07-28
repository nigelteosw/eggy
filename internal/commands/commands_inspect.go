package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
)

// The two inspection commands. Both render a report the kernel measured
// (services.Diagnostics) rather than computing anything here: a diagnostic
// that re-derives what a turn does is a diagnostic that can disagree with it.

func handleCapabilities(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.diagnostics == nil {
		return CommandResult{State: ResultInfo, Title: "Diagnostics are not available in this environment."}, nil
	}
	report, err := s.diagnostics.CapabilityReport(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	model := report.ActiveModel
	if model == "" {
		model = "unconfigured"
	}
	if report.ReasoningEffort != "" {
		model += " (effort: " + report.ReasoningEffort + ")"
	}
	self := report.SelfRepository
	if self == "" {
		self = "none"
	}
	return CommandResult{
		Title: "Eggy capabilities",
		Fields: []ResultField{
			{Label: "Reasoning model", Value: model},
			{Label: "Repositories", Value: listOrNone(report.Repositories)},
			{Label: "Self repository", Value: self},
			{Label: "Integrations", Value: listOrNone(report.Integrations)},
			{Label: "Calendar", Value: readiness(report.CalendarEnabled)},
			{Label: "Commit ready", Value: readiness(report.RepositoryCommitReady)},
			{Label: "Push ready", Value: readiness(report.RepositoryPushReady)},
			{Label: "Pull request ready", Value: readiness(report.PullRequestReady)},
			{Label: "Tools", Value: fmt.Sprintf("%d", len(report.Tools))},
		},
		Lines: report.Tools,
		Next:  []string{"/context"},
	}, nil
}

func handleContext(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.diagnostics == nil {
		return CommandResult{State: ResultInfo, Title: "Diagnostics are not available in this environment."}, nil
	}
	report, err := s.diagnostics.ContextReport(ctx, destination.FromContext(ctx).ConversationID())
	if err != nil {
		return CommandResult{}, err
	}
	rows := make([][]string, 0, len(report.Sections))
	for _, section := range report.Sections {
		budget := "—"
		if section.MaxBytes > 0 {
			budget = fmt.Sprintf("%d%% of %d", int64(section.Bytes)*100/section.MaxBytes, section.MaxBytes)
		}
		rows = append(rows, []string{section.Name, fmt.Sprintf("%d", section.Bytes), fmt.Sprintf("~%d", estimatedTokens(section.Bytes)), budget})
	}
	resident := report.ResidentBytes()
	rows = append(rows, []string{"total", fmt.Sprintf("%d", resident), fmt.Sprintf("~%d", estimatedTokens(resident)), "—"})
	return CommandResult{
		Title: "Context in this conversation",
		// The budget is stated rather than subtracted from the total above:
		// it bounds only what the loop appends during a turn, so "resident
		// minus budget" would be an arithmetic result with no meaning.
		Detail:       "Token counts are estimates (4 bytes per token), not the provider's own count. No provider context limit is configured; the loop budget below is what actually triggers compaction.",
		TableHeaders: []string{"Section", "Bytes", "Tokens", "Budget used"},
		TableRows:    rows,
		Fields: []ResultField{
			{Label: "Recent history messages", Value: fmt.Sprintf("%d", report.RecentMessages)},
			{Label: "Loop budget (chars)", Value: fmt.Sprintf("%d, compacts past it", report.BudgetChars)},
			{Label: "Live tool steps", Value: fmt.Sprintf("%d, older steps fold into a summary", report.RecentSteps)},
			{Label: "Output truncated at", Value: fmt.Sprintf("%d chars per message", report.OutputExcerptChars)},
			{Label: "Runaway guard", Value: fmt.Sprintf("%d tool steps per turn", report.MaxSteps)},
		},
		Next: []string{"/clear", "/memory"},
	}, nil
}

// estimatedTokens is the crude 4-bytes-per-token approximation, labelled as
// an estimate everywhere it surfaces. Eggy has no tokenizer, and adding one
// per provider to answer a diagnostic is not worth the dependency.
func estimatedTokens(bytes int) int { return bytes / 4 }

func readiness(ready bool) string {
	if ready {
		return "ready"
	}
	return "unavailable"
}

func listOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}
