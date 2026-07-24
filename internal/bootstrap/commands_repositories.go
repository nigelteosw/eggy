package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

type repositoryAddPayload struct {
	Name              string
	CloneURL          string
	BaseBranch        string
	ProtectedBranches []string
}

// pendingRepositoryAddNames returns the names of add-repository approvals
// still pending owner approval, excluding any that already made it into
// state (approved between load and now).
func pendingRepositoryAddNames(state ports.State, registered map[string]ports.Repository) []string {
	var names []string
	for _, approval := range state.Approvals {
		if approval.Status != approvals.Pending || approval.Action != approvals.AddRepository {
			continue
		}
		var payload repositoryAddPayload
		if err := json.Unmarshal(approval.Payload, &payload); err != nil || payload.Name == "" {
			continue
		}
		if _, exists := registered[payload.Name]; exists {
			continue
		}
		names = append(names, payload.Name)
	}
	sort.Strings(names)
	return names
}

func handleRepositories(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.repositories == nil {
		return CommandResult{State: ResultInfo, Title: "Repositories are not configured."}, nil
	}
	if len(req.Args) > 0 {
		return usageHelp(mustEntry("repositories"), fmt.Sprintf("Unknown repositories subcommand %q. Use add or remove.", req.Args[0])), nil
	}
	registered, err := s.repositories.List(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	pendingNames := pendingRepositoryAddNames(state, registered)
	if len(registered) == 0 && len(pendingNames) == 0 {
		return CommandResult{
			State: ResultInfo,
			Title: "No repositories configured.",
			Next:  []string{"/repositories add <name> <clone_url> [base_branch] [protected_branches]"},
		}, nil
	}
	names := make([]string, 0, len(registered))
	for name := range registered {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([][]string, 0, len(names))
	for _, name := range names {
		repository := registered[name]
		rows = append(rows, []string{name, repository.BaseBranch, strings.Join(repository.ProtectedBranches, ", ")})
	}
	var lines []string
	for _, name := range pendingNames {
		lines = append(lines, name+" — awaiting owner approval")
	}
	return CommandResult{
		TableHeaders: []string{"Repository", "Base branch", "Protected branches"},
		TableRows:    rows,
		Lines:        lines,
		Next:         []string{"/repositories add <name> <clone_url> [base_branch] [protected_branches]", "/repositories remove <name>"},
	}, nil
}

func handleRepositoriesAdd(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.repositories == nil {
		return CommandResult{State: ResultInfo, Title: "Repositories are not configured."}, nil
	}
	if len(req.Args) < 2 || len(req.Args) > 4 {
		return usageHelp(mustEntry("repositories add"), "Expected <name> <clone_url>, with optional [base_branch] and [protected_branches] (comma-separated)."), nil
	}
	name, cloneURL := req.Args[0], req.Args[1]
	baseBranch := ""
	if len(req.Args) >= 3 {
		baseBranch = req.Args[2]
	}
	var protectedBranches []string
	if len(req.Args) == 4 {
		for _, branch := range strings.Split(req.Args[3], ",") {
			if trimmed := strings.TrimSpace(branch); trimmed != "" {
				protectedBranches = append(protectedBranches, trimmed)
			}
		}
	}
	approval, err := s.repositories.RequestAdd(ctx, name, cloneURL, baseBranch, protectedBranches)
	if err != nil {
		return errorResult(err), nil
	}
	if s.channel != nil {
		if err := s.channel.DeliverApproval(ctx, s.owner, approval); err != nil {
			return CommandResult{}, err
		}
	}
	return CommandResult{
		State:  ResultInfo,
		Title:  "Add request for " + name + " staged, awaiting approval.",
		Detail: "The owner will see an Approve/Reject prompt.",
		Next:   []string{"/repositories"},
	}, nil
}

func handleRepositoriesRemove(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.repositories == nil {
		return CommandResult{State: ResultInfo, Title: "Repositories are not configured."}, nil
	}
	if len(req.Args) != 1 {
		return usageHelp(mustEntry("repositories remove"), "Expected exactly one <name>."), nil
	}
	if err := s.repositories.Remove(ctx, req.Args[0]); err != nil {
		return errorResult(err), nil
	}
	return CommandResult{Title: "Removed " + req.Args[0] + ".", Next: []string{"/repositories"}}, nil
}
