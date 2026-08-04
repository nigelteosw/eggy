package ports

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

var ErrStateVersionConflict = errors.New("state version conflict")

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	// Effect is what ModeNormal acts on. Its zero value says the tool changes
	// something, so a tool nobody classified is gated rather than trusted:
	// forgetting costs an approval prompt, and the opposite mistake costs the
	// mutation itself.
	Effect ToolEffect `json:"effect,omitzero"`
}

// ToolEffect classifies what calling a tool does.
//
// It is declared by the tool rather than looked up in a table somewhere else,
// because a table is a second place to forget: whoever adds an action is
// already editing the file the classification lives in.
//
// MCP tools are the deliberate exception and leave this alone. A remote
// catalog cannot be classified from here -- nothing in Eggy knows whether
// railway_deploy writes -- so an MCP server stays governed by the
// require_approval list on its own configuration, which is the trust decision
// the owner already made by configuring it.
type ToolEffect struct {
	// ReadOnly marks a tool that changes nothing outside Eggy, and is the one
	// claim that lets a call through in ModeNormal.
	ReadOnly bool `json:"read_only,omitempty"`
	// Mutations names the actions that write, for a tool carrying several
	// operations behind one schema. Empty on a tool that is not ReadOnly means
	// every call to it writes.
	Mutations []string `json:"mutations,omitempty"`
}

// GateAllTool is the action name that means "every call to this tool writes".
// Spelling it out is clearer at a call site than an empty Mutations, which
// reads like nobody filled it in -- and is what the zero value already means.
const GateAllTool = "*"

// ReadOnlyTool is the classification for a tool that only reads.
func ReadOnlyTool() ToolEffect { return ToolEffect{ReadOnly: true} }

// MutatingActions classifies a tool whose named actions write and whose others
// do not.
func MutatingActions(actions ...string) ToolEffect { return ToolEffect{Mutations: actions} }

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ModelRequest struct {
	Model           string           `json:"model"`
	Messages        []Message        `json:"messages"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
}

type ModelResponse struct {
	Message Message    `json:"message"`
	Usage   ModelUsage `json:"usage,omitzero"`
	// ReasoningContent is the model's visible chain-of-thought for this
	// response, when the provider returns one. It is never fed back into a
	// following request's message history.
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type ModelUsage struct {
	PromptTokens       int64 `json:"prompt_tokens"`
	CompletionTokens   int64 `json:"completion_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	CachedPromptTokens int64 `json:"cached_prompt_tokens,omitempty"`
	ReasoningTokens    int64 `json:"reasoning_tokens,omitempty"`
}

func (u ModelUsage) Add(other ModelUsage) ModelUsage {
	return ModelUsage{
		PromptTokens:       u.PromptTokens + other.PromptTokens,
		CompletionTokens:   u.CompletionTokens + other.CompletionTokens,
		TotalTokens:        u.TotalTokens + other.TotalTokens,
		CachedPromptTokens: u.CachedPromptTokens + other.CachedPromptTokens,
		ReasoningTokens:    u.ReasoningTokens + other.ReasoningTokens,
	}
}

type Model interface {
	Generate(context.Context, ModelRequest) (ModelResponse, error)
}

type Tool interface {
	Definition() ToolDefinition
	Execute(context.Context, json.RawMessage) (json.RawMessage, error)
}

// Channel delivers agent output to one surface. It deliberately carries no
// chat or thread identifier: the target is the destination stamped on ctx
// for the turn (see internal/kernel/destination.Destination), so a tool or
// helper constructed once at startup reports into whichever conversation is
// actually running rather than into a fixed one baked in at construction.
//
// The port covers delivery only. Acknowledging a Telegram callback query is
// part of *receiving* an update and lives in that adapter's webhook handler,
// not here, so no other surface has to implement a concept it doesn't have.
//
// Channel is the floor every surface must reach: send text, and ask for a
// decision. In-place edits and typing indicators are surface-specific
// affordances, so they live in the optional TrackableChannel and
// TypingChannel extensions rather than forcing a surface without them to
// stub methods it cannot honour. Consumers type-assert for the extension
// they want and degrade when it is absent -- see plugins/channels/
// channelutil, which does exactly that once so callers don't repeat it.
type Channel interface {
	Deliver(ctx context.Context, text string) error
	DeliverApproval(ctx context.Context, approval approvals.Approval) error
}

// TrackableChannel is a Channel whose messages can be revised after the
// fact: DeliverTrackable returns a handle for the message it sent, and
// EditText rewrites that message in place. Surfaces use it to keep one live
// message for a long-running run instead of a message per step.
type TrackableChannel interface {
	Channel
	DeliverTrackable(ctx context.Context, text string) (messageID string, err error)
	EditText(ctx context.Context, messageID, text string) error
}

// TypingChannel is a Channel that can show the user that a turn is in
// progress. The indicator is advisory: a surface without one is still a
// perfectly good Channel.
type TypingChannel interface {
	Channel
	SendTyping(ctx context.Context) error
}

type AgentContext struct {
	Soul   string `json:"soul"`
	User   string `json:"user"`
	Memory string `json:"memory"`
	// UserMaxBytes and MemoryMaxBytes are the write budgets ContextStore
	// enforces on the two agent-writable documents, used to render an
	// in-context usage indicator. Zero suppresses the indicator. Soul has no
	// budget: it is owner-editable and never agent-written.
	UserMaxBytes   int64 `json:"user_max_bytes,omitempty"`
	MemoryMaxBytes int64 `json:"memory_max_bytes,omitempty"`
}

type ContextDocument string

const (
	ContextSoul   ContextDocument = "soul"
	ContextUser   ContextDocument = "user"
	ContextMemory ContextDocument = "memory"
)

// ContextStore holds the agent's durable context documents. Only User and
// Memory are writable; Soul is owner-editable and load-only.
//
// Entries are plain lines, addressed by a substring of their text rather than
// by any structural key, so the agent never has to model the file's layout.
// AddEntry appends one. ReplaceEntry and RemoveEntry act on the single entry
// containing oldText, and error when it matches no entry or more than one.
type ContextStore interface {
	Load(context.Context) (AgentContext, error)
	AddEntry(ctx context.Context, document ContextDocument, text string) error
	ReplaceEntry(ctx context.Context, document ContextDocument, oldText, text string) error
	RemoveEntry(ctx context.Context, document ContextDocument, oldText string) error
}

// StoredMessage is one durable, provider-neutral conversation message.
type StoredMessage struct {
	ID int64
	// ConversationID scopes a message to one thread: a web thread's own
	// generated ID, or Telegram's fixed, reserved thread ID. Never empty for
	// a message written through MemoryStore.WriteMessage.
	ConversationID string
	Role           Role
	Content        string
	Source         string
	CreatedAt      time.Time
}

// MemoryStore persists and recalls durable conversation messages.
type MemoryStore interface {
	WriteMessage(context.Context, StoredMessage) error
	// RecentMessages returns conversationID's most recent messages, oldest
	// first, bounded to limit -- the thread-scoped live turn-context window
	// that replaced a former global field on State.
	RecentMessages(ctx context.Context, conversationID string, limit int) ([]StoredMessage, error)
	// ResetConversation clears conversationID's live turn-context window
	// without deleting its durable history.
	ResetConversation(ctx context.Context, conversationID string, at time.Time) error
	SearchText(context.Context, string, int) ([]StoredMessage, error)
}

// Thread is one conversation surface: a web sidebar conversation, or
// Telegram's single fixed thread. Title is empty until auto-titled from the
// thread's first exchange.
//
// A thread is also where an attached workspace lives. Workspace is the
// checkout the thread's primitive tools act on, empty when none is
// attached; WorkspaceRepository names the repository it was cloned from.
// Keeping them here lets repository exploration continue across turns.
type Thread struct {
	ID                  string
	Title               string
	Channel             string
	Workspace           string
	WorkspaceRepository string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// ThreadStore persists conversation threads. It is deliberately separate
// from MemoryStore: MemoryStore models messages, and a thread is a distinct
// concept that surfaces (web chat) and the kernel (workspace attachment)
// both need without either depending on a concrete storage adapter.
type ThreadStore interface {
	CreateThread(ctx context.Context, id, channel string, at time.Time) (Thread, error)
	ListThreads(ctx context.Context, channel string) ([]Thread, error)
	// GetThread reports found=false with a nil error when no such thread
	// exists.
	GetThread(ctx context.Context, id string) (thread Thread, found bool, err error)
	// SetThreadTitle auto-titles a thread; a no-op once it has a title.
	SetThreadTitle(ctx context.Context, id, title string) error
	// RenameThread sets a thread's title outright, overwriting any
	// auto-generated one. This is the owner naming the conversation.
	RenameThread(ctx context.Context, id, title string) error
	// DeleteThread removes a thread along with its messages and reset
	// marker. Deleting a thread that does not exist is not an error.
	DeleteThread(ctx context.Context, id string) error
	// AttachWorkspace records a checkout on a thread, creating the thread
	// row if this is a surface (Telegram) that never explicitly created
	// one. Replaces any previously attached workspace.
	AttachWorkspace(ctx context.Context, id, channel, repository, workspace string, at time.Time) error
	// DetachWorkspace clears a thread's attached workspace. Detaching a
	// thread with none is not an error.
	DetachWorkspace(ctx context.Context, id string) error
	// ThreadsWithWorkspace returns every thread that currently has a
	// workspace attached, for boot reconciliation and idle reaping.
	ThreadsWithWorkspace(ctx context.Context) ([]Thread, error)
}

// WorkspaceProbe reports whether a previously created workspace still
// exists on disk. A Runner implements it so a restart can reconcile durable
// thread -> checkout bindings against reality instead of trusting a record
// whose directory a volume wipe removed.
type WorkspaceProbe interface {
	Exists(ctx context.Context, workspace string) (bool, error)
}

// SkillSummary is the compact, always-in-context view of one installed
// skill: enough for the agent to decide whether to load its full body with
// skill_read, without paying for that body on every turn.
type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Skill is one installed skill's full content, returned only when fetched
// by name (skill_read, /skills show), never held resident across a whole
// turn's context the way SkillSummary is.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

// SkillsStore persists procedural skills as one Markdown file per skill.
// Write is deliberately create-or-replace, whole-file: unlike ContextStore's
// section-addressed edits, a skill has no durable internal structure worth
// patching in place. Nothing in this port executes a skill; it only reads
// and writes its Markdown text.
type SkillsStore interface {
	List(context.Context) ([]SkillSummary, error)
	Read(context.Context, string) (Skill, error)
	Write(context.Context, string, string, string) error
	Delete(context.Context, string) error
}

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
}

type StateStore interface {
	Load(context.Context) (State, error)
	Update(context.Context, uint64, func(*State) error) (State, error)
}

type ScheduleKind string

const (
	ScheduleExact     ScheduleKind = "exact"
	ScheduleRecurring ScheduleKind = "recurring"
)

// ScheduleExecution distinguishes a deterministic, pre-rendered notification
// from a schedule that starts a model turn. ScheduleExecutionMessage covers
// reminders and watchdog-style notifications: Instruction is delivered to
// the owner verbatim at fire time with no model call. ScheduleExecutionAgent
// (the default, including for schedules persisted before this field existed)
// runs Instruction as a self-contained, read-only agent turn.
type ScheduleExecution string

const (
	ScheduleExecutionAgent   ScheduleExecution = "agent"
	ScheduleExecutionMessage ScheduleExecution = "message"
)

type Schedule struct {
	ID          string            `json:"id"`
	Kind        ScheduleKind      `json:"kind"`
	Execution   ScheduleExecution `json:"execution,omitempty"`
	Instruction string            `json:"instruction"`
	Expression  string            `json:"expression,omitempty"`
	NextRun     time.Time         `json:"next_run"`
	LastRun     time.Time         `json:"last_run,omitzero"`
	PendingRun  time.Time         `json:"pending_run,omitzero"`
	Enabled     bool              `json:"enabled"`
}

type Command struct {
	Argv      []string
	Dir       string
	Env       map[string]string
	Timeout   time.Duration
	MaxOutput int64
}

type CommandResult struct {
	Stdout          string
	Stderr          string
	ExitCode        int
	OutputTruncated bool
}

type Runner interface {
	Create(context.Context, string) (string, error)
	Execute(context.Context, Command) (CommandResult, error)
	Destroy(context.Context, string) error
}

type Repository struct {
	Name              string
	CloneURL          string
	BaseBranch        string
	ProtectedBranches []string
}

type RepositoryCheckout interface {
	Clone(context.Context, Repository, string) error
}

type RepositorySummary struct {
	Number        int    `json:"number,omitempty"`
	Title         string `json:"title,omitempty"`
	State         string `json:"state,omitempty"`
	Body          string `json:"body,omitempty"`
	URL           string `json:"url,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Private       bool   `json:"private,omitempty"`
}

type CheckRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	URL        string `json:"url,omitempty"`
}

// RepositoryReader answers read-only questions about a repository checkout and
// its GitHub metadata without launching a coding agent, a branch, or a commit.
type RepositoryReader interface {
	ReadFile(ctx context.Context, workspace, path string, startLine, endLine int) (string, error)
	RepositorySummary(ctx context.Context, repository Repository) (RepositorySummary, error)
	Issue(ctx context.Context, repository Repository, number int) (RepositorySummary, error)
	ReviewSummary(ctx context.Context, repository Repository, number int) (RepositorySummary, error)
	Checks(ctx context.Context, repository Repository, ref string) ([]CheckRun, error)
}

type ApprovalPolicy interface {
	Authorize(context.Context, approvals.Action, any, string) error
}
