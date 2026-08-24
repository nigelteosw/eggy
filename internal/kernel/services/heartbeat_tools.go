package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

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
watch optionally replaces the whole watch list. Use it to record what you observed, so a later check-in reads your note and does not report the same thing twice — for example "PR #18 open since Aug 20 — mentioned Aug 22". Keep every item the owner put there unless it is genuinely finished; you are annotating their list, not replacing it with your own.
Never put a time, interval, or cron expression in the watch list. Anything that should happen at a particular time is a schedule.`

var heartbeatRespondSchema = json.RawMessage(`{"type":"object","properties":{"notify":{"type":"boolean"},"notification_text":{"type":"string","minLength":1},"watch":{"type":"string"}},"required":["notify"],"additionalProperties":false}`)

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
		Watch            string `json:"watch"`
	}
	if err := DecodeToolInput(raw, &input); err != nil {
		return nil, err
	}
	if input.Notify && strings.TrimSpace(input.NotificationText) == "" {
		return nil, errors.New("notification_text is required when notify is true")
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
