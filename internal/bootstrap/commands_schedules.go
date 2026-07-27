package bootstrap

import (
	"context"

	"github.com/nigelteosw/eggy/internal/ports"
)

func handleSchedules(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	var schedules []ports.Schedule
	if s.schedules != nil {
		loaded, err := s.schedules.List(ctx)
		if err != nil {
			return CommandResult{}, err
		}
		schedules = loaded
	}
	if len(schedules) == 0 {
		return CommandResult{
			State:  ResultInfo,
			Title:  "No schedules.",
			Detail: "Ask Eggy in conversation to schedule an instruction, e.g. \"remind me every morning at 9am to check email.\"",
		}, nil
	}
	// cronfile.Store already lists by id, so the rows come out stable.
	rows := make([][]string, 0, len(schedules))
	for _, schedule := range schedules {
		enabled := "yes"
		if !schedule.Enabled {
			enabled = "no"
		}
		nextRun := "—"
		if !schedule.NextRun.IsZero() {
			nextRun = schedule.NextRun.Format("2006-01-02 15:04 MST")
		}
		rows = append(rows, []string{schedule.Instruction, nextRun, enabled})
	}
	timezone := s.timezone
	if timezone == "" {
		timezone = "UTC"
	}
	return CommandResult{
		TableHeaders: []string{"Instruction", "Next run", "Enabled"},
		TableRows:    rows,
		Fields:       []ResultField{{Label: "Owner timezone", Value: timezone}},
	}, nil
}
