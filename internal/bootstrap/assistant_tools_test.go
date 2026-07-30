package bootstrap

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/scheduler/cronfile"
	schedulerlocal "github.com/nigelteosw/eggy/plugins/scheduler/local"
)

// TestScheduleToolsDistinguishReminderFromAgentExecution proves the agent
// can create a deterministic, pre-rendered reminder ("kind":"reminder") as
// well as the default self-contained agent-turn schedule, and that an
// unrecognized kind is rejected rather than silently defaulting.
func TestScheduleToolsDistinguishReminderFromAgentExecution(t *testing.T) {
	// Schedules live as files in <home>/cron, so the scheduler is backed by
	// a real cron directory rather than the state store.
	scheduler := schedulerlocal.New(cronfile.Open(t.TempDir()))
	now := func() time.Time { return time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC) }
	exact := scheduleTools(scheduler, now)[0]

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
}

func TestCurrentTimeToolReturnsTrustedZonedClock(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Singapore")
	now := func() time.Time { return time.Date(2026, 7, 19, 12, 34, 56, 0, location) }
	result, err := currentTimeTool(now, location, "Asia/Singapore").Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"current_time":"2026-07-19T12:34:56+08:00"`) || !strings.Contains(string(result), `"timezone":"Asia/Singapore"`) {
		t.Fatalf("result=%s", result)
	}
}
