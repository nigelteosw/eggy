package approvals

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
)

var (
	ErrNotAuthorized   = errors.New("action is not authorized")
	ErrExpired         = errors.New("approval expired")
	ErrPayloadMismatch = errors.New("approval payload changed")
)

type Action string

// Each Action is one protected operation. An approval authorizes exactly one
// of these against one payload digest: approving a calendar deletion never
// authorizes a different deletion, and never authorizes a create or update.
const (
	CalendarCreate Action = "calendar_create"
	CalendarUpdate Action = "calendar_update"
	CalendarDelete Action = "calendar_delete"
)

type Status string

const (
	Pending     Status = "pending"
	Approved    Status = "approved"
	Rejected    Status = "rejected"
	Expired     Status = "expired"
	Invalidated Status = "invalidated"
	Used        Status = "used"
)

type Approval struct {
	ID            string          `json:"id"`
	Action        Action          `json:"action"`
	PayloadDigest string          `json:"payload_digest"`
	Payload       json.RawMessage `json:"payload"`
	Summary       string          `json:"summary"`
	Status        Status          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	ExpiresAt     time.Time       `json:"expires_at"`
	DecidedAt     time.Time       `json:"decided_at,omitempty"`
	// Destination is the channel this approval's eventual decision should be
	// delivered back to, stamped once at request time from the requesting
	// turn's ctx. The zero value (Kind: "") behaves as Telegram, matching
	// approvals persisted before this field existed.
	Destination destination.Destination `json:"destination,omitempty"`
}
