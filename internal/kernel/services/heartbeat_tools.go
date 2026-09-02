package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// HeartbeatRespondToolName is the heartbeat's own reply channel.
const HeartbeatRespondToolName = "heartbeat_respond"

// HeartbeatResponse is what a beat decided, carried on the turn's context so
// the tool can hand its decision back to the turn that ran it.
//
// A context value rather than a return value because a tool is registered
// once at startup and shared by every turn, while this decision belongs to
// one beat. It mirrors destination.With, which solves the same problem for
// the same reason.
type HeartbeatResponse struct {
	// Responded distinguishes "the model called the tool and chose silence"
	// from "the model never called the tool", which is what lets the turn fall
	// back to the HEARTBEAT_OK text protocol only when it has to.
	Responded bool
	Notify    bool
	Text      string
	// NextCheck is when the beat wants to look again. Zero means it did not
	// say, which happens only when the tool call failed or was never made:
	// the field is required, because a beat that has just read the watch list
	// and looked at the world is the best-placed thing in the system to
	// answer, and an optional field would leave that answer unasked.
	NextCheck time.Duration
}

type heartbeatResponseKey struct{}

// WithHeartbeatResponse attaches a fresh response to ctx and returns both. The
// caller reads the response after the loop returns.
func WithHeartbeatResponse(ctx context.Context) (context.Context, *HeartbeatResponse) {
	response := &HeartbeatResponse{}
	return context.WithValue(ctx, heartbeatResponseKey{}, response), response
}

// HeartbeatResponseFromContext returns the response carried on ctx, or nil on
// a turn that is not a heartbeat.
func HeartbeatResponseFromContext(ctx context.Context) *HeartbeatResponse {
	response, _ := ctx.Value(heartbeatResponseKey{}).(*HeartbeatResponse)
	return response
}

const heartbeatRespondDescription = `End your check-in. Call this exactly once, as the last thing you do.
notify=false means say nothing to the owner. That is the normal outcome: check in, find nothing that warrants interrupting them, stay quiet.
notify=true delivers notification_text to the owner's phone. Use it only for something that genuinely warrants interrupting them right now.
next_check is when you want to look again, as a duration like "20m" or "6h". Aim it at the next moment something you are watching could change, or the owner could need to hear from you — not at how long you can bear to wait. If a reminder is due at 15:00 and needs an hour of preparation, come back at 14:00, not at 15:30. If nothing you are watching moves before Monday, say so and sleep until Monday.
watch optionally replaces the whole watch list. Use it to record what you observed, so a later check-in reads your note and does not report the same thing twice — for example "PR #18 open since Aug 20 — mentioned Aug 22". Keep every item the owner put there unless it is genuinely finished; you are annotating their list, not replacing it with your own.
Never put a time, interval, or cron expression in the watch list. Anything that should happen at a particular time is a schedule, and when you want to look again is next_check.`

var heartbeatRespondSchema = json.RawMessage(`{"type":"object","properties":{"notify":{"type":"boolean"},"notification_text":{"type":"string","minLength":1},"next_check":{"type":"string","minLength":1},"watch":{"type":"string"}},"required":["notify","next_check"],"additionalProperties":false}`)

type heartbeatRespondTool struct {
	store ports.ContextStore
	guard *SecretGuard
}

// NewHeartbeatTools returns the heartbeat's reply tool. It is registered like
// any other kernel tool but reaches a turn only through the heartbeat's
// allowlist, so it costs no prompt bytes on an ordinary turn.
func NewHeartbeatTools(store ports.ContextStore, guard *SecretGuard) []ports.Tool {
	if guard == nil {
		guard = NewSecretGuard(nil)
	}
	return []ports.Tool{heartbeatRespondTool{store: store, guard: guard}}
}

func (t heartbeatRespondTool) Definition() ports.ToolDefinition {
	// Internal, not ReadOnly: it writes WATCH.md. Like the memory tool, every
	// write lands in a document the owner reads and can edit directly and
	// nowhere else, which is exactly what ports.InternalTool classifies.
	return ports.ToolDefinition{
		Name:        HeartbeatRespondToolName,
		Description: heartbeatRespondDescription,
		Schema:      heartbeatRespondSchema,
		Effect:      ports.InternalTool(),
	}
}

func (t heartbeatRespondTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Notify           bool   `json:"notify"`
		NotificationText string `json:"notification_text"`
		NextCheck        string `json:"next_check"`
		Watch            string `json:"watch"`
	}
	if err := DecodeToolInput(raw, &input); err != nil {
		return nil, err
	}
	if input.Notify && strings.TrimSpace(input.NotificationText) == "" {
		return nil, errors.New("notification_text is required when notify is true")
	}
	// Enforced here rather than left to the schema for the reason the schedule
	// tool gives for the same choice: not every provider honors a required
	// field, and a beat that silently skipped this would fall back to a fixed
	// interval forever without anyone noticing.
	nextCheck, err := parseNextCheck(input.NextCheck)
	if err != nil {
		return nil, err
	}

	response := HeartbeatResponseFromContext(ctx)
	if response == nil {
		return nil, errors.New("heartbeat_respond is only available on a heartbeat turn")
	}
	// Recorded before the watch write, so a rejected annotation still delivers
	// the finding. The finding is what the owner needs; the annotation only
	// saves them from hearing it twice.
	response.Responded = true
	response.Notify = input.Notify
	response.Text = strings.TrimSpace(input.NotificationText)
	response.NextCheck = nextCheck

	if strings.TrimSpace(input.Watch) != "" {
		if err := t.guard.Validate("", input.Watch); err != nil {
			return nil, err
		}
		if err := t.store.ReplaceDocument(ctx, ports.ContextWatch, input.Watch); err != nil {
			return nil, err
		}
	}
	return json.RawMessage(`{"acknowledged":true}`), nil
}

// parseNextCheck reads the beat's own answer to "when should I look again".
//
// The error text names the accepted form rather than only rejecting: this is
// the one field on the tool a model has no prior convention for, and a beat
// that cannot say when to return falls back to a fixed interval, which is the
// behaviour the field exists to replace.
func parseNextCheck(value string) (time.Duration, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, errors.New(`next_check is required: say when to look again, as a duration like "20m" or "6h"`)
	}
	parsed, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf(`next_check %q is not a duration like "20m" or "6h"`, trimmed)
	}
	if parsed <= 0 {
		return 0, errors.New("next_check must be a positive duration")
	}
	return parsed, nil
}
