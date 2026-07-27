package cronfile

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func fromSchedule(schedule ports.Schedule) job {
	return job{
		ID:          schedule.ID,
		Kind:        string(schedule.Kind),
		Execution:   string(schedule.Execution),
		Instruction: schedule.Instruction,
		Cron:        schedule.Expression,
		NextRun:     formatTime(schedule.NextRun),
		LastRun:     formatTime(schedule.LastRun),
		PendingRun:  formatTime(schedule.PendingRun),
		Enabled:     schedule.Enabled,
	}
}

func (j job) toSchedule() (ports.Schedule, error) {
	nextRun, err := parseTime(j.NextRun)
	if err != nil {
		return ports.Schedule{}, fmt.Errorf("next_run: %w", err)
	}
	lastRun, err := parseTime(j.LastRun)
	if err != nil {
		return ports.Schedule{}, fmt.Errorf("last_run: %w", err)
	}
	pendingRun, err := parseTime(j.PendingRun)
	if err != nil {
		return ports.Schedule{}, fmt.Errorf("pending_run: %w", err)
	}
	if strings.TrimSpace(j.Instruction) == "" {
		return ports.Schedule{}, errors.New("instruction is required")
	}
	return ports.Schedule{
		ID:          j.ID,
		Kind:        ports.ScheduleKind(j.Kind),
		Execution:   ports.ScheduleExecution(j.Execution),
		Instruction: j.Instruction,
		Expression:  j.Cron,
		NextRun:     nextRun,
		LastRun:     lastRun,
		PendingRun:  pendingRun,
		Enabled:     j.Enabled,
	}, nil
}

// formatTime writes RFC3339 with the offset preserved, because a recurring
// schedule's next run is computed in the owner's location and a round trip
// through UTC would silently move a "09:00 daily" job.
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not an RFC3339 time", value)
	}
	return parsed, nil
}
