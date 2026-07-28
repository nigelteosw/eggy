package commands

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func handleRuns(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.changes == nil {
		return CommandResult{State: ResultInfo, Title: "Coding is not configured."}, nil
	}
	sessions, err := s.changes.List(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	if len(sessions) == 0 {
		return CommandResult{
			State:  ResultInfo,
			Title:  "No coding sessions.",
			Detail: "A session starts when Eggy branches this conversation's workspace to change a configured repository.",
		}, nil
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	rows := make([][]string, 0, len(sessions))
	for _, session := range sessions {
		validation := session.Validation
		if validation == "" {
			validation = "—"
		}
		rows = append(rows, []string{session.ID, session.Repository, string(session.Phase), validation})
	}
	return CommandResult{
		TableHeaders: []string{"Session", "Repository", "Status", "Validation"},
		TableRows:    rows,
		Next:         []string{"/stop"},
	}, nil
}

// handleRunsShow reports one run in full. Everything shown is read straight
// off the persisted ports.Change: a run does not record a per-run model alias
// or a provider session ID, so neither is invented here.
func handleRunsShow(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.changes == nil {
		return CommandResult{State: ResultInfo, Title: "Coding is not configured."}, nil
	}
	if len(req.Args) == 0 {
		return usageHelp(mustEntry("runs show"), "Name the run to show."), nil
	}
	sessions, err := s.changes.List(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	id := req.Args[0]
	index := slices.IndexFunc(sessions, func(session ports.Change) bool { return session.ID == id })
	if index < 0 {
		return CommandResult{State: ResultError, Title: fmt.Sprintf("No run %q.", id), Next: []string{"/runs"}}, nil
	}
	session := sessions[index]
	fields := []ResultField{
		{Label: "Repository", Value: session.Repository},
		{Label: "Branch", Value: orDash(session.Branch)},
		{Label: "Base revision", Value: orDash(session.BaseRevision)},
		{Label: "Phase", Value: string(session.Phase)},
		{Label: "Validation", Value: orDash(session.Validation)},
		{Label: "Elapsed", Value: elapsed(session, s.now)},
		{Label: "Started", Value: session.StartedAt.Format(time.RFC3339)},
		{Label: "Updated", Value: session.UpdatedAt.Format(time.RFC3339)},
		{Label: "Trigger", Value: trigger(session.Unprompted)},
	}
	if session.Commit != "" {
		fields = append(fields, ResultField{Label: "Commit", Value: session.Commit})
	}
	if session.PullRequestURL != "" {
		fields = append(fields, ResultField{Label: "Pull request", Value: fmt.Sprintf("#%d %s", session.PullRequestNumber, session.PullRequestURL)})
	}
	if session.ChecksConclusion != "" {
		fields = append(fields, ResultField{Label: "Checks", Value: session.ChecksConclusion})
	}
	return CommandResult{Title: "Run " + session.ID, Fields: fields, Next: []string{"/runs"}}, nil
}

// elapsed is the run's wall-clock duration: to FinishedAt once it has one,
// and to now while it is still open. An unset clock yields a dash rather than
// a duration measured from the zero time.
func elapsed(session ports.Change, now func() time.Time) string {
	if session.StartedAt.IsZero() {
		return "—"
	}
	end := session.FinishedAt
	if end.IsZero() {
		if now == nil {
			return "—"
		}
		end = now()
	}
	return end.Sub(session.StartedAt).Round(time.Second).String()
}

func trigger(unprompted bool) string {
	if unprompted {
		return "unprompted (scheduled turn; proposes only)"
	}
	return "owner"
}

func orDash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

// handleStop cancels the turn running in this conversation. There is no run
// ID to name any more: continuing an unfinished change is ordinary
// conversation against a thread whose workspace is still open.
func handleStop(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.turns == nil {
		return CommandResult{State: ResultInfo, Title: "Nothing to stop."}, nil
	}
	if !s.turns.Stop(ctx) {
		return CommandResult{State: ResultInfo, Title: "Nothing is running in this conversation."}, nil
	}
	return CommandResult{
		Title:  "Stopping.",
		Detail: "The workspace is left as it was; ask me to continue when you want to pick it back up.",
		Next:   []string{"/runs"},
	}, nil
}
