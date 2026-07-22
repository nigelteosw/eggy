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

type Channel interface {
	Deliver(context.Context, string, string) error
	DeliverApproval(context.Context, string, approvals.Approval) error
	DeliverTrackable(ctx context.Context, chatID, text string) (messageID string, err error)
	EditText(ctx context.Context, chatID, messageID, text string) error
	AnswerCallback(ctx context.Context, callbackQueryID string) error
	SendTyping(ctx context.Context, chatID string) error
}

type AgentContext struct {
	Soul   string `json:"soul"`
	User   string `json:"user"`
	Memory string `json:"memory"`
	// MaxBytes is the per-document capacity ContextStore enforces on Soul,
	// User, and Memory, used to render an in-context usage indicator on User
	// and Memory. Zero means unknown/unbounded and suppresses the indicator.
	MaxBytes int64 `json:"max_bytes,omitempty"`
}

type ContextDocument string

const (
	ContextSoul   ContextDocument = "soul"
	ContextUser   ContextDocument = "user"
	ContextMemory ContextDocument = "memory"
)

type ContextStore interface {
	Load(context.Context) (AgentContext, error)
	Append(context.Context, ContextDocument, string, string) error
	ReplaceSection(context.Context, ContextDocument, string, string) error
	RemoveSection(context.Context, ContextDocument, string) error
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
	Calendar          CalendarAuth                  `json:"calendar,omitempty"`
	Agent             AgentRuntimeState             `json:"agent,omitempty"`
	// DisabledSkills names skills currently excluded from the compact
	// steering index built for the agent. Disabling never removes or edits
	// the skill's file, so it carries no approval gate, unlike SkillsStore.Write/Delete.
	DisabledSkills map[string]bool `json:"disabled_skills,omitempty"`
}

type AgentRuntimeState struct {
	SelectedModel   string                `json:"selected_model,omitempty"`
	ReasoningEffort string                `json:"reasoning_effort,omitempty"`
	Usage           map[string]ModelUsage `json:"usage,omitempty"`
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

type Schedule struct {
	ID          string       `json:"id"`
	Kind        ScheduleKind `json:"kind"`
	Instruction string       `json:"instruction"`
	Expression  string       `json:"expression,omitempty"`
	NextRun     time.Time    `json:"next_run"`
	LastRun     time.Time    `json:"last_run,omitempty"`
	PendingRun  time.Time    `json:"pending_run,omitempty"`
	Enabled     bool         `json:"enabled"`
}

type Scheduler interface {
	Add(context.Context, Schedule) error
	Remove(context.Context, string) error
	Due(context.Context, time.Time) ([]Schedule, error)
	Next(string, time.Time) (time.Time, error)
}

type CodingProgress struct {
	Kind    string
	Message string
	RunID   string
}

type CodingResult struct {
	Summary       string
	Validation    string
	CommitMessage string
	ChangedFiles  []string
}

// SessionPhase is the single lifecycle phase for an implementation
// session, replacing the formerly separate CodingRun.Status string and
// ImplementationSessionStatus enum. The awaiting_push_approval and
// awaiting_pr_approval phases from the old two-store design are gone
// entirely: ShippingService.Ship runs commit, push, and pull-request
// creation back to back with automatic approval, so those were
// instantaneous internal milestones, never a real crash-recovery window.
// PhaseReady is their one necessary survivor: the handoff between
// CodingService finishing an implementation run and a separate Ship call
// (e.g. from /continue) is a real gap another process restart can land in.
type SessionPhase string

const (
	// PhaseRunning means the implementation loop is actively executing.
	PhaseRunning SessionPhase = "running"
	// PhaseReady means implementation finished (Diff/Validation captured)
	// and the run is waiting to be shipped; resumable.
	PhaseReady SessionPhase = "ready"
	// PhaseInterrupted means the process restarted mid-run; resumable.
	PhaseInterrupted SessionPhase = "interrupted"
	// PhaseBlocked means implementation failed or an integrity check
	// failed (branch/HEAD moved, workspace missing); resumable so an
	// owner can inspect and retry.
	PhaseBlocked SessionPhase = "blocked"
	// PhaseCommitted means a commit was made but push did not complete
	// (unavailable or denied).
	PhaseCommitted SessionPhase = "committed"
	// PhasePushed means the branch was pushed but pull-request creation
	// did not complete (unavailable).
	PhasePushed SessionPhase = "pushed"
	// PhaseCompleted means a pull request was created (or an existing
	// open one for the branch was reused).
	PhaseCompleted SessionPhase = "completed"
	// PhaseCancelled is reserved for an owner-cancelled run at rest; no
	// code path sets it yet.
	PhaseCancelled SessionPhase = "cancelled"
)

const (
	SessionAssistantMessage = "assistant_message"
	SessionToolStart        = "tool_start"
	SessionToolResult       = "tool_result"
	SessionToolError        = "tool_error"
	SessionTerminal         = "terminal"
	SessionMilestone        = "milestone"
)

type SessionContext struct {
	Summary        string    `json:"summary,omitempty"`
	RecentMessages []Message `json:"recent_messages,omitempty"`
}

// ImplementationSession is the single canonical record of a coding run's
// metadata and lifecycle: repository, workspace, branch, base revision,
// current phase, validation evidence, commit, and pull request. The
// resumable context (SessionContext) and the bounded event history
// (Events) round out the aggregate; the event log itself is persisted
// separately (one append-only file per session) so transcripts never
// inflate this metadata document or state.json.
type ImplementationSession struct {
	ID                string                       `json:"id"`
	Repository        string                       `json:"repository,omitempty"`
	Instruction       string                       `json:"instruction,omitempty"`
	Workspace         string                       `json:"workspace,omitempty"`
	Branch            string                       `json:"branch,omitempty"`
	BaseRevision      string                       `json:"base_revision,omitempty"`
	Phase             SessionPhase                 `json:"phase"`
	Diff              string                       `json:"diff,omitempty"`
	Validation        string                       `json:"validation,omitempty"`
	Commit            string                       `json:"commit,omitempty"`
	PullRequestURL    string                       `json:"pull_request_url,omitempty"`
	PullRequestNumber int                          `json:"pull_request_number,omitempty"`
	Context           SessionContext               `json:"context,omitempty"`
	StartedAt         time.Time                    `json:"started_at"`
	UpdatedAt         time.Time                    `json:"updated_at"`
	FinishedAt        time.Time                    `json:"finished_at,omitempty"`
	Events            []ImplementationSessionEvent `json:"-"`
}

type ImplementationSessionEvent struct {
	Sequence     uint64    `json:"sequence,omitempty"`
	At           time.Time `json:"at"`
	Kind         string    `json:"kind"`
	Message      string    `json:"message,omitempty"`
	ToolName     string    `json:"tool_name,omitempty"`
	Content      string    `json:"content,omitempty"`
	ModelMessage Message   `json:"model_message,omitempty"`
}

type ImplementationSessionStore interface {
	Create(context.Context, ImplementationSession) (ImplementationSession, error)
	Load(context.Context, string) (ImplementationSession, error)
	List(context.Context) ([]ImplementationSession, error)
	AppendEvent(context.Context, string, ImplementationSessionEvent) (ImplementationSession, error)
	Update(context.Context, string, func(*ImplementationSession) error) (ImplementationSession, error)
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
	CreatePullRequest(context.Context, Repository, string, string, string) (PullRequest, error)
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
