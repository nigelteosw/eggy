package ports

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/kernel/tasks"
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
	Model    string           `json:"model"`
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
}

type ModelResponse struct {
	Message Message    `json:"message"`
	Usage   ModelUsage `json:"usage,omitempty"`
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
}

type MemoryStore interface {
	Load(context.Context) (string, error)
	Append(context.Context, string, string) error
	ReplaceSection(context.Context, string, string) error
}

type AgentContext struct {
	Soul   string `json:"soul"`
	User   string `json:"user"`
	Memory string `json:"memory"`
}

type ContextDocument string

const (
	ContextUser   ContextDocument = "user"
	ContextMemory ContextDocument = "memory"
)

type ContextStore interface {
	Load(context.Context) (AgentContext, error)
	Append(context.Context, ContextDocument, string, string) error
	ReplaceSection(context.Context, ContextDocument, string, string) error
}

type State struct {
	SchemaVersion       int                           `json:"schema_version"`
	Version             uint64                        `json:"version"`
	RecentMessages      []Message                     `json:"recent_messages,omitempty"`
	ConversationSummary string                        `json:"conversation_summary,omitempty"`
	SelectedRepository  string                        `json:"selected_repository,omitempty"`
	Tasks               map[string]tasks.Task         `json:"tasks,omitempty"`
	Approvals           map[string]approvals.Approval `json:"approvals,omitempty"`
	Schedules           map[string]Schedule           `json:"schedules,omitempty"`
	CodingRuns          map[string]CodingRun          `json:"coding_runs,omitempty"`
	ProcessedEvents     map[string]time.Time          `json:"processed_events,omitempty"`
	ProactiveMessages   []time.Time                   `json:"proactive_messages,omitempty"`
	Calendar            CalendarAuth                  `json:"calendar,omitempty"`
	Agent               AgentRuntimeState             `json:"agent,omitempty"`
}

type AgentRuntimeState struct {
	SelectedModel string                `json:"selected_model,omitempty"`
	Usage         map[string]ModelUsage `json:"usage,omitempty"`
}

type StateStore interface {
	Load(context.Context) (State, error)
	Update(context.Context, uint64, func(*State) error) (State, error)
}

type ScheduleKind string

const (
	ScheduleExact     ScheduleKind = "exact"
	ScheduleRecurring ScheduleKind = "recurring"
	ScheduleHeartbeat ScheduleKind = "heartbeat"
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

type TriggerSource interface {
	Events() <-chan events.Event
	Start(context.Context) error
}

type CodingRun struct {
	ID         string    `json:"id"`
	Repository string    `json:"repository"`
	Workspace  string    `json:"workspace"`
	Branch     string    `json:"branch"`
	Commit     string    `json:"commit,omitempty"`
	Status     string    `json:"status"`
	Diff       string    `json:"diff,omitempty"`
	Validation string    `json:"validation,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

type CodingRequest struct {
	RunID       string
	Workspace   string
	Instruction string
	Environment map[string]string
	ReadOnly    bool
}

type CodingProgress struct {
	Kind    string
	Message string
}

type CodingResult struct {
	Summary       string
	Validation    string
	CommitMessage string
}

type CodingAgent interface {
	Run(context.Context, CodingRequest, func(CodingProgress)) (CodingResult, error)
	Interrupt(string) error
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

type StreamingRunner interface {
	Runner
	ExecuteStreaming(context.Context, Command, func(string)) (CommandResult, error)
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

type RepositoryProvider interface {
	Clone(context.Context, Repository, string) error
	Inspect(context.Context, string) (string, error)
	CreateBranch(context.Context, string, string) error
	Head(context.Context, string) (string, error)
	RemoteHead(context.Context, string, string) (string, error)
	Diff(context.Context, string) (string, error)
	Commit(context.Context, string, string) (string, error)
	Push(context.Context, string, string) error
	CreatePullRequest(context.Context, Repository, string, string, string) (PullRequest, error)
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

type CalendarProvider interface {
	AuthorizationURL(string) string
	ExchangeCode(context.Context, string) (CalendarAuth, error)
	List(context.Context, string, time.Time, time.Time) ([]CalendarEvent, error)
	Create(context.Context, CalendarEvent) (CalendarEvent, error)
	Update(context.Context, CalendarEvent) (CalendarEvent, error)
	Delete(context.Context, string, string, string) error
}

type ApprovalPolicy interface {
	Authorize(context.Context, approvals.Action, any, string) error
}
