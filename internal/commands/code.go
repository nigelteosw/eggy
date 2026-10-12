package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

// codeUsage lists every /code form. The subcommands are fixed words, so a
// repository name can never be read as one of them.
const codeUsage = "Usage: /code repos [enable|disable <name>] · /code run <name> <task> · /code status · /code cancel <id> · /code answer <id> <text>"

// CodeRunner is the agent runner as /code sees it. The commands package
// declares it rather than importing the extension; bootstrap adapts the
// service to it. Every method acts for the account on the context.
type CodeRunner interface {
	StartRun(ctx context.Context, repository, task string) (ports.AgentRun, error)
	Runs(ctx context.Context) ([]ports.AgentRun, error)
	CancelRun(ctx context.Context, id string) (ports.AgentRun, error)
	AnswerRun(ctx context.Context, id, text string) (ports.AgentRun, error)
	Repositories(ctx context.Context) ([]CodeRepository, error)
	SetRepositoryEnabled(ctx context.Context, name string, enabled bool) error
}

// CodeRepository is one allowed repository as an account sees it.
type CodeRepository struct {
	Name       string
	Enabled    bool
	PushAccess bool
}

// codeCommand is the direct command surface for coding runs. It acts only
// from a verified chat or the web, and never from Discord, which can read a
// run's status but not start one or enable a repository.
func (s *CommandService) codeCommand(ctx context.Context, args []string) (string, bool, error) {
	if s.Code == nil {
		return "Agent Runner is not enabled.", true, nil
	}
	if len(args) == 0 {
		return codeUsage, true, nil
	}
	switch args[0] {
	case "repos":
		return s.codeRepos(ctx, args[1:])
	case "run":
		if len(args) < 3 {
			return codeUsage, true, nil
		}
		if destination.FromContext(ctx).Kind == destination.Discord {
			return "Agent Runner can't be started from Discord. Start the run from the web panel or Telegram.", true, nil
		}
		run, err := s.Code.StartRun(ctx, args[1], strings.Join(args[2:], " "))
		if err != nil {
			return err.Error(), true, nil
		}
		return fmt.Sprintf("Run %s queued on %s. I'll report back here.", run.ID, run.Repository), true, nil
	case "status":
		runs, err := s.Code.Runs(ctx)
		if err != nil {
			return err.Error(), true, nil
		}
		if len(runs) == 0 {
			return "No runs yet.", true, nil
		}
		var lines []string
		for _, run := range runs {
			lines = append(lines, fmt.Sprintf("%s · %s · %s", run.ID, run.Repository, run.State))
		}
		return strings.Join(lines, "\n"), true, nil
	case "cancel":
		if len(args) != 2 {
			return codeUsage, true, nil
		}
		run, err := s.Code.CancelRun(ctx, args[1])
		if err != nil {
			return err.Error(), true, nil
		}
		return fmt.Sprintf("Run %s is %s.", run.ID, run.State), true, nil
	case "answer":
		if len(args) < 3 {
			return codeUsage, true, nil
		}
		run, err := s.Code.AnswerRun(ctx, args[1], strings.Join(args[2:], " "))
		if err != nil {
			return err.Error(), true, nil
		}
		return fmt.Sprintf("Answer sent; run %s is %s.", run.ID, run.State), true, nil
	default:
		return codeUsage, true, nil
	}
}

func (s *CommandService) codeRepos(ctx context.Context, args []string) (string, bool, error) {
	switch {
	case len(args) == 0:
		repos, err := s.Code.Repositories(ctx)
		if err != nil {
			return err.Error(), true, nil
		}
		if len(repos) == 0 {
			return "No repositories are allowed in config.yaml.", true, nil
		}
		var lines []string
		for _, repo := range repos {
			state := "not enabled"
			if repo.Enabled {
				state = "enabled"
			}
			push := "no push access"
			if repo.PushAccess {
				push = "push access"
			}
			lines = append(lines, fmt.Sprintf("%s · %s · %s", repo.Name, state, push))
		}
		return strings.Join(lines, "\n"), true, nil
	case len(args) == 2 && (args[0] == "enable" || args[0] == "disable"):
		enable := args[0] == "enable"
		if err := s.Code.SetRepositoryEnabled(ctx, args[1], enable); err != nil {
			return err.Error(), true, nil
		}
		if enable {
			return args[1] + " enabled for your account.", true, nil
		}
		return args[1] + " disabled for your account.", true, nil
	default:
		return codeUsage, true, nil
	}
}
