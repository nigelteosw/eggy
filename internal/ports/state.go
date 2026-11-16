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
	// Theme is this account's own panel palette: "light", "dark" or "system".
	// Empty means the account has not chosen, and the panel follows the
	// deployment's appearance default. It is per person, so one account's
	// choice never changes another's screen.
	Theme string `json:"theme,omitempty"`
	// Look is this account's own panel look: a built-in name, or "custom"
	// for CustomLook. Empty means the default look. Fonts are separate so
	// any look can take any of the offered faces; empty means the default.
	Look       string            `json:"look,omitempty"`
	CustomLook *PaletteOverrides `json:"custom_look,omitempty"`
	TextFont   string            `json:"text_font,omitempty"`
	CodeFont   string            `json:"code_font,omitempty"`
}

// PaletteOverrides are colour tokens laid over the panel's default palette,
// per mode, keyed by token name without its leading dashes ("primary") with an
// OKLCH "L% C H" value. A mode left out keeps the default palette.
type PaletteOverrides struct {
	Light map[string]string `json:"light,omitempty"`
	Dark  map[string]string `json:"dark,omitempty"`
}

// LookView is an account's look as the panel paints it and the settings tool
// reports it: the chosen names, and the overrides the chosen look resolves to.
type LookView struct {
	Look      string           `json:"look"`
	TextFont  string           `json:"text_font"`
	CodeFont  string           `json:"code_font"`
	Overrides PaletteOverrides `json:"overrides"`
	HasCustom bool             `json:"has_custom"`
	Looks     []string         `json:"looks"`
	Options   []LookOption     `json:"options"`
	TextFonts []string         `json:"text_fonts"`
	CodeFonts []string         `json:"code_fonts"`
}

// UserTheme is a look someone saved for everyone on the deployment, beside
// the built-in themes in internal/themes. It is shared: every account can
// choose it, and only the account that saved it can delete it. Its palettes
// are checked like a custom look's before it is stored.
type UserTheme struct {
	Name        string
	Label       string
	Description string
	Light       map[string]string
	Dark        map[string]string
	CreatedBy   string
	CreatedAt   time.Time
}

// ThemeStore keeps user themes, in creation order.
type ThemeStore interface {
	ListThemes(ctx context.Context) ([]UserTheme, error)
	CreateTheme(ctx context.Context, theme UserTheme) error
	DeleteTheme(ctx context.Context, name string) error
}

var (
	ErrThemeExists   = errors.New("a theme with that name already exists")
	ErrThemeNotFound = errors.New("no theme with that name")
)

// LookOption is one look as the panel offers it: its name and words, and the
// OKLCH colours its swatch is drawn from -- the light and dark backgrounds and
// the accent. The panel holds no colours of its own for a look.
type LookOption struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Light       string `json:"light"`
	Dark        string `json:"dark"`
	Accent      string `json:"accent"`
	// User marks a theme someone saved, and Mine one this account saved and
	// may delete.
	User bool `json:"user,omitempty"`
	Mine bool `json:"mine,omitempty"`
}

// LookChange is one write to an account's look. A nil field keeps what is
// stored; Custom replaces the stored custom look whole and selects it.
type LookChange struct {
	Look     *string
	Custom   *PaletteOverrides
	TextFont *string
	CodeFont *string
}

type StateStore interface {
	Load(context.Context) (State, error)
	Update(context.Context, uint64, func(*State) error) (State, error)
}

// Describe says, in the owner's terms, what a mode does. Telegram's /mode reply
// and the web panel's choice both read it, so the two surfaces give the same
// answer to "what happens if I pick this".
func (m ApprovalMode) Describe() string {
	switch m {
	case ModeStrict:
		return "I ask before every tool I use, even to read or look something up."
	case ModeAuto:
		return "I don't ask. I read, change and send things for you on my own."
	default:
		return "I read and look things up on my own, and ask before I change or send anything."
	}
}
