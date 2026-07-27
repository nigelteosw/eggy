package bootstrap

import (
	"context"
	"sort"
)

func handleRuns(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.sessions == nil {
		return CommandResult{State: ResultInfo, Title: "Coding is not configured."}, nil
	}
	sessions, err := s.sessions.Runs(ctx)
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
