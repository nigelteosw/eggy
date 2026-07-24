package bootstrap

import (
	"context"
	"sort"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

func handleRuns(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.coding == nil {
		return CommandResult{State: ResultInfo, Title: "Coding is not configured."}, nil
	}
	sessions, err := s.coding.List(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	if len(sessions) == 0 {
		return CommandResult{
			State:  ResultInfo,
			Title:  "No coding runs.",
			Detail: "An implementation run starts when you ask Eggy to change a configured repository, or with /continue.",
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
		TableHeaders: []string{"Run", "Repository", "Status", "Validation"},
		TableRows:    rows,
		Next:         []string{"/continue <run-id>", "/stop <run-id>"},
	}, nil
}

func handleContinue(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.coding == nil || s.shipping == nil {
		return CommandResult{State: ResultInfo, Title: "Coding is not configured."}, nil
	}
	var runID, instruction string
	if len(req.Args) > 0 {
		runID = req.Args[0]
		instruction = strings.TrimSpace(strings.TrimPrefix(req.Tail, runID))
	}
	if instruction == "" {
		instruction = "Continue the approved task, inspect the current state, and complete the next safe implementation step."
	}
	var run ports.ImplementationSession
	var result ports.CodingResult
	var err error
	if runID == "" {
		run, result, err = s.coding.ResumeLatest(ctx, instruction, nil)
	} else {
		run, result, err = s.coding.Resume(ctx, runID, instruction, nil)
	}
	if err != nil {
		return errorResult(err), nil
	}
	pr, note, err := s.shipping.Ship(ctx, run.ID, run.Branch, result.CommitMessage)
	if err != nil {
		return errorResult(err), nil
	}
	if note != "" {
		return CommandResult{
			Title:  "Implementation session " + run.ID,
			Detail: note,
		}, nil
	}
	_ = s.coding.Cleanup(ctx, run.ID)
	return CommandResult{
		Title:  "Implementation session " + run.ID + " shipped",
		Fields: []ResultField{{Label: "Pull request", Value: pr.URL}},
	}, nil
}

func handleStop(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.coding == nil {
		return CommandResult{State: ResultInfo, Title: "Coding is not configured."}, nil
	}
	if len(req.Args) != 1 {
		return usageHelp(mustEntry("stop"), "Expected exactly one <run-id>."), nil
	}
	if err := s.coding.Stop(req.Args[0]); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{Title: "Stop requested for " + req.Args[0] + ".", Next: []string{"/runs"}}, nil
}
