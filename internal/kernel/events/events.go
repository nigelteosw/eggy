package events

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

type Type string

const (
	TypeMessage       Type = "message"
	TypeApproval      Type = "approval"
	TypeSchedule      Type = "schedule"
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
	// constructed by whichever surface produced the event -- never inferred from
	// Source or from message content.
	Destination destination.Destination `json:"destination"`
	Payload     json.RawMessage         `json:"payload"`
	// SenderID is the provider-neutral text of the authenticated sender a
	// direct message came from, filled only by an ingress that verified it
	// (the Telegram webhook, after its private-chat and allowlist checks).
	// It is metadata about how the event arrived, never prompt text, and
	// it is what lets a command mint a browser login for that sender and
	// nobody else. Empty everywhere else: a selection callback, a web
	// thread, a schedule.
	SenderID string `json:"sender_id,omitempty"`
}

type Message struct {
	Text  string              `json:"text"`
	Parts []ports.ContentPart `json:"parts,omitempty"`
	// Quote is the earlier passage this message replies to, when the surface
	// has one: a Telegram reply (or partial quote), a highlighted span in the
	// web UI, a Discord reply later. Surfaces fill it in; only Prompt reads it.
	Quote *Quote `json:"quote,omitempty"`
}

// Quote is a replied-to passage, kept surface-neutral so every channel that
// grows a reply affordance produces the same thing and the model sees one
// format regardless of where the owner typed.
type Quote struct {
	Text string `json:"text"`
	// OwnMessage is true when the quoted passage came from Eggy, so the
	// prompt can say so: being pointed back at its own words is a different
	// situation from the owner introducing new text.
	OwnMessage bool `json:"own_message,omitempty"`
}

// Prompt is the message as the model should read it. A reply is prefixed the
// way Hermes Agent's gateway does it, with the full quoted text rather than a
// preview: the point is disambiguation -- which prior passage is meant --
// and a truncated quote silently loses later list items and code. It is
// injected even when the passage is already in history, for the same reason.
//
// The quote is wrapped verbatim, not escaped: a surface that shows the sent
// message back (the web transcript) must be able to rebuild this string
// exactly, and the model reads it as prose either way.
func (m Message) Prompt() string {
	if m.Quote == nil || strings.TrimSpace(m.Quote.Text) == "" {
		return m.Text
	}
	who := ""
	if m.Quote.OwnMessage {
		who = " your previous message"
	}
	return fmt.Sprintf("[Replying to%s: \"%s\"]\n\n%s", who, m.Quote.Text, m.Text)
}

type ApprovalDecision struct {
	ApprovalID string `json:"approval_id"`
	Approved   bool   `json:"approved"`
	// MessageID identifies an already-delivered approval message to edit in
	// place with the outcome, when the originating surface tracks one.
	MessageID string `json:"message_id"`
}
