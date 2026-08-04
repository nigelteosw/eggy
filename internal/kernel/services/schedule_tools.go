package services

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ScheduleWriter is the slice of the scheduler these tools need: record a
// schedule, and resolve a cron expression's next fire time. It is declared
// here rather than imported so internal/kernel stays provider-neutral, which
// is the same reason repo.ScheduleCounter exists beside it.
type ScheduleWriter interface {
	Add(ctx context.Context, schedule ports.Schedule) error
	Next(expression string, after time.Time) (time.Time, error)
	// List and Remove are what make a created schedule reviewable. Without
	// them the agent could create a job and then never answer what it
	// created -- which also meant it could never take one back, so a wrong
	// cron expression was permanent short of editing the volume by hand.
	List(ctx context.Context) ([]ports.Schedule, error)
	Remove(ctx context.Context, id string) error
}

// NewScheduleTools returns the single tool that creates, reviews, and cancels
// schedules. newID supplies the schedule's identifier: the scheme belongs to
// the caller that owns run identity, not to the kernel.
//
// The four separate tools this replaced spent four slots of the model's tool
// budget on one subject, and the model had to know which of schedule_exact and
// schedule_recurring to reach for before it knew what the owner meant. One
// tool with an action keeps the whole subject in one description; the
// read-only "list" action stays separately grantable because the allowlist
// understands "schedule:list" (see agent.RunOptions.AllowedTools).
// location renders a schedule's next run for the owner. A stored time carries
// whatever zone it was created with -- the offset the model wrote into `at`, or
// the scheduler's for a cron -- so without one place to render them, two
// schedules made the same day report their times in two different zones.
func NewScheduleTools(schedules ScheduleWriter, now func() time.Time, newID func() string, location *time.Location) []ports.Tool {
	if location == nil {
		location = time.UTC
	}
	return []ports.Tool{scheduleTool{schedules: schedules, now: now, newID: newID, location: location}}
}

type scheduleTool struct {
	schedules ScheduleWriter
	now       func() time.Time
	newID     func() string
	location  *time.Location
}

// ScheduleToolName is the one tool covering the whole scheduling subject.
const ScheduleToolName = "schedule"

// scheduleSchema keeps every action's fields in one object. A schema union
// would describe the shape more exactly, but not every provider honors
// oneOf, so the required-field rules are enforced in Execute where they
// cannot be skipped.
const scheduleSchema = `{"type":"object","properties":{` +
	`"action":{"type":"string","enum":["list","create","cancel"],"description":"'list' reports what exists, 'create' adds a schedule, 'cancel' removes one by id"},` +
	`"instruction":{"type":"string","description":"create: what should happen when it fires"},` +
	`"cron":{"type":"string","description":"create: five-field cron expression for a recurring schedule"},` +
	`"at":{"type":"string","description":"create: RFC3339 time for a one-time schedule"},` +
	`"kind":{"type":"string","enum":["agent","reminder"],"description":"create: 'reminder' delivers instruction verbatim at fire time with no model call; 'agent' (default) starts a self-contained read-only agent turn"},` +
	`"id":{"type":"string","description":"cancel: the schedule id to remove"}` +
	`},"required":["action"],"additionalProperties":false}`

func (t scheduleTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        ScheduleToolName,
		Description: "Review, create, or cancel schedules. action=list reports every schedule with its cron expression, next run, and kind; action=create takes an instruction plus either cron (recurring) or at (one-time RFC3339); action=cancel removes one by id so it never runs again",
		Schema:      json.RawMessage(scheduleSchema),
		// A schedule outlives the turn that made it and fires unattended,
		// which is the whole reason creating one is a decision rather than a
		// note to self.
		Effect: ports.MutatingActions("create", "cancel"),
	}
}

// DefinitionForActions describes only the actions a restricted turn was
// granted, so a heartbeat allowed "schedule:list" is never shown create or
// cancel and cannot propose calling them.
func (t scheduleTool) DefinitionForActions(actions []string) ports.ToolDefinition {
	if len(actions) == 0 {
		return t.Definition()
	}
	if len(actions) == 1 && actions[0] == "list" {
		return ports.ToolDefinition{
			Name:        ScheduleToolName,
			Description: "List the schedules that exist, with their cron expression, next run, and kind. Only action=list is available on this turn",
			Schema:      json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["list"]}},"required":["action"],"additionalProperties":false}`),
			Effect:      ports.ReadOnlyTool(),
		}
	}
	definition := t.Definition()
	definition.Description += ". Only these actions are available on this turn: " + strings.Join(actions, ", ")
	return definition
}

func (t scheduleTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Action      string `json:"action"`
		Instruction string `json:"instruction"`
		Cron        string `json:"cron"`
		At          string `json:"at"`
		Kind        string `json:"kind"`
		ID          string `json:"id"`
	}
	if err := DecodeToolInput(raw, &input); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(input.Action) {
	case "list":
		return t.list(ctx)
	case "create":
		return t.create(ctx, input.Instruction, input.Cron, input.At, input.Kind)
	case "cancel":
		return t.cancel(ctx, input.ID)
	default:
		return nil, fmt.Errorf("unknown schedule action %q", input.Action)
	}
}

// ScheduleAction reports which action a raw schedule tool call asks for, so
// the allowlist can grant "schedule:list" without granting the rest. A call
// whose arguments do not parse has no action, and the allowlist denies it.
func ScheduleAction(raw json.RawMessage) string {
	var input struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return ""
	}
	return strings.TrimSpace(input.Action)
}

func (t scheduleTool) list(ctx context.Context) (json.RawMessage, error) {
	schedules, err := t.schedules.List(ctx)
	if err != nil {
		return nil, err
	}
	// Sorted by next run so the answer reads as a timeline rather than in
	// whatever order the store happened to walk its directory.
	slices.SortFunc(schedules, func(a, b ports.Schedule) int { return a.NextRun.Compare(b.NextRun) })
	if schedules == nil {
		schedules = []ports.Schedule{}
	}
	return json.Marshal(struct {
		Schedules []ports.Schedule `json:"schedules"`
	}{Schedules: schedules})
}

// cancel treats removing a schedule that is already gone as success: the
// owner's intent is that it not run, and it will not.
func (t scheduleTool) cancel(ctx context.Context, id string) (json.RawMessage, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("id is required to cancel a schedule")
	}
	if err := t.schedules.Remove(ctx, id); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Cancelled string `json:"cancelled"`
		Summary   string `json:"summary"`
	}{Cancelled: id, Summary: fmt.Sprintf("Cancelled schedule %s. It will not run again.", id)})
}

func (t scheduleTool) create(ctx context.Context, instruction, cron, at, kind string) (json.RawMessage, error) {
	if strings.TrimSpace(instruction) == "" {
		return nil, fmt.Errorf("instruction is required to create a schedule")
	}
	hasCron, hasAt := strings.TrimSpace(cron) != "", strings.TrimSpace(at) != ""
	if hasCron == hasAt {
		return nil, fmt.Errorf("creating a schedule needs exactly one of cron (recurring) or at (one-time)")
	}
	execution, err := scheduleExecution(kind)
	if err != nil {
		return nil, err
	}
	schedule := ports.Schedule{ID: t.newID(), Execution: execution, Instruction: instruction, Enabled: true}
	if hasAt {
		parsed, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return nil, err
		}
		schedule.Kind, schedule.NextRun = ports.ScheduleExact, parsed
	} else {
		next, err := t.schedules.Next(cron, t.now())
		if err != nil {
			return nil, err
		}
		schedule.Kind, schedule.Expression, schedule.NextRun = ports.ScheduleRecurring, cron, next
	}
	if err := t.schedules.Add(ctx, schedule); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		ports.Schedule
		Summary string `json:"summary"`
	}{Schedule: schedule, Summary: scheduleSummary(schedule, t.location)})
}

// scheduleSummary is the one line an owner should read about a schedule that
// was just made. It is carried in the tool's own result under "summary", which
// is what the approve-tap outcome renders instead of the raw record: an owner
// who taps approve gets "Reminder set for Wed 5 Aug 2026, 8:00 PM +08", not the
// JSON of the instruction they just wrote.
//
// The instruction is deliberately left out. It is the owner's own words, often
// several paragraphs of them, and echoing it back is what made the old outcome
// message unreadable.
func scheduleSummary(schedule ports.Schedule, location *time.Location) string {
	what := "Agent run"
	if schedule.Execution == ports.ScheduleExecutionMessage {
		what = "Reminder"
	}
	when := schedule.NextRun.In(location).Format("Mon 2 Jan 2006, 3:04 PM MST")
	if schedule.Kind == ports.ScheduleRecurring {
		return fmt.Sprintf("%s recurring on `%s`, next %s. Cancel with id %s.", what, schedule.Expression, when, schedule.ID)
	}
	return fmt.Sprintf("%s set for %s. Cancel with id %s.", what, when, schedule.ID)
}

// scheduleExecution maps the optional "kind" onto an execution: a plain
// reminder or watchdog notification (delivered verbatim with no later model
// call) versus work that needs a self-contained agent turn to run. It
// defaults to "agent" so omitting it keeps today's behavior.
func scheduleExecution(kind string) (ports.ScheduleExecution, error) {
	switch kind {
	case "", "agent":
		return ports.ScheduleExecutionAgent, nil
	case "reminder":
		return ports.ScheduleExecutionMessage, nil
	default:
		return "", fmt.Errorf("unknown schedule kind %q", kind)
	}
}
