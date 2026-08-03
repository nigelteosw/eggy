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

// fakeSchedules records what the tools write and holds what they read. Using
// this rather than the real scheduler is what keeps internal/kernel free of a
// plugin import.
//
// It grew List and Remove when the list and cancel tools arrived, which by
// the standard this comment used to set means ScheduleWriter is no longer a
// narrow slice of the scheduler -- it is now most of it. That is the honest
// consequence of making schedules reviewable: answering "what did I create"
// and "take that one back" needs the same reads the scheduler does.
type fakeSchedules struct {
	added   []ports.Schedule
	removed []string
	next    time.Time
	err     error
}

func (f *fakeSchedules) Add(_ context.Context, schedule ports.Schedule) error {
	if f.err != nil {
		return f.err
	}
	f.added = append(f.added, schedule)
	return nil
}

func (f *fakeSchedules) Next(string, time.Time) (time.Time, error) { return f.next, f.err }

func (f *fakeSchedules) List(context.Context) ([]ports.Schedule, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]ports.Schedule(nil), f.added...), nil
}

func (f *fakeSchedules) Remove(_ context.Context, id string) error {
	if f.err != nil {
		return f.err
	}
	kept := f.added[:0]
	for _, schedule := range f.added {
		if schedule.ID != id {
			kept = append(kept, schedule)
		}
	}
	f.removed = append(f.removed, id)
	f.added = kept
	return nil
}

// TestScheduleToolsDistinguishReminderFromAgentExecution proves the agent
// can create a deterministic, pre-rendered reminder ("kind":"reminder") as
// well as the default self-contained agent-turn schedule, and that an
// unrecognized kind is rejected rather than silently defaulting.
func TestScheduleToolsDistinguishReminderFromAgentExecution(t *testing.T) {
	schedules := &fakeSchedules{}
	now := func() time.Time { return time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC) }
	id := 0
	newID := func() string { id++; return fmt.Sprintf("sched-%d", id) }
	tool := NewScheduleTools(schedules, now, newID)[0]

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"create","at":"2026-07-19T12:00:00Z","instruction":"Take the bins out","kind":"reminder"}`))
	if err != nil {
		t.Fatal(err)
	}
	var reminder ports.Schedule
	if err := json.Unmarshal(result, &reminder); err != nil || reminder.Execution != ports.ScheduleExecutionMessage {
		t.Fatalf("reminder=%s err=%v", result, err)
	}

	result, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"create","at":"2026-07-19T12:00:00Z","instruction":"Check my calendar for conflicts"}`))
	if err != nil {
		t.Fatal(err)
	}
	var agentSchedule ports.Schedule
	if err := json.Unmarshal(result, &agentSchedule); err != nil || agentSchedule.Execution != ports.ScheduleExecutionAgent {
		t.Fatalf("default schedule=%s err=%v", result, err)
	}

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"create","at":"2026-07-19T12:00:00Z","instruction":"x","kind":"nonsense"}`)); err == nil {
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

// The gap these two close: the agent could create a schedule and then had no
// way to say what it had created, which also meant no way to take one back. A
// wrong cron expression was permanent short of editing the volume by hand.
func TestScheduleListAndCancelMakeCreatedSchedulesReviewable(t *testing.T) {
	schedules := &fakeSchedules{next: time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)}
	now := func() time.Time { return time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC) }
	id := 0
	tool := NewScheduleTools(schedules, now, func() string { id++; return fmt.Sprintf("sched-%d", id) })[0]

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"create","cron":"0 9 * * *","instruction":"check the deploy"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"create","at":"2026-08-02T18:00:00Z","instruction":"stand up"}`)); err != nil {
		t.Fatal(err)
	}

	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Schedules []ports.Schedule `json:"schedules"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Schedules) != 2 {
		t.Fatalf("schedules=%#v, want both", listed.Schedules)
	}
	// Sorted by next run, so the answer reads as a timeline: the 18:00
	// one-off comes before tomorrow morning's recurring job.
	if listed.Schedules[0].Instruction != "stand up" {
		t.Fatalf("order=%#v, want the soonest first", listed.Schedules)
	}
	if listed.Schedules[1].Expression != "0 9 * * *" {
		t.Fatalf("expression lost: %#v", listed.Schedules[1])
	}

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"cancel","id":"sched-1"}`)); err != nil {
		t.Fatal(err)
	}
	remaining, err := schedules.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != "sched-2" {
		t.Fatalf("remaining=%#v, want only the uncancelled one", remaining)
	}
}

// An empty list is an empty array, not null: "nothing is scheduled" is an
// answer, and a null would read to the model as a missing field.
func TestScheduleListReportsNothingScheduledAsAnEmptyList(t *testing.T) {
	tool := NewScheduleTools(&fakeSchedules{}, time.Now, func() string { return "id" })[0]
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"schedules":[]}` {
		t.Fatalf("raw=%s", raw)
	}
}

func TestScheduleCancelRequiresAnID(t *testing.T) {
	tool := NewScheduleTools(&fakeSchedules{}, time.Now, func() string { return "id" })[0]
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"cancel","id":"  "}`)); err == nil {
		t.Fatal("a blank id must be rejected rather than removing nothing quietly")
	}
}

// One tool covering the whole subject has to police the field combinations a
// schema union used to: a create with neither cron nor at, or with both, is a
// mistake the owner would otherwise discover only when it fired (or didn't).
func TestScheduleCreateRequiresExactlyOneOfCronOrAt(t *testing.T) {
	schedules := &fakeSchedules{next: time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)}
	tool := NewScheduleTools(schedules, time.Now, func() string { return "id" })[0]
	for _, arguments := range []string{
		`{"action":"create","instruction":"x"}`,
		`{"action":"create","instruction":"x","cron":"0 9 * * *","at":"2026-08-02T18:00:00Z"}`,
		`{"action":"create","cron":"0 9 * * *"}`,
		`{"action":"nonsense"}`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(arguments)); err == nil {
			t.Fatalf("expected %s to be rejected", arguments)
		}
	}
	if len(schedules.added) != 0 {
		t.Fatalf("added=%#v, want no schedule written by a rejected call", schedules.added)
	}
}

// A turn granted only the list action is shown a definition that offers only
// that action, so the model is never tempted into a call the allowlist would
// refuse.
func TestScheduleDefinitionNarrowsToTheGrantedActions(t *testing.T) {
	tool := NewScheduleTools(&fakeSchedules{}, time.Now, func() string { return "id" })[0]
	scoped, ok := tool.(interface {
		DefinitionForActions([]string) ports.ToolDefinition
	})
	if !ok {
		t.Fatal("the schedule tool must expose per-action definitions")
	}
	definition := scoped.DefinitionForActions([]string{"list"})
	if strings.Contains(string(definition.Schema), "cancel") || strings.Contains(string(definition.Schema), "cron") {
		t.Fatalf("schema=%s, want only the list action described", definition.Schema)
	}
}
