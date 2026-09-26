package ports

import (
	"context"
	"errors"
	"time"

	"github.com/nigelteosw/eggy/internal/core/approvals"
)

var ErrStateVersionConflict = errors.New("state version conflict")

type State struct {
	SchemaVersion     int                           `json:"schema_version"`
	Version           uint64                        `json:"version"`
	Approvals         map[string]approvals.Approval `json:"approvals,omitempty"`
	Repositories      map[string]Repository         `json:"repositories,omitempty"`
	ProcessedEvents   map[string]time.Time          `json:"processed_events,omitempty"`
	ProactiveMessages []time.Time                   `json:"proactive_messages,omitempty"`
	Agent             AgentRuntimeState             `json:"agent,omitzero"`
	// ApprovalMode decides which tool calls stop and ask. Empty means the
	// configured default, so state written before this field existed adopts
	// whatever config.yaml says rather than silently picking one. It is
	// durable rather than per-turn: a bypass the owner forgot they enabled
	// must still be visible after a restart, which is what /status reports it
	// for.
	ApprovalMode ApprovalMode `json:"approval_mode,omitempty"`
	// ApprovalAutoMode is the retired boolean this replaced. It is read once
	// at load to carry an existing bypass forward into ModeAuto and is never
	// written again -- an owner who left the gate off must not have it come
	// back on under them because the field was renamed.
	ApprovalAutoMode bool `json:"approval_auto_mode,omitempty"`
}

// ApprovalMode is how much the owner wants to be asked.
//
// The three are a ladder, and the middle rung is the one that has to be right:
// gating everything trains the owner to approve without reading, and gating
// nothing is a bypass. Reads run, writes ask.
type ApprovalMode string

const (
	// ModeStrict asks before every tool call, reads included. Named strict
	// rather than safe because safe mode already means the degraded boot --
	// config.yaml failed to load, repair page only -- and /status would
	// otherwise report two unrelated things by the same name.
	ModeStrict ApprovalMode = "strict"
	// ModeNormal asks before anything that changes something outside Eggy.
	ModeNormal ApprovalMode = "normal"
	// ModeAuto asks nothing. It is a deliberate, durable bypass; nothing may
	// select it on the owner's behalf.
	ModeAuto ApprovalMode = "auto"
)

// Valid reports whether a mode is one of the three. An unknown mode is never
// silently corrected to a working one: the strictness the owner asked for is
// not something to guess at.
func (m ApprovalMode) Valid() bool {
	return m == ModeStrict || m == ModeNormal || m == ModeAuto
}

type AgentRuntimeState struct {
	SelectedModel   string                `json:"selected_model,omitempty"`
	ReasoningEffort string                `json:"reasoning_effort,omitempty"`
	Usage           map[string]ModelUsage `json:"usage,omitempty"`
	// HideThinking suppresses delivery of the model's raw reasoning content
	// as a separate "Thinking:" message. Defaults to false (shown), so
	// state persisted before this field existed keeps today's behavior.
	HideThinking bool `json:"hide_thinking,omitempty"`
	// Heartbeat is this account's own switch for periodic check-ins. Off
	// until the person turns it on: an unprompted message is something they
	// should have asked for, and the deployment's heartbeat section only
	// sets the cadence for those who do.
	Heartbeat bool `json:"heartbeat,omitempty"`
}

type StateStore interface {
	Load(context.Context) (State, error)
	Update(context.Context, uint64, func(*State) error) (State, error)
}
