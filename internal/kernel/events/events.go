package events

import (
	"encoding/json"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
)

type Type string

const (
	TypeMessage       Type = "message"
	TypeApproval      Type = "approval"
	TypeSchedule      Type = "schedule"
	TypeHeartbeat     Type = "heartbeat"
	TypeOAuthCallback Type = "oauth_callback"
	TypeRunnerUpdate  Type = "runner_update"
	// TypeScheduledMessage delivers a pre-rendered notification verbatim with
	// no model call, for a schedule created with
	// ports.ScheduleExecutionMessage (a reminder or watchdog-style
	// notification), as distinct from TypeSchedule which starts a
	// self-contained read-only agent turn.
	TypeScheduledMessage Type = "scheduled_message"
)

type Event struct {
	ID            string    `json:"id"`
	Type          Type      `json:"type"`
	Source        string    `json:"source"`
	Owner         string    `json:"owner"`
	Timestamp     time.Time `json:"timestamp"`
	CorrelationID string    `json:"correlation_id"`
	// Destination is the surface this event's turn should reply to,
	// constructed by whichever surface produced the event (Telegram's
	// webhook handler, the web thread-send handler, or a schedule/heartbeat
	// tick, all of which are fixed to Telegram) -- never inferred from
	// Source or from message content.
	Destination destination.Destination `json:"destination"`
	Payload     json.RawMessage         `json:"payload"`
}

type Message struct {
	Text string `json:"text"`
}

type ApprovalDecision struct {
	ApprovalID string `json:"approval_id"`
	Approved   bool   `json:"approved"`
	// MessageID identifies an already-delivered approval message to edit in
	// place with the outcome, when the originating surface tracks one.
	MessageID string `json:"message_id"`
}
