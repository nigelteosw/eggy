package approvals

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotAuthorized   = errors.New("action is not authorized")
	ErrExpired         = errors.New("approval expired")
	ErrPayloadChanged  = errors.New("approval payload changed")
	ErrProtectedBranch = errors.New("protected branch push denied")
)

type Action string

const (
	Commit         Action = "commit"
	Push           Action = "push"
	CreatePR       Action = "create_pull_request"
	CalendarCreate Action = "calendar_create"
	CalendarUpdate Action = "calendar_update"
	CalendarDelete Action = "calendar_delete"
	AddRepository  Action = "add_repository"
)

type Status string

const (
	Pending  Status = "pending"
	Approved Status = "approved"
	Rejected Status = "rejected"
	Expired  Status = "expired"
	Used     Status = "used"
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
}
