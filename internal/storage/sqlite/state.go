package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nigelteosw/eggy/internal/core/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// StateStore is ports.StateStore over the machine-state tables. It replaced
// state.json, and it keeps that file's contract exactly: Load returns a
// snapshot with a version, and Update refuses a mutation whose caller read an
// older one. What state.json never had is an account: every row here belongs
// to the principal on the context, with its own version counter, so one
// person's /mode or model choice is theirs alone and two people's writes
// cannot conflict with each other. What changed is where the compare-and-set happens -- a single
// IMMEDIATE transaction rather than a file lock around a rewrite -- so an
// interrupted update leaves the previous state rather than a partial file.
type StateStore struct{ db *sql.DB }

// State returns the store's operational state. It is a view onto the same
// database handle, not a second connection, so a state update is serialized
// against every other writer.
func (s *Store) State() *StateStore { return &StateStore{db: s.db} }

// rows is the read half of *sql.DB and *sql.Tx, so loadState serves both the
// snapshot Load takes and the one Update mutates inside its transaction.
type rows interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *StateStore) Load(ctx context.Context) (ports.State, error) {
	if err := ctx.Err(); err != nil {
		return ports.State{}, err
	}
	account, err := accountOf(ctx)
	if err != nil {
		return ports.State{}, err
	}
	return loadState(ctx, s.db, account)
}

func (s *StateStore) Update(ctx context.Context, expectedVersion uint64, mutate func(*ports.State) error) (ports.State, error) {
	if err := ctx.Err(); err != nil {
		return ports.State{}, err
	}
	account, err := accountOf(ctx)
	if err != nil {
		return ports.State{}, err
	}
	// The read and the compare-and-set share one transaction, and the store
	// runs on a single connection, so two updaters cannot both read version
	// N and both believe they won: the second blocks here and then fails its
	// version check.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ports.State{}, err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := loadState(ctx, tx, account)
	if err != nil {
		return ports.State{}, err
	}
	if state.Version != expectedVersion {
		return ports.State{}, fmt.Errorf("%w: expected %d, current %d", ports.ErrStateVersionConflict, expectedVersion, state.Version)
	}
	if err := mutate(&state); err != nil {
		return ports.State{}, err
	}
	state.Version++
	state.SchemaVersion = MachineStateVersion
	if err := saveState(ctx, tx, account, state); err != nil {
		return ports.State{}, err
	}
	if err := tx.Commit(); err != nil {
		return ports.State{}, err
	}
	return state, nil
}

func initialState() ports.State {
	return ports.State{
		SchemaVersion:   MachineStateVersion,
		Approvals:       map[string]approvals.Approval{},
		ProcessedEvents: map[string]time.Time{},
	}
}

func loadState(ctx context.Context, source rows, account string) (ports.State, error) {
	state := initialState()
	var (
		approvalMode string
		autoMode     int
		agent        string
		repositories string
	)
	err := source.QueryRowContext(ctx, `SELECT version, approval_mode, approval_auto_mode, agent, repositories FROM machine_state WHERE account_id = ?`, account).
		Scan(&state.Version, &approvalMode, &autoMode, &agent, &repositories)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ports.State{}, fmt.Errorf("read machine state: %w", err)
	}
	if err == nil {
		state.ApprovalMode = ports.ApprovalMode(approvalMode)
		state.ApprovalAutoMode = autoMode != 0
		if err := json.Unmarshal([]byte(agent), &state.Agent); err != nil {
			return ports.State{}, fmt.Errorf("decode agent state: %w", err)
		}
		if repositories != "" && repositories != "null" {
			if err := json.Unmarshal([]byte(repositories), &state.Repositories); err != nil {
				return ports.State{}, fmt.Errorf("decode repositories: %w", err)
			}
		}
	}
	if err := scanRows(ctx, source, `SELECT id, record FROM approvals WHERE account_id = ?`, func(scan func(...any) error) error {
		var id, record string
		if err := scan(&id, &record); err != nil {
			return err
		}
		var approval approvals.Approval
		if err := json.Unmarshal([]byte(record), &approval); err != nil {
			return fmt.Errorf("decode approval %s: %w", id, err)
		}
		state.Approvals[id] = approval
		return nil
	}, account); err != nil {
		return ports.State{}, err
	}
	if err := scanRows(ctx, source, `SELECT id, seen_at FROM processed_events WHERE account_id = ?`, func(scan func(...any) error) error {
		var id, seenAt string
		if err := scan(&id, &seenAt); err != nil {
			return err
		}
		handled, err := parseTime(seenAt)
		if err != nil {
			return fmt.Errorf("processed event %s: %w", id, err)
		}
		state.ProcessedEvents[id] = handled
		return nil
	}, account); err != nil {
		return ports.State{}, err
	}
	if err := scanRows(ctx, source, `SELECT sent_at FROM proactive_messages WHERE account_id = ? ORDER BY id`, func(scan func(...any) error) error {
		var sentAt string
		if err := scan(&sentAt); err != nil {
			return err
		}
		sent, err := parseTime(sentAt)
		if err != nil {
			return fmt.Errorf("proactive message: %w", err)
		}
		state.ProactiveMessages = append(state.ProactiveMessages, sent)
		return nil
	}, account); err != nil {
		return ports.State{}, err
	}
	return state, nil
}

// saveState rewrites the mutable collections wholesale rather than diffing
// them. Each is bounded and small -- pending approvals, the deduplication
// window the dispatcher prunes, and the proactive-message window -- and a
// rewrite inside the same transaction cannot leave a half-applied mutation
// the way a computed diff with a missed case could.
func saveState(ctx context.Context, tx *sql.Tx, account string, state ports.State) error {
	agent, err := json.Marshal(state.Agent)
	if err != nil {
		return err
	}
	repositories, err := json.Marshal(state.Repositories)
	if err != nil {
		return err
	}
	autoMode := 0
	if state.ApprovalAutoMode {
		autoMode = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO machine_state (account_id, version, approval_mode, approval_auto_mode, agent, repositories)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id) DO UPDATE SET
			version = excluded.version,
			approval_mode = excluded.approval_mode,
			approval_auto_mode = excluded.approval_auto_mode,
			agent = excluded.agent,
			repositories = excluded.repositories`,
		account, state.Version, string(state.ApprovalMode), autoMode, string(agent), string(repositories)); err != nil {
		return fmt.Errorf("write machine state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM approvals WHERE account_id = ?`, account); err != nil {
		return err
	}
	for id, approval := range state.Approvals {
		record, err := json.Marshal(approval)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO approvals (id, account_id, record) VALUES (?, ?, ?)`, id, account, string(record)); err != nil {
			return fmt.Errorf("write approval %s: %w", id, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM processed_events WHERE account_id = ?`, account); err != nil {
		return err
	}
	for id, handled := range state.ProcessedEvents {
		if _, err := tx.ExecContext(ctx, `INSERT INTO processed_events (account_id, id, seen_at) VALUES (?, ?, ?)`, account, id, formatTime(handled)); err != nil {
			return fmt.Errorf("write processed event %s: %w", id, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM proactive_messages WHERE account_id = ?`, account); err != nil {
		return err
	}
	for _, sent := range state.ProactiveMessages {
		if _, err := tx.ExecContext(ctx, `INSERT INTO proactive_messages (account_id, sent_at) VALUES (?, ?)`, account, formatTime(sent)); err != nil {
			return fmt.Errorf("write proactive message: %w", err)
		}
	}
	return nil
}

// scanRows runs a query and hands each row to visit, closing the result set
// even when visit fails. Every read here is a small collection, so the
// callback shape costs nothing and keeps loadState free of four copies of the
// same defer-and-check.
func scanRows(ctx context.Context, source rows, query string, visit func(scan func(...any) error) error, args ...any) error {
	result, err := source.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer result.Close()
	for result.Next() {
		if err := visit(result.Scan); err != nil {
			return err
		}
	}
	return result.Err()
}
