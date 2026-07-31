package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// fakeSchedules records what the tools write. Using this rather than the real
// scheduler is what keeps internal/kernel free of a plugin import, and it is
// the reason ScheduleWriter is narrow: if this fake ever needs to grow, the
// interface has stopped being a slice of the scheduler and started being the
// scheduler.
type fakeSchedules struct {
	added []ports.Schedule
	next  time.Time
	err   error
}

func (f *fakeSchedules) Add(_ context.Context, schedule ports.Schedule) error {
	if f.err != nil {
		return f.err
	}
	f.added = append(f.added, schedule)
	return nil
}

func (f *fakeSchedules) Next(string, time.Time) (time.Time, error) { return f.next, f.err }

// TestScheduleToolsDistinguishReminderFromAgentExecution proves the agent
// can create a deterministic, pre-rendered reminder ("kind":"reminder") as
// well as the default self-contained agent-turn schedule, and that an
// unrecognized kind is rejected rather than silently defaulting.
func TestScheduleToolsDistinguishReminderFromAgentExecution(t *testing.T) {
	schedules := &fakeSchedules{}
	now := func() time.Time { return time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC) }
	id := 0
	newID := func() string { id++; return fmt.Sprintf("sched-%d", id) }
	exact := NewScheduleTools(schedules, now, newID)[0]

	result, err := exact.Execute(context.Background(), json.RawMessage(`{"at":"2026-07-19T12:00:00Z","instruction":"Take the bins out","kind":"reminder"}`))
	if err != nil {
		t.Fatal(err)
	}
	var reminder ports.Schedule
	if err := json.Unmarshal(result, &reminder); err != nil || reminder.Execution != ports.ScheduleExecutionMessage {
		t.Fatalf("reminder=%s err=%v", result, err)
	}

	result, err = exact.Execute(context.Background(), json.RawMessage(`{"at":"2026-07-19T12:00:00Z","instruction":"Check my calendar for conflicts"}`))
	if err != nil {
		t.Fatal(err)
	}
	var agentSchedule ports.Schedule
	if err := json.Unmarshal(result, &agentSchedule); err != nil || agentSchedule.Execution != ports.ScheduleExecutionAgent {
		t.Fatalf("default schedule=%s err=%v", result, err)
	}

	if _, err := exact.Execute(context.Background(), json.RawMessage(`{"at":"2026-07-19T12:00:00Z","instruction":"x","kind":"nonsense"}`)); err == nil {
		t.Fatal("expected an unknown kind to be rejected")
	}
	// A rejected kind must not have reached the scheduler: the two accepted
	// calls above are the only writes.
	if len(schedules.added) != 2 {
		t.Fatalf("scheduler received %d schedules, want 2", len(schedules.added))
	}
}

func TestCurrentTimeToolReturnsTrustedZonedClock(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Singapore")
	now := func() time.Time { return time.Date(2026, 7, 19, 12, 34, 56, 0, location) }
	result, err := NewCurrentTimeTool(now, location, "Asia/Singapore").Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"current_time":"2026-07-19T12:34:56+08:00"`) || !strings.Contains(string(result), `"timezone":"Asia/Singapore"`) {
		t.Fatalf("result=%s", result)
	}
}
