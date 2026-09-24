package turns

import (
	"context"
	"log/slog"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

// CommandExecutor runs a deterministic slash command, reporting handled=false
// when the input is not one so the turn falls through to the model.
type CommandExecutor interface {
	Execute(ctx context.Context, input string) (string, bool, error)
}

// Registry is the live-turn bookkeeping a turn participates in: it registers
// itself, accepts owner steering while it runs, and drains what arrived.
type Registry interface {
	Admit(ctx context.Context, message ports.Message, steerable bool, prepare func(owner bool) error) (services.TurnAdmission, error)
	Pending(ctx context.Context) []ports.Message
	Release(ctx context.Context) []ports.Message
	Active() bool
}

// Conversation is the durable message history a turn reads for context and
// appends its own exchange to.
type Conversation interface {
	Record(ctx context.Context, conversationID string, message ports.Message, source string) error
	RecentMessages(ctx context.Context, conversationID string) ([]ports.Message, error)
	// SessionID names the stretch of the conversation running now, so a
	// trace can say which side of a /clear it falls on.
	SessionID(ctx context.Context, conversationID string) (string, error)
}

// Runtime is the per-turn model selection and usage accounting. Unlike
// commands.AgentSettings next door, this one needs RecordUsage and none of
// the owner-facing setters: accumulating usage is exactly the turn path's job.
type Runtime interface {
	SelectedModel(ctx context.Context) (string, error)
	ReasoningEffort(ctx context.Context) (string, error)
	ShowThinking(ctx context.Context) (bool, error)
	RecordUsage(ctx context.Context, alias string, usage ports.ModelUsage) error
}

// SkillIndex lists the enabled skills that go into the capability manifest.
type SkillIndex interface {
	Enabled(ctx context.Context) ([]ports.SkillSummary, error)
}

// Loop is the tool-calling loop itself.
type Loop interface {
	Run(ctx context.Context, alias, effort string, input ports.Message, history []ports.Message, options agent.RunOptions) (agent.RunResult, error)
	ToolNames(options agent.RunOptions) []string
}

// ThreadTitler auto-titles a web thread from its first message.
type ThreadTitler interface {
	SetThreadTitle(ctx context.Context, id, title string) error
}

// ApprovalDecider records an owner's approve/reject decision.
type ApprovalDecider interface {
	Decide(ctx context.Context, id string, approved bool) error
}

// ApprovalExecutor performs the action an approval authorized.
type ApprovalExecutor interface {
	ExecuteApproved(context.Context, approvals.Approval) (any, error)
}

// Presenter is the surface-side rendering a turn asks for. It lives outside
// the kernel because the kernel may not import plugins/, and because a typing
// hint and an in-place "Calling X..." message are affordances a surface either
// has or doesn't. Every method is safe to call on a surface with neither.
type Presenter interface {
	// StartTyping shows work-in-progress and returns the function that stops
	// it.
	StartTyping(ctx context.Context) (stop func())
	// ShowToolCalls returns a per-tool-call callback and the function that
	// settles the indicator once the turn is done.
	ShowToolCalls(ctx context.Context) (onToolCall func(string), finish func())
	// DeliverOutcome reports the result of an approve/reject tap, editing the
	// original message when the surface supports it.
	DeliverOutcome(ctx context.Context, messageID, text string) error
}

// Options carries the collaborators a Service needs. Zero values are
// tolerated the same way commands.Options tolerates them: a nil optional
// collaborator degrades that one behavior rather than panicking.
type Options struct {
	Commands     CommandExecutor
	Registry     Registry
	Conversation Conversation
	Context      ports.ContextStore
	Store        ports.StateStore
	Runtime      Runtime
	Skills       SkillIndex
	Loop         Loop
	Channel      ports.Channel
	Threads      ThreadTitler
	Approvals    ApprovalDecider
	Executors    map[approvals.Action]ApprovalExecutor
	Presenter    Presenter
	// Traces records what a turn actually did -- every model call with its
	// prompt, every tool call with its arguments and output. Nil when
	// tracing is switched off, and every method on the returned turn
	// tolerates that, so the turn path carries no branch for it.
	Traces   *services.TraceRecorder
	Manifest agent.CapabilityManifest
	Logger   *slog.Logger
	Now      func() time.Time
	Location *time.Location
	Timezone string
	// PartSupport reports whether the model behind an alias accepts input of
	// one modality, and whether that answer is known.
	// Nil means no provider reports modalities, and every such turn proceeds.
	// Only a known "no" blocks one: an unknown model is sent the part rather
	// than refused on a guess.
	PartSupport func(ctx context.Context, alias string, kind ports.Modality) (supported, known bool)
}
