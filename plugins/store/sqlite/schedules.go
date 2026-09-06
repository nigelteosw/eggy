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
type ScheduleStore struct{ db *sql.DB }

func (s *Store) Schedules() *ScheduleStore { return &ScheduleStore{db: s.db} }

const scheduleColumns = `id, kind, execution, instruction, expression, next_run, last_run, pending_run, enabled`

// List returns every schedule ordered by id, so the panel and the scheduler
// see one stable listing.
func (s *ScheduleStore) List() ([]ports.Schedule, error) {
	ctx := context.Background()
	schedules := make([]ports.Schedule, 0)
	err := scanRows(ctx, s.db, `SELECT `+scheduleColumns+` FROM schedules ORDER BY id`, func(scan func(...any) error) error {
		schedule, err := scanSchedule(scan)
		if err != nil {
			return err
		}
		schedules = append(schedules, schedule)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return schedules, nil
}

func (s *ScheduleStore) Get(id string) (ports.Schedule, error) {
	if !namePattern.MatchString(id) {
		return ports.Schedule{}, errors.New("invalid schedule id")
	}
	return getSchedule(context.Background(), s.db, id)
}

// Create writes a job only when its id is free, so two schedules can never
// collapse into one record.
func (s *ScheduleStore) Create(schedule ports.Schedule) error {
	if !namePattern.MatchString(schedule.ID) {
		return errors.New("invalid schedule id")
	}
	if strings.TrimSpace(schedule.Instruction) == "" {
		return errors.New("instruction is required")
	}
	_, err := s.db.Exec(`INSERT INTO schedules (`+scheduleColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		schedule.ID, string(schedule.Kind), string(schedule.Execution), schedule.Instruction, schedule.Expression,
		formatTime(schedule.NextRun), formatTime(schedule.LastRun), formatTime(schedule.PendingRun), boolToInt(schedule.Enabled))
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("schedule %q already exists", schedule.ID)
	}
	return err
}

// Update applies mutate to one job inside a transaction. A job that is gone
// returns ports.ErrScheduleNotFound and leaves nothing written.
func (s *ScheduleStore) Update(id string, mutate func(*ports.Schedule) error) error {
	if !namePattern.MatchString(id) {
		return errors.New("invalid schedule id")
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	schedule, err := getSchedule(ctx, tx, id)
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
			next_run = ?, last_run = ?, pending_run = ?, enabled = ? WHERE id = ?`,
		string(schedule.Kind), string(schedule.Execution), schedule.Instruction, schedule.Expression,
		formatTime(schedule.NextRun), formatTime(schedule.LastRun), formatTime(schedule.PendingRun),
		boolToInt(schedule.Enabled), id); err != nil {
		return err
	}
	return tx.Commit()
}

// Delete removes a job. A job that is already gone is not an error: the
// owner asked for it to be absent, and it is.
func (s *ScheduleStore) Delete(id string) error {
	if !namePattern.MatchString(id) {
		return errors.New("invalid schedule id")
	}
	_, err := s.db.Exec(`DELETE FROM schedules WHERE id = ?`, id)
	return err
}

func getSchedule(ctx context.Context, source rows, id string) (ports.Schedule, error) {
	row := source.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM schedules WHERE id = ?`, id)
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
	if err := scan(&schedule.ID, &kind, &execution, &schedule.Instruction, &schedule.Expression,
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
