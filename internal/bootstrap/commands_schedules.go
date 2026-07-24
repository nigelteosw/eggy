package bootstrap

import (
	"context"
	"sort"
)

func handleSchedules(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	if len(state.Schedules) == 0 {
		return CommandResult{
			State:  ResultInfo,
			Title:  "No schedules.",
			Detail: "Ask Eggy in conversation to schedule an instruction, e.g. \"remind me every morning at 9am to check email.\"",
		}, nil
	}
	ids := make([]string, 0, len(state.Schedules))
	for id := range state.Schedules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rows := make([][]string, 0, len(ids))
	for _, id := range ids {
		schedule := state.Schedules[id]
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
