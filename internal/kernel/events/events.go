package events

import (
	"encoding/json"
	"time"
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
	ID            string          `json:"id"`
	Type          Type            `json:"type"`
	Source        string          `json:"source"`
	Owner         string          `json:"owner"`
	Timestamp     time.Time       `json:"timestamp"`
	CorrelationID string          `json:"correlation_id"`
	Payload       json.RawMessage `json:"payload"`
}

type Message struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

type ApprovalDecision struct {
	ApprovalID      string `json:"approval_id"`
	Approved        bool   `json:"approved"`
	CallbackQueryID string `json:"callback_query_id"`
	MessageID       string `json:"message_id"`
}
