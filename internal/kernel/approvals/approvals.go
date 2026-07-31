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

// Action names one protected operation. An approval authorizes exactly one
// Action against one payload digest, so approving a deletion never authorizes
// a different deletion and never authorizes a create or update. No action is
// declared here today: the mechanism outlives any particular caller, and each
// protected surface registers its own executor in bootstrap.
type Action string

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
