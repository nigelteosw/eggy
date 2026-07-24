package bootstrap

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

func handleStatus(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	names := make([]string, 0, len(state.Repositories))
	for name := range state.Repositories {
		names = append(names, name)
	}
	sort.Strings(names)
	repositories := "none"
	if len(names) > 0 {
		repositories = strings.Join(names, ", ")
	}
	pending := 0
	for _, approval := range state.Approvals {
		if approval.Status == approvals.Pending {
			pending++
		}
	}
	active := 0
	if s.coding != nil {
		sessions, err := s.coding.List(ctx)
		if err != nil {
			return CommandResult{}, err
		}
		for _, session := range sessions {
			if session.Phase == ports.PhaseRunning {
				active++
			}
		}
	}
	model := "unconfigured"
	if s.agentRuntime != nil {
		if selected, err := s.agentRuntime.SelectedModel(ctx); err == nil && selected != "" {
			model = selected
		}
	}
	var next []string
	if len(names) == 0 {
		next = append(next, "/repositories add <name> <clone_url> [base_branch] [protected_branches]")
	}
	if pending > 0 {
		next = append(next, "/repositories")
	}
	if active > 0 {
		next = append(next, "/runs")
	}
	return CommandResult{
		Title: "Eggy status",
		Fields: []ResultField{
			{Label: "Repositories", Value: repositories},
			{Label: "Active runs", Value: fmt.Sprintf("%d", active)},
			{Label: "Pending approvals", Value: fmt.Sprintf("%d", pending)},
			{Label: "Schedules", Value: fmt.Sprintf("%d", len(state.Schedules))},
			{Label: "Active model", Value: model},
		},
		Next: next,
	}, nil
}

func handleStart(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	return CommandResult{
		Title:  "Hey, I'm Eggy",
		Detail: "Your personal AI assistant! I can chat, manage code repositories, schedule reminders, and more.\n\n" + HelpText("") + "\n\nType /help <command> for detailed usage on any command.",
	}, nil
}

func handleHelp(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	command := ""
	if len(req.Args) > 0 {
		command = req.Args[0]
	}
	return CommandResult{Detail: HelpText(command)}, nil
}

func handleRestart(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.restart == nil {
		return CommandResult{State: ResultInfo, Title: "Restart is not available in this environment."}, nil
	}
	s.restart()
	return CommandResult{
		Title:  "Restarting Eggy to pick up config changes. Back in a few seconds.",
		Detail: "Any active implementation session is interrupted safely and can be resumed with /continue once Eggy is back.",
	}, nil
}
