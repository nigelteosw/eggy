package ports

import (
	"errors"
	"time"
)

// ErrScheduleNotFound reports a schedule id with no record behind it. It
// lives here rather than in the store because the scheduler branches on it --
// a job cancelled between the listing and the claim is ordinary, not a
// failure -- and the kernel may not import the adapter that raises it.
var ErrScheduleNotFound = errors.New("schedule not found")

type ScheduleKind string

const (
	ScheduleExact     ScheduleKind = "exact"
	ScheduleRecurring ScheduleKind = "recurring"
)

// ScheduleExecution distinguishes a deterministic, pre-rendered notification
// from a schedule that starts a model turn. ScheduleExecutionMessage covers
// reminders and watchdog-style notifications: Instruction is delivered to
// the owner verbatim at fire time with no model call. ScheduleExecutionAgent
// (the default, including for schedules persisted before this field existed)
// runs Instruction as a self-contained, read-only agent turn.
type ScheduleExecution string

const (
	ScheduleExecutionAgent   ScheduleExecution = "agent"
	ScheduleExecutionMessage ScheduleExecution = "message"
)

type Schedule struct {
	ID string `json:"id"`
	// Owner is the account whose instruction this is. The store stamps it
	// from the creating principal, and the scheduler restores that principal
	// from it when the job fires -- never from the instruction text.
	Owner       string            `json:"owner,omitempty"`
	Kind        ScheduleKind      `json:"kind"`
	Execution   ScheduleExecution `json:"execution,omitempty"`
	Instruction string            `json:"instruction"`
	Expression  string            `json:"expression,omitempty"`
	NextRun     time.Time         `json:"next_run"`
	LastRun     time.Time         `json:"last_run,omitzero"`
	PendingRun  time.Time         `json:"pending_run,omitzero"`
	Enabled     bool              `json:"enabled"`
}
