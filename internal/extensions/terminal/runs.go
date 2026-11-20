package terminal

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
)

// runLabel names the agent run a terminal was opened in.
const runLabel = "eggy.run"

var runID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Run is one of the account's agent runs as the terminal shows it.
type Run struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	State      string `json:"state"`
	Brief      string `json:"brief"`
}

// checkout is a run's working tree in the account's workspace. A run from
// before the shared workspace has none here, and is not listed.
func (c *core) checkout(id string) string {
	return filepath.Join(c.dir, "runs", id, "ws")
}

func (c *core) hasCheckout(id string) bool {
	info, err := os.Stat(c.checkout(id))
	return err == nil && info.IsDir()
}

// runs lists the account's runs with a checkout in its workspace. A failure
// to list them is logged and shows no runs: the terminals still work.
func (s *Service) runs(ctx context.Context, c *core) []Run {
	if s.opts.Runs == nil {
		return nil
	}
	all, err := s.opts.Runs(ctx)
	if err != nil {
		s.opts.Logger.Warn("terminal: agent runs not listed", "error", err)
		return nil
	}
	var out []Run
	for _, r := range all {
		if runID.MatchString(r.ID) && spaceName.MatchString(r.Repository) && c.hasCheckout(r.ID) {
			out = append(out, r)
		}
	}
	return out
}

// addRuns puts each run under its repository's space.
func (s *Service) addRuns(ctx context.Context, c *core, set spaceSet) {
	for _, r := range s.runs(ctx, c) {
		sp := set.get(r.Repository)
		sp.Runs = append(sp.Runs, r)
	}
}

// StartInRun starts command in one of the account's runs' checkout. The run
// is looked up among the account's own, so another account's run id is not
// found. The terminal is the account's like any other: the core is already
// confined to the account's directory, runs included, and it is listed
// under the run's repository space.
func (s *Service) StartInRun(ctx context.Context, id, command string, cols, rows uint16) (Terminal, error) {
	argv, err := argvFor(command)
	if err != nil {
		return Terminal{}, err
	}
	if !runID.MatchString(id) {
		return Terminal{}, ErrNotFound
	}
	c, err := s.coreFor(ctx)
	if err != nil {
		return Terminal{}, err
	}
	for _, r := range s.runs(ctx, c) {
		if r.ID == id {
			labels := map[string]string{spaceLabel: r.Repository, commandLabel: command, runLabel: id}
			return s.start(ctx, c, argv, c.checkout(id), labels, cols, rows)
		}
	}
	return Terminal{}, ErrNotFound
}
