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
}

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
	Usage   ModelUsage `json:"usage,omitempty"`
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

type WebSearchRequest struct {
	Query string
	Limit int
}

type WebSearchResult struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Snippet     string   `json:"snippet,omitempty"`
	PublishedAt string   `json:"published_at,omitempty"`
	Sources     []string `json:"sources,omitempty"`
}

type WebSearcher interface {
	Search(context.Context, WebSearchRequest) ([]WebSearchResult, error)
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
	// Heartbeat is the owner-editable HEARTBEAT.md checklist: what to look at
	// on a heartbeat turn. It is never injected into an ordinary conversation
	// turn's instructions — only a heartbeat turn renders it. Timing,
	// timezone, quiet hours, limits, and prohibited actions remain fixed Go
	// policy (HeartbeatPolicy, HeartbeatActionAllowed) and are not part of
	// this file's content.
	Heartbeat string `json:"heartbeat"`
	// UserMaxBytes and MemoryMaxBytes are the write budgets ContextStore
	// enforces on the two agent-writable documents, used to render an
	// in-context usage indicator. Zero suppresses the indicator. Soul and
	// Heartbeat have no budget: they are owner-editable, never agent-written.
	UserMaxBytes   int64 `json:"user_max_bytes,omitempty"`
	MemoryMaxBytes int64 `json:"memory_max_bytes,omitempty"`
}

type ContextDocument string

const (
	ContextSoul      ContextDocument = "soul"
	ContextUser      ContextDocument = "user"
	ContextMemory    ContextDocument = "memory"
	ContextHeartbeat ContextDocument = "heartbeat"
)

// ContextStore holds the agent's durable context documents. Only User and
// Memory are writable; Soul and Heartbeat are owner-editable and load-only.
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

// StoredMessage is one durable conversation message. It deliberately carries
// only provider-neutral conversation data, never an embedding representation.
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
	// that replaced the old global State.RecentMessages.
	RecentMessages(ctx context.Context, conversationID string, limit int) ([]StoredMessage, error)
	// ResetConversation clears conversationID's live turn-context window
	// (later RecentMessages calls only see messages recorded after at) without
	// deleting its durable history: SearchText/SearchSimilar keep finding
	// everything.
	ResetConversation(ctx context.Context, conversationID string, at time.Time) error
	SearchText(context.Context, string, int) ([]StoredMessage, error)
	SearchSimilar(context.Context, []float32, int) ([]StoredMessage, error)
	PendingEmbeddings(context.Context, int) ([]StoredMessage, error)
	SetEmbedding(context.Context, int64, []float32) error
}

// Thread is one conversation surface: a web sidebar conversation, or
// Telegram's single fixed thread. Title is empty until auto-titled from the
// thread's first exchange.
//
// A thread is also where an attached workspace lives. Workspace is the
// checkout the thread's primitive tools act on, empty when none is
// attached; WorkspaceRepository names the repository it was cloned from.
// Keeping them here rather than on an implementation run is what makes
// inspect -> edit -> discuss one continuous thread rather than a lane
// transition.
//
// WorkspaceBranch is empty while the checkout is an inspection clone still
// sitting on the base branch, and holds the branch name once the thread has
// started editing. It is what makes the checkout writable: the same
// directory keeps serving the thread's reads before, during, and after the
// edits, with no second clone. ChangeID names the Change those edits belong
// to, which propose_change ships.
type Thread struct {
	ID                  string
	Title               string
	Channel             string
	Workspace           string
	WorkspaceRepository string
	WorkspaceBranch     string
	ChangeID            string
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
	// AttachWorkspace records a checkout on a thread, creating the thread
	// row if this is a surface (Telegram) that never explicitly created
	// one. Replaces any previously attached workspace.
	AttachWorkspace(ctx context.Context, id, channel, repository, workspace string, at time.Time) error
	// SetWorkspaceEdit records the branch created in the thread's
	// already-attached checkout, and the change those edits belong to,
	// promoting the checkout from an inspection clone to a writable one. An
	// empty branch demotes it back.
	SetWorkspaceEdit(ctx context.Context, id, branch, changeID string) error
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

// Embedder produces an embedding for text. Storage adapters never depend on
// an Embedder; a kernel service coordinates the two through these ports.
type Embedder interface {
	Embed(context.Context, string) ([]float32, error)
}

// SkillSummary is the compact, always-in-context view of one installed
// skill: enough for the agent to decide whether to load its full body with
// skill_read, without paying for that body on every turn.
type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Disabled mirrors State.DisabledSkills for this skill. A disabled skill
	// is dropped from the steering list built for the agent, but its file is
	// untouched and it remains readable by exact name.
	Disabled bool `json:"disabled,omitempty"`
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
	RecentMessages    []Message                     `json:"recent_messages,omitempty"`
	Approvals         map[string]approvals.Approval `json:"approvals,omitempty"`
	Schedules         map[string]Schedule           `json:"schedules,omitempty"`
	Repositories      map[string]Repository         `json:"repositories,omitempty"`
	ProcessedEvents   map[string]time.Time          `json:"processed_events,omitempty"`
	ProactiveMessages []time.Time                   `json:"proactive_messages,omitempty"`
	// Calendar is retained only so a state.json written by an older Eggy
	// can be migrated into auth.json at boot. Nothing reads it at runtime.
	Calendar CalendarAuth      `json:"calendar,omitempty"`
	Agent    AgentRuntimeState `json:"agent,omitempty"`
	// DisabledSkills names skills currently excluded from the compact
	// steering index built for the agent. Disabling never removes or edits
	// the skill's file, so it carries no approval gate, unlike SkillsStore.Write/Delete.
	DisabledSkills map[string]bool `json:"disabled_skills,omitempty"`
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
	LastRun     time.Time         `json:"last_run,omitempty"`
	PendingRun  time.Time         `json:"pending_run,omitempty"`
	Enabled     bool              `json:"enabled"`
}

type CodingProgress struct {
	Kind    string
	Message string
	RunID   string
}

// ProgressReporter delivers one implementation run's progress to a surface.
// ctx is the turn's own context, so a reporter that ultimately writes to a
// Channel routes to the destination that started the run rather than to a
// fixed default. A nil ProgressReporter means "nobody is watching".
type ProgressReporter func(context.Context, CodingProgress)

// SessionPhase is a session's lifecycle in the only three states anything
// actually branches on. The finer-grained phases this replaced (ready,
// committed, pushed, interrupted, cancelled) were never read for a decision:
// they were rendered in one column of /runs and duplicated milestones that
// SetPhase already appends to the durable event stream, which is where the
// detail belongs.
type SessionPhase string

const (
	// PhaseRunning means the session is actively progressing.
	PhaseRunning SessionPhase = "running"
	// PhaseCompleted means the change shipped: a pull request was created,
	// or an already-open one for the branch was reused.
	PhaseCompleted SessionPhase = "completed"
	// PhaseBlocked means the session stopped short and an owner may want to
	// look: a restart landed mid-session, an integrity check failed, or the
	// commit -> push -> pull-request chain stopped partway. The reason is the
	// milestone recorded alongside the transition.
	PhaseBlocked SessionPhase = "blocked"
)

const (
	SessionAssistantMessage = "assistant_message"
	SessionToolStart        = "tool_start"
	SessionToolResult       = "tool_result"
	SessionToolError        = "tool_error"
	SessionTerminal         = "terminal"
	SessionMilestone        = "milestone"
)

// Transcript is the durable record of one turn: what was asked, what the
// model did, in order. Every turn has one, editing or not, so it carries no
// repository, branch, or lifecycle -- a conversation about the weather is a
// transcript too, and had none of those to record.
//
// The event log is persisted separately (one append-only file per
// transcript) so it never inflates this metadata document or state.json.
type Transcript struct {
	ID          string            `json:"id"`
	Instruction string            `json:"instruction,omitempty"`
	StartedAt   time.Time         `json:"started_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	FinishedAt  time.Time         `json:"finished_at,omitempty"`
	Events      []TranscriptEvent `json:"-"`
}

type TranscriptEvent struct {
	Sequence     uint64    `json:"sequence,omitempty"`
	At           time.Time `json:"at"`
	Kind         string    `json:"kind"`
	Message      string    `json:"message,omitempty"`
	ToolName     string    `json:"tool_name,omitempty"`
	Content      string    `json:"content,omitempty"`
	ModelMessage Message   `json:"model_message,omitempty"`
}

type TranscriptStore interface {
	Create(context.Context, Transcript) (Transcript, error)
	Load(context.Context, string) (Transcript, error)
	List(context.Context) ([]Transcript, error)
	AppendEvent(context.Context, string, TranscriptEvent) (Transcript, error)
	Update(context.Context, string, func(*Transcript) error) (Transcript, error)
}

// Change is one branched, shippable unit of work: what a thread's checkout
// was branched to do, and how far it got. It deliberately records no
// workspace path -- the live checkout belongs to the thread (see Thread and
// services.WorkspaceSessions), and a change outlives it. Repository and
// Branch are not that same duplication: they are what this change *was*,
// which stays meaningful long after the checkout is reaped.
type Change struct {
	ID           string       `json:"id"`
	Repository   string       `json:"repository"`
	Branch       string       `json:"branch"`
	BaseRevision string       `json:"base_revision,omitempty"`
	Phase        SessionPhase `json:"phase"`
	// Model is the reasoning-model alias selected when the change was opened.
	// It is recorded at creation rather than read back at display time, so
	// /runs show reports the model that did the work even after /model
	// switches. Empty on changes opened before this was recorded.
	Model             string `json:"model,omitempty"`
	Diff              string `json:"diff,omitempty"`
	Validation        string `json:"validation,omitempty"`
	Commit            string `json:"commit,omitempty"`
	PullRequestURL    string `json:"pull_request_url,omitempty"`
	PullRequestNumber int    `json:"pull_request_number,omitempty"`
	// ChecksRef and ChecksConclusion record the last commit whose
	// pull-request checks Eggy has already reacted to, so a shipped change is
	// resumed once per failing result rather than on every poll.
	ChecksRef        string `json:"checks_ref,omitempty"`
	ChecksConclusion string `json:"checks_conclusion,omitempty"`
	// Unprompted records that a scheduled or heartbeat turn opened this
	// change, so a later unprompted turn can tell its own work from an
	// owner's open change in the same thread and never continue theirs.
	Unprompted bool      `json:"unprompted,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

type ChangeStore interface {
	Create(context.Context, Change) (Change, error)
	Load(context.Context, string) (Change, error)
	List(context.Context) ([]Change, error)
	Update(context.Context, string, func(*Change) error) (Change, error)
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

type PullRequest struct {
	URL    string
	Number int
}

type WorkspaceRevision struct {
	Branch string
	Head   string
}

// WorkspaceInspector lets coding workflows verify repository control-plane
// invariants without depending on a specific source-control provider.
type WorkspaceInspector interface {
	WorkspaceRevision(context.Context, string) (WorkspaceRevision, error)
}

type RepositoryCapabilities struct {
	Commit      bool
	Push        bool
	PullRequest bool
}

// RepositoryCapabilityProvider reports adapter readiness without exposing
// provider credentials or provider-specific types to the kernel.
type RepositoryCapabilityProvider interface {
	RepositoryCapabilities() RepositoryCapabilities
}

type RemoteChecker interface {
	CheckRemote(context.Context, Repository, string) error
}

type RepositoryCheckout interface {
	Clone(context.Context, Repository, string) error
	Inspect(context.Context, string) (string, error)
	CreateBranch(context.Context, string, string) error
	Diff(context.Context, string) (string, error)
}

type RepositoryCommitter interface {
	Diff(context.Context, string) (string, error)
	Commit(context.Context, string, string) (string, error)
}

type RepositoryPusher interface {
	Head(context.Context, string) (string, error)
	Push(context.Context, string, string) error
}

type PullRequestProvider interface {
	RemoteHead(context.Context, string, string) (string, error)
	// CreatePullRequest opens a pull request for branch. draft asks the
	// provider to open it as a draft, which is what an unprompted turn's
	// proposal is: something the owner reviews and marks ready, never
	// something that presents itself as finished work.
	CreatePullRequest(ctx context.Context, repository Repository, branch, title, body string, draft bool) (PullRequest, error)
	// FindOpenPullRequest looks up an already-open pull request for branch,
	// so shipping can keep improving the same pull request across repeated
	// /continue rounds instead of opening a new one every time. found is
	// false, with a nil error, when no open pull request exists yet.
	FindOpenPullRequest(ctx context.Context, repository Repository, branch string) (pr PullRequest, found bool, err error)
	// UpdatePullRequestBody appends a short note to an already-open pull
	// request's description, e.g. after reusing it for another round of
	// changes. Best-effort: callers should not fail the whole shipping
	// chain if this fails, since the code change and the pull request
	// itself are already in place.
	UpdatePullRequestBody(ctx context.Context, repository Repository, number int, note string) error
}

// CodingRepository is the complete repository contract required by the coding
// workflow. New providers extend Eggy by implementing this port in an adapter.
type CodingRepository interface {
	RepositoryCheckout
	WorkspaceInspector
}

type WorkspaceEntry struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

type WorkspaceMatch struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
	Text string `json:"text,omitempty"`
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
	ListTree(ctx context.Context, workspace, path string, maxEntries int) ([]WorkspaceEntry, error)
	Search(ctx context.Context, workspace, query string, maxMatches int) ([]WorkspaceMatch, error)
	ReadFile(ctx context.Context, workspace, path string, startLine, endLine int) (string, error)
	Status(ctx context.Context, workspace string) (string, error)
	Branches(ctx context.Context, workspace string) ([]string, error)
	RepositorySummary(ctx context.Context, repository Repository) (RepositorySummary, error)
	Issue(ctx context.Context, repository Repository, number int) (RepositorySummary, error)
	PullRequestSummary(ctx context.Context, repository Repository, number int) (RepositorySummary, error)
	Checks(ctx context.Context, repository Repository, ref string) ([]CheckRun, error)
}

// CalendarAuthStore persists the owner's Google Calendar OAuth credential.
// It is deliberately separate from StateStore: the credential lives in
// auth.json alongside every other provider credential, not in the runtime
// state file (see plugins/auth/authfile).
type CalendarAuthStore interface {
	Load(context.Context) (CalendarAuth, error)
	Update(context.Context, func(*CalendarAuth) error) error
}

type CalendarAuth struct {
	EncryptedRefreshToken string    `json:"encrypted_refresh_token,omitempty"`
	TokenExpiry           time.Time `json:"token_expiry,omitempty"`
	EnrollmentDigest      string    `json:"enrollment_digest,omitempty"`
	EnrollmentExpires     time.Time `json:"enrollment_expires,omitempty"`
}

type CalendarEvent struct {
	ID             string    `json:"id,omitempty"`
	CalendarID     string    `json:"calendar_id"`
	Title          string    `json:"title"`
	Description    string    `json:"description,omitempty"`
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	Participants   []string  `json:"participants,omitempty"`
	ETag           string    `json:"etag,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	// URL is the provider's own web link for viewing this event (e.g.
	// Google Calendar's htmlLink), populated by List/Create/Update -- never
	// sent back on a mutation request, since it is provider-assigned.
	URL string `json:"url,omitempty"`
}

type CalendarInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	AccessRole string `json:"access_role"`
	Primary    bool   `json:"primary"`
	Hidden     bool   `json:"hidden"`
}

type CalendarProvider interface {
	AuthorizationURL(string) string
	ExchangeCode(context.Context, string) (CalendarAuth, error)
	ListCalendars(context.Context) ([]CalendarInfo, error)
	List(context.Context, string, time.Time, time.Time) ([]CalendarEvent, error)
	Create(context.Context, CalendarEvent) (CalendarEvent, error)
	Update(context.Context, CalendarEvent) (CalendarEvent, error)
	Delete(context.Context, string, string, string) error
}

type ApprovalPolicy interface {
	Authorize(context.Context, approvals.Action, any, string) error
}
