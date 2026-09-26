package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ScheduleStore keeps one row per scheduled job. Schedules were YAML files
// under <home>/cron so an owner could read and edit them; the web panel now
// lists, creates, and cancels them, which is the surface that reading was
// for, and a job is machine-managed state like every other record here.
//
// Every mutation runs in its own transaction, so the claim the scheduler
// makes on a due job -- read, check, stamp pending -- cannot interleave with
// a second tick or with a cancellation from the panel.
//
// Every job belongs to the account that created it. The one read that spans
// accounts is ListAll, which the scheduler's tick uses to find what is due;
// it acts as each job's Owner from then on, so the claim and the completion
// go through the same scoped Update everyone else uses.
type ScheduleStore struct{ db *sql.DB }

func (s *Store) Schedules() *ScheduleStore { return &ScheduleStore{db: s.db} }

const scheduleColumns = `id, account_id, kind, execution, instruction, expression, next_run, last_run, pending_run, enabled`

// List returns the account's schedules ordered by id, so the panel and the
// scheduler see one stable listing.
func (s *ScheduleStore) List(ctx context.Context) ([]ports.Schedule, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return nil, err
	}
	return s.list(ctx, `SELECT `+scheduleColumns+` FROM schedules WHERE account_id = ? ORDER BY id`, account)
}

// ListAll returns every account's schedules, for the scheduler's tick. It
// takes no principal because it is the step that finds out whose turn it is.
func (s *ScheduleStore) ListAll(ctx context.Context) ([]ports.Schedule, error) {
	return s.list(ctx, `SELECT `+scheduleColumns+` FROM schedules ORDER BY id`)
}

func (s *ScheduleStore) list(ctx context.Context, query string, args ...any) ([]ports.Schedule, error) {
	schedules := make([]ports.Schedule, 0)
	err := scanRows(ctx, s.db, query, func(scan func(...any) error) error {
		schedule, err := scanSchedule(scan)
		if err != nil {
			return err
		}
		schedules = append(schedules, schedule)
		return nil
	}, args...)
	if err != nil {
		return nil, err
	}
	return schedules, nil
}

func (s *ScheduleStore) Get(ctx context.Context, id string) (ports.Schedule, error) {
	if !namePattern.MatchString(id) {
		return ports.Schedule{}, errors.New("invalid schedule id")
	}
	account, err := accountOf(ctx)
	if err != nil {
		return ports.Schedule{}, err
	}
	return getSchedule(ctx, s.db, account, id)
}

// Create writes a job only when its id is free, so two schedules can never
// collapse into one record. The id is global: the scheduler claims by id
// alone, so two accounts cannot share one even though neither can see the
// other's.
func (s *ScheduleStore) Create(ctx context.Context, schedule ports.Schedule) error {
	if !namePattern.MatchString(schedule.ID) {
		return errors.New("invalid schedule id")
	}
	if strings.TrimSpace(schedule.Instruction) == "" {
		return errors.New("instruction is required")
	}
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO schedules (`+scheduleColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		schedule.ID, account, string(schedule.Kind), string(schedule.Execution), schedule.Instruction, schedule.Expression,
		formatTime(schedule.NextRun), formatTime(schedule.LastRun), formatTime(schedule.PendingRun), boolToInt(schedule.Enabled))
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("schedule %q already exists", schedule.ID)
	}
	return err
}

// Update applies mutate to one of the account's jobs inside a transaction.
// A job that is gone -- or that belongs to someone else, which is the same
// thing from here -- returns ports.ErrScheduleNotFound and leaves nothing
// written.
func (s *ScheduleStore) Update(ctx context.Context, id string, mutate func(*ports.Schedule) error) error {
	if !namePattern.MatchString(id) {
		return errors.New("invalid schedule id")
	}
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	schedule, err := getSchedule(ctx, tx, account, id)
	if err != nil {
		return err
	}
	if err := mutate(&schedule); err != nil {
		return err
	}
	schedule.ID = id
	if strings.TrimSpace(schedule.Instruction) == "" {
		return errors.New("instruction is required")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE schedules SET kind = ?, execution = ?, instruction = ?, expression = ?,
			next_run = ?, last_run = ?, pending_run = ?, enabled = ? WHERE account_id = ? AND id = ?`,
		string(schedule.Kind), string(schedule.Execution), schedule.Instruction, schedule.Expression,
		formatTime(schedule.NextRun), formatTime(schedule.LastRun), formatTime(schedule.PendingRun),
		boolToInt(schedule.Enabled), account, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Delete removes one of the account's jobs. A job that is already gone is
// not an error: the owner asked for it to be absent, and it is.
func (s *ScheduleStore) Delete(ctx context.Context, id string) error {
	if !namePattern.MatchString(id) {
		return errors.New("invalid schedule id")
	}
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM schedules WHERE account_id = ? AND id = ?`, account, id)
	return err
}

func getSchedule(ctx context.Context, source rows, account, id string) (ports.Schedule, error) {
	row := source.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM schedules WHERE account_id = ? AND id = ?`, account, id)
	schedule, err := scanSchedule(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.Schedule{}, ports.ErrScheduleNotFound
	}
	return schedule, err
}

func scanSchedule(scan func(...any) error) (ports.Schedule, error) {
	var (
		schedule                  ports.Schedule
		kind, execution           string
		nextRun, lastRun, pending string
		enabled                   int
	)
	if err := scan(&schedule.ID, &schedule.Owner, &kind, &execution, &schedule.Instruction, &schedule.Expression,
		&nextRun, &lastRun, &pending, &enabled); err != nil {
		return ports.Schedule{}, err
	}
	var err error
	if schedule.NextRun, err = parseTime(nextRun); err != nil {
		return ports.Schedule{}, fmt.Errorf("schedule %s next_run: %w", schedule.ID, err)
	}
	if schedule.LastRun, err = parseTime(lastRun); err != nil {
		return ports.Schedule{}, fmt.Errorf("schedule %s last_run: %w", schedule.ID, err)
	}
	if schedule.PendingRun, err = parseTime(pending); err != nil {
		return ports.Schedule{}, fmt.Errorf("schedule %s pending_run: %w", schedule.ID, err)
	}
	schedule.Kind = ports.ScheduleKind(kind)
	schedule.Execution = ports.ScheduleExecution(execution)
	schedule.Enabled = enabled != 0
	return schedule, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
