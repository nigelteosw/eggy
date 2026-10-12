package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// agentRunnerSchema is applied on every open with the other schemas. Both
// tables are additive, so an older binary ignores them and no
// MachineStateVersion bump is needed. The table exists whether or not the
// extension is enabled, so turning it off never loses a run's record.
const agentRunnerSchema = `
CREATE TABLE IF NOT EXISTS agent_runs (
  id                 TEXT PRIMARY KEY,
  account_id         TEXT NOT NULL,
  repository         TEXT NOT NULL,
  brief              TEXT NOT NULL,
  brief_digest       TEXT NOT NULL,
  base_branch        TEXT NOT NULL,
  base_sha           TEXT NOT NULL DEFAULT '',
  branch             TEXT NOT NULL,
  head_sha           TEXT NOT NULL DEFAULT '',
  state              TEXT NOT NULL,
  revision           INTEGER NOT NULL DEFAULT 0,
  stage              TEXT NOT NULL DEFAULT '',
  pat_generation     INTEGER NOT NULL,
  mandate_expires    TEXT NOT NULL,
  question           TEXT NOT NULL DEFAULT '',
  answer             TEXT NOT NULL DEFAULT '',
  used_tokens        INTEGER NOT NULL DEFAULT 0,
  used_active_ns     INTEGER NOT NULL DEFAULT 0,
  output             TEXT NOT NULL DEFAULT '',
  pr_url             TEXT NOT NULL DEFAULT '',
  failure            TEXT NOT NULL DEFAULT '',
  origin_channel     TEXT NOT NULL DEFAULT '',
  origin_destination TEXT NOT NULL DEFAULT '',
  delivered          INTEGER NOT NULL DEFAULT 0,
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS agent_runs_account_created ON agent_runs (account_id, created_at);
CREATE INDEX IF NOT EXISTS agent_runs_state_created ON agent_runs (state, created_at);
CREATE TABLE IF NOT EXISTS agent_runner_repos (
  account_id TEXT NOT NULL,
  repository TEXT NOT NULL,
  PRIMARY KEY (account_id, repository)
);
`

const agentRunColumns = `id, repository, brief, brief_digest, base_branch, base_sha, branch, head_sha, state, revision, stage, pat_generation, mandate_expires, question, answer, used_tokens, used_active_ns, output, pr_url, failure, origin_channel, origin_destination, delivered, created_at, updated_at`

func scanAgentRun(scan func(...any) error) (ports.AgentRun, error) {
	var (
		run                   ports.AgentRun
		state, stage          string
		expires, created, upd string
		usedActiveNS          int64
		delivered             int64
	)
	if err := scan(&run.AccountID, &run.ID, &run.Repository, &run.Brief, &run.BriefDigest, &run.BaseBranch, &run.BaseSHA, &run.Branch, &run.HeadSHA,
		&state, &run.Revision, &stage, &run.PATGeneration, &expires, &run.Question, &run.Answer, &run.UsedTokens, &usedActiveNS,
		&run.Output, &run.PRURL, &run.Failure, &run.OriginChannel, &run.OriginDestination, &delivered, &created, &upd); err != nil {
		return ports.AgentRun{}, err
	}
	var err error
	if run.MandateExpires, err = parseTime(expires); err != nil {
		return ports.AgentRun{}, fmt.Errorf("agent run %s mandate_expires: %w", run.ID, err)
	}
	if run.CreatedAt, err = parseTime(created); err != nil {
		return ports.AgentRun{}, fmt.Errorf("agent run %s created_at: %w", run.ID, err)
	}
	if run.UpdatedAt, err = parseTime(upd); err != nil {
		return ports.AgentRun{}, fmt.Errorf("agent run %s updated_at: %w", run.ID, err)
	}
	run.State = ports.AgentRunState(state)
	run.Stage = ports.PublishStage(stage)
	run.UsedActive = time.Duration(usedActiveNS)
	run.Delivered = delivered != 0
	return run, nil
}

// agentRunSelect adds the owner, which reads come back with and writes never set.
const agentRunSelect = `account_id, ` + agentRunColumns

// agentRunArgs lists the values in agentRunColumns order, for INSERT and UPDATE.
func agentRunArgs(run ports.AgentRun) []any {
	delivered := 0
	if run.Delivered {
		delivered = 1
	}
	return []any{run.ID, run.Repository, run.Brief, run.BriefDigest, run.BaseBranch, run.BaseSHA, run.Branch, run.HeadSHA,
		string(run.State), run.Revision, string(run.Stage), run.PATGeneration, formatTime(run.MandateExpires), run.Question, run.Answer,
		run.UsedTokens, int64(run.UsedActive), run.Output, run.PRURL, run.Failure, run.OriginChannel, run.OriginDestination, delivered,
		formatTime(run.CreatedAt), formatTime(run.UpdatedAt)}
}

// CreateAgentRun stores a new run for the acting account.
func (s *Store) CreateAgentRun(ctx context.Context, run ports.AgentRun) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	// 25 run columns plus account_id.
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", 26), ", ")
	args := append([]any{account}, agentRunArgs(run)...)
	_, err = s.db.ExecContext(ctx, `INSERT INTO agent_runs (account_id, `+agentRunColumns+`) VALUES (`+placeholders+`)`, args...)
	if err != nil {
		return fmt.Errorf("create agent run: %w", err)
	}
	return nil
}

// GetAgentRun reads one of the acting account's runs.
func (s *Store) GetAgentRun(ctx context.Context, id string) (ports.AgentRun, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return ports.AgentRun{}, err
	}
	run, err := scanAgentRun(s.db.QueryRowContext(ctx, `SELECT `+agentRunSelect+` FROM agent_runs WHERE id = ? AND account_id = ?`, id, account).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.AgentRun{}, ports.ErrAgentRunNotFound
	}
	return run, err
}

// ListAgentRuns returns the acting account's newest runs first.
func (s *Store) ListAgentRuns(ctx context.Context, limit int) ([]ports.AgentRun, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+agentRunSelect+` FROM agent_runs WHERE account_id = ? ORDER BY created_at DESC LIMIT ?`, account, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []ports.AgentRun
	for rows.Next() {
		run, err := scanAgentRun(rows.Scan)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// UpdateAgentRun writes the run only if the stored revision is still the one
// the caller read. When it is not, the answer tells a stale caller apart from
// a run that no longer exists. created_at is never rewritten.
func (s *Store) UpdateAgentRun(ctx context.Context, run ports.AgentRun) (ports.AgentRun, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return ports.AgentRun{}, err
	}
	expected := run.Revision
	run.Revision++
	run.UpdatedAt = time.Now().UTC()
	// agentRunArgs order: id, then the writable columns, then created_at, then updated_at.
	all := agentRunArgs(run)
	setArgs := append(append([]any{}, all[1:23]...), all[24])
	result, err := s.db.ExecContext(ctx, `UPDATE agent_runs SET repository = ?, brief = ?, brief_digest = ?, base_branch = ?, base_sha = ?, branch = ?, head_sha = ?,
		state = ?, revision = ?, stage = ?, pat_generation = ?, mandate_expires = ?, question = ?, answer = ?, used_tokens = ?, used_active_ns = ?,
		output = ?, pr_url = ?, failure = ?, origin_channel = ?, origin_destination = ?, delivered = ?, updated_at = ?
		WHERE id = ? AND account_id = ? AND revision = ?`, append(setArgs, run.ID, account, expected)...)
	if err != nil {
		return ports.AgentRun{}, fmt.Errorf("update agent run: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		if _, getErr := s.GetAgentRun(ctx, run.ID); errors.Is(getErr, ports.ErrAgentRunNotFound) {
			return ports.AgentRun{}, ports.ErrAgentRunNotFound
		}
		return ports.AgentRun{}, ports.ErrAgentRunConflict
	}
	return run, nil
}

// SetRepositoryEnabled turns cloning on or off for one allowed repository in
// the acting account's settings.
func (s *Store) SetRepositoryEnabled(ctx context.Context, repository string, enabled bool) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	if enabled {
		_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO agent_runner_repos (account_id, repository) VALUES (?, ?)`, account, repository)
	} else {
		_, err = s.db.ExecContext(ctx, `DELETE FROM agent_runner_repos WHERE account_id = ? AND repository = ?`, account, repository)
	}
	return err
}

// EnabledRepositories lists the repositories the acting account has enabled.
func (s *Store) EnabledRepositories(ctx context.Context) ([]string, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT repository FROM agent_runner_repos WHERE account_id = ? ORDER BY repository`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var repos []string
	for rows.Next() {
		var repo string
		if err := rows.Scan(&repo); err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

// NextQueuedAgentRun is for the worker, which serves every account. It takes
// no account on purpose and is never reachable from a request.
func (s *Store) NextQueuedAgentRun(ctx context.Context) (ports.AgentRun, bool, error) {
	run, err := scanAgentRun(s.db.QueryRowContext(ctx, `SELECT `+agentRunSelect+` FROM agent_runs WHERE state = ? ORDER BY created_at ASC LIMIT 1`, string(ports.AgentRunQueued)).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.AgentRun{}, false, nil
	}
	if err != nil {
		return ports.AgentRun{}, false, err
	}
	return run, true, nil
}

// AgentRunsInStates is for the worker's startup and reconciliation. Like
// NextQueuedAgentRun it spans accounts and takes no account.
func (s *Store) AgentRunsInStates(ctx context.Context, states ...ports.AgentRunState) ([]ports.AgentRun, error) {
	var runs []ports.AgentRun
	for _, state := range states {
		rows, err := s.db.QueryContext(ctx, `SELECT `+agentRunSelect+` FROM agent_runs WHERE state = ? ORDER BY created_at ASC`, string(state))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			run, err := scanAgentRun(rows.Scan)
			if err != nil {
				rows.Close()
				return nil, err
			}
			runs = append(runs, run)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return runs, nil
}
