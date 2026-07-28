// Package turns owns what happens during one turn: the tool allowlist a turn
// runs with, the context it is built from, the transcript it records, and the
// rules that separate an owner-prompted turn from an unprompted one.
//
// It exists because that is core agentic behavior rather than wiring. It used
// to live in internal/bootstrap, the composition root, where no kernel test
// could guard it -- and where the safety-relevant part (which turns are
// unprompted, and what those turns may reach) sat next to HTTP clients and
// adapter selection.
//
// Every collaborator is a narrow interface declared here rather than a whole
// service, for the same reason internal/commands and internal/web declare
// theirs: a turn should not be able to reach past what it needs. Presentation
// stays outside -- the kernel may not import plugins/, and the typing hint and
// live "Calling X..." indicator are surface affordances anyway. They arrive as
// the Presenter interface.
package turns

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/kernel/events"
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
	Begin(ctx context.Context, steerable bool) (context.Context, func())
	Steer(ctx context.Context, text string) bool
	Pending(ctx context.Context) []ports.Message
	Active() bool
}

// Conversation is the durable message history a turn reads for context and
// appends its own exchange to.
type Conversation interface {
	Record(ctx context.Context, conversationID string, message ports.Message, source string) error
	RecentMessages(ctx context.Context, conversationID string) ([]ports.Message, error)
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
	Run(ctx context.Context, alias, effort, input string, history []ports.Message, options agent.RunOptions) (agent.RunResult, error)
	ToolNames(options agent.RunOptions) []string
}

// Transcripts is the durable per-turn record. Load and List are absent: a
// turn writes its transcript, it never reads another's.
type Transcripts interface {
	Open(ctx context.Context, id, instruction string) (ports.Transcript, error)
	Append(ctx context.Context, id string, event ports.TranscriptEvent) error
	Close(ctx context.Context, id string) error
	RedactProgress(content string) string
}

// ProgressReporter delivers a milestone on the turn's destination.
type ProgressReporter interface {
	Deliver(ctx context.Context, progress ports.CodingProgress)
}

// WorkspaceResolver reports the checkout bound to the calling thread, so a
// turn knows whether it is an editing turn.
type WorkspaceResolver interface {
	Resolve(ctx context.Context) (services.WorkspaceBinding, error)
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

// HeartbeatGate decides whether a heartbeat may send an owner-facing check-in
// and records one against the weekly limit. It governs *sending* only: silent
// context curation runs regardless.
type HeartbeatGate interface {
	CanSend(state ports.State, now time.Time) bool
	Record(ctx context.Context, store ports.StateStore, now time.Time) error
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
	Transcripts  Transcripts
	Progress     ProgressReporter
	Workspaces   WorkspaceResolver
	Threads      ThreadTitler
	Approvals    ApprovalDecider
	Executors    map[approvals.Action]ApprovalExecutor
	Heartbeat    HeartbeatGate
	Presenter    Presenter
	Manifest     agent.CapabilityManifest
	Logger       *slog.Logger
	Now          func() time.Time
	Location     *time.Location
	Timezone     string
}

// Service runs turns. One instance serves every surface: Telegram and web are
// peers that each only decide which entry point to call.
type Service struct {
	commands     CommandExecutor
	registry     Registry
	conversation Conversation
	context      ports.ContextStore
	store        ports.StateStore
	runtime      Runtime
	skills       SkillIndex
	loop         Loop
	channel      ports.Channel
	transcripts  Transcripts
	progress     ProgressReporter
	workspaces   WorkspaceResolver
	threads      ThreadTitler
	approvals    ApprovalDecider
	executors    map[approvals.Action]ApprovalExecutor
	heartbeat    HeartbeatGate
	presenter    Presenter
	manifest     agent.CapabilityManifest
	logger       *slog.Logger
	now          func() time.Time
	location     *time.Location
	timezone     string
}

func New(options Options) *Service {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	location := options.Location
	if location == nil {
		location = time.UTC
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		commands: options.Commands, registry: options.Registry, conversation: options.Conversation,
		context: options.Context, store: options.Store, runtime: options.Runtime,
		skills: options.Skills, loop: options.Loop, channel: options.Channel,
		transcripts: options.Transcripts, progress: options.Progress, workspaces: options.Workspaces,
		threads: options.Threads, approvals: options.Approvals, executors: options.Executors,
		heartbeat: options.Heartbeat, presenter: options.Presenter, manifest: options.Manifest,
		logger: logger, now: now, location: location, timezone: options.Timezone,
	}
}

// Policy is what differs between kinds of turn: whether it carries ambient
// recent-conversation history, whether its exchange is recorded, and what to
// attribute the messages to.
type Policy struct {
	IncludeRecentHistory bool
	RecordConversation   bool
	Source               string
}

// ReadOnlyTools is the floor every restricted turn starts from.
//
// read_file and terminal resolve their workspace from session state, so a
// read-only turn needs workspace_open/close to have anything to read. Both
// stay read-only: an attached checkout has no branch, and the write
// primitives remain off this list.
func ReadOnlyTools() agent.RunOptions {
	return agent.RunOptions{AllowedTools: map[string]bool{
		"status": true, "repository_list": true, "calendar_list": true,
		"read_file": true, "terminal": true, "repository_github": true,
		"workspace_open": true, "workspace_close": true,
		"skill_read": true,
	}}
}

// ProposeOnlyTools is what a scheduled turn runs with: readOnlyTools plus the
// tools needed to make and propose a change.
//
// An older allowlist barred repository writes outright. That was a proxy for
// the invariant that matters -- nothing lands without a payload-bound
// authorization and a human-reviewed pull request -- and the proxy cost Eggy
// the ability to improve itself between owner messages. The invariant is now
// held where it belongs rather than by absence: ScheduledTurn marks the turn
// unprompted, propose_change opens a draft pull request on a branch of its
// own, ShippingService refuses a base or protected branch, and an unprompted
// turn cannot touch a change the owner has open.
//
// What stays barred is unchanged: no MCP tool, so an unprompted turn still
// reaches no arbitrary remote side effect.
func ProposeOnlyTools() agent.RunOptions {
	options := ReadOnlyTools()
	for _, tool := range []string{"workspace_edit", "patch", "write_file", "propose_change"} {
		options.AllowedTools[tool] = true
	}
	return options
}

// HeartbeatTools extends ReadOnlyTools -- deliberately not ProposeOnlyTools --
// with the narrow memory-curation tools, so a heartbeat can write stable facts
// to USER.md/MEMORY.md.
//
// A heartbeat is a check-in on the owner, not a work tick. Its job is to
// notice what is worth telling them and to curate durable context; it is not
// the place to start repository work nobody asked for. A scheduled turn is the
// propose path, because the owner wrote the schedule that asks for it. Keeping
// the write tools off a heartbeat also keeps its cost proportionate: every
// tick is a model call, and one that cannot edit is a far cheaper one.
func HeartbeatTools() agent.RunOptions {
	options := ReadOnlyTools()
	for _, tool := range []string{"memory", "skill_disable", "skill_enable"} {
		options.AllowedTools[tool] = true
	}
	return options
}

// OwnerMessage runs a direct owner turn: the complete tool set, ambient
// recent-conversation history, and a recorded exchange. It is the only kind
// of turn a later owner message can steer.
func (s *Service) OwnerMessage(ctx context.Context, text, source string) error {
	if strings.TrimSpace(source) == "" {
		source = "telegram"
	}
	return s.run(ctx, text, agent.RunOptions{}, Policy{
		IncludeRecentHistory: true,
		RecordConversation:   true,
		Source:               source,
	})
}

// ChecksTurn resumes the thread that proposed a change whose pull-request
// checks failed. It is an ordinary owner-facing turn on purpose: the
// workspace is still open on that branch, so the agent fixes the failure with
// the same tools and proposes again. Nothing about it is a separate mode --
// that is what makes self-improvement a loop instead of one shot.
func (s *Service) ChecksTurn(ctx context.Context, instruction string) error {
	return s.run(ctx, instruction, agent.RunOptions{}, Policy{
		IncludeRecentHistory: true,
		RecordConversation:   true,
		Source:               "checks",
	})
}

// ScheduledTurn runs a turn the owner scheduled but is not present for. It is
// self-contained: no ambient recent-conversation history, so an owner's
// earlier chat cannot silently steer instructions they never reviewed at the
// time this schedule fires. It is marked unprompted, which is what confines
// it to proposing.
func (s *Service) ScheduledTurn(ctx context.Context, text string) error {
	return s.run(services.WithUnpromptedTurn(ctx), text, ProposeOnlyTools(), Policy{})
}

// run is one turn, whatever kind. Everything above differs only in the tool
// allowlist, the policy, and whether the context is marked unprompted.
func (s *Service) run(ctx context.Context, text string, options agent.RunOptions, policy Policy) error {
	if s.commands != nil {
		if output, handled, err := s.commands.Execute(ctx, text); handled {
			if err != nil {
				return err
			}
			return s.channel.Deliver(ctx, output)
		}
	}
	// A message that arrives while a steerable turn is already running joins
	// that turn rather than starting a competing one. The owner gets to
	// redirect work in progress -- "actually, skip the tests" -- instead of
	// waiting for it to finish or racing it.
	if policy.RecordConversation && s.registry.Steer(ctx, text) {
		if err := s.conversation.Record(ctx, destination.FromContext(ctx).ConversationID(), ports.Message{Role: ports.RoleUser, Content: text}, policy.Source); err != nil {
			return err
		}
		return s.channel.Deliver(ctx, "Got it — folding that into what I'm working on.")
	}
	agentContext, err := s.context.Load(ctx)
	if err != nil {
		return err
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	alias, err := s.runtime.SelectedModel(ctx)
	if err != nil {
		return err
	}
	effort, err := s.runtime.ReasoningEffort(ctx)
	if err != nil {
		return err
	}
	enabledSkills, err := s.skills.Enabled(ctx)
	if err != nil {
		return err
	}
	manifest := s.capabilityManifest(state, alias, enabledSkills)
	manifest.Tools = s.loop.ToolNames(options)
	history := agent.BuildInstructions(agentContext, manifest, agent.TemporalContext{Now: s.now().In(s.location), Timezone: s.timezone})
	dest := destination.FromContext(ctx)
	if policy.IncludeRecentHistory {
		recent, err := s.conversation.RecentMessages(ctx, dest.ConversationID())
		if err != nil {
			s.logger.Error("recent conversation window unavailable", "conversation_id", dest.ConversationID(), "error", err)
		} else {
			history = append(history, recent...)
		}
	}
	finishToolProgress := func() {}
	onToolCall := func(string) {}
	if policy.RecordConversation && s.presenter != nil {
		onToolCall, finishToolProgress = s.presenter.ShowToolCalls(ctx)
	}
	options.OnEvent = turnEvents(onToolCall)
	// Every turn gets a durable transcript, editing or not.
	transcript, closeTranscript := s.openTranscript(ctx, text)
	defer closeTranscript()
	if transcript != nil {
		options.Transcript = transcript
		// The transcript travels on ctx the same way the destination does, so
		// a tool deep in the turn (propose_change -> Ship) records its
		// milestones against this turn without every signature carrying it.
		ctx = services.WithTranscript(ctx, transcript.session)
	}
	// Only a direct owner turn is steerable: a scheduled turn is deliberately
	// self-contained, and folding an owner message into one would hand it the
	// ambient instruction that isolation exists to prevent.
	turnContext, endTurn := s.registry.Begin(ctx, policy.RecordConversation)
	defer endTurn()
	turnContext = services.WithSelectedModel(turnContext, alias)
	options.PendingInput = func() []ports.Message { return s.registry.Pending(ctx) }
	stopTyping := func() {}
	if s.presenter != nil {
		stopTyping = s.presenter.StartTyping(ctx)
	}
	result, runErr := s.loop.Run(turnContext, alias, effort, text, history, options)
	stopTyping()
	finishToolProgress()
	endTurn()
	if errors.Is(runErr, context.Canceled) && ctx.Err() == nil {
		// The turn was stopped by the owner, not by the surface going away:
		// the milestone is reported on ctx so it still reaches them.
		if usageErr := s.runtime.RecordUsage(ctx, alias, result.Usage); usageErr != nil {
			return usageErr
		}
		return s.channel.Deliver(ctx, "Stopped. The workspace is left as it was, so you can look at it or ask me to continue.")
	}
	usageErr := s.runtime.RecordUsage(ctx, alias, result.Usage)
	if errors.Is(runErr, agent.ErrToolStepLimit) {
		if usageErr != nil {
			return usageErr
		}
		return s.channel.Deliver(ctx, "I ran out of tool-call steps working on that before I could finish. Try a narrower request, or ask me to continue.")
	}
	if runErr != nil {
		return runErr
	}
	if usageErr != nil {
		return usageErr
	}
	if policy.RecordConversation {
		conversationID := dest.ConversationID()
		if err := s.conversation.Record(ctx, conversationID, ports.Message{Role: ports.RoleUser, Content: text}, policy.Source); err != nil {
			return err
		}
		if err := s.conversation.Record(ctx, conversationID, result.Message, policy.Source); err != nil {
			return err
		}
		if dest.Kind == destination.Web && s.threads != nil {
			if err := s.threads.SetThreadTitle(ctx, dest.ThreadID, truncateThreadTitle(text)); err != nil {
				s.logger.Error("thread auto-titling failed", "thread_id", dest.ThreadID, "error", err)
			}
		}
	}
	if strings.TrimSpace(result.ReasoningContent) != "" {
		showThinking, err := s.runtime.ShowThinking(ctx)
		if err != nil {
			return err
		}
		if showThinking {
			if err := s.channel.Deliver(ctx, "Thinking:\n"+result.ReasoningContent); err != nil {
				return err
			}
		}
	}
	return s.channel.Deliver(ctx, result.Message.Content)
}

// Active reports whether a turn is currently executing. A heartbeat tick is
// skipped entirely while one is, rather than interleaving a curation/check-in
// turn with live work. With one loop this is a property of the turn registry
// rather than of any session's phase: an owner editing a repository is simply
// a turn in progress.
func (s *Service) Active() bool { return s.registry.Active() }

// Heartbeat runs a small, self-contained check-in turn: no ambient
// recent-conversation history, so instructions from an old chat cannot be
// silently revived. Its context is the durable docs (SOUL/USER/MEMORY), the
// owner-editable HEARTBEAT.md checklist, and the capability manifest -- never
// state.RecentMessages.
//
// Silent context curation (USER.md/MEMORY.md) is never gated by quiet hours
// or the weekly proactive-message limit; only the owner-facing check-in is.
// HeartbeatGate.CanSend governs sending the check-in and recording it against
// the weekly limit, not whether the turn runs at all.
func (s *Service) Heartbeat(ctx context.Context) error {
	if s.Active() {
		return nil
	}
	// Marked unprompted for the same reason a scheduled turn is. A heartbeat
	// carries no repository write tools today, so nothing reads the mark --
	// but if it ever regains one it inherits the draft-only rules rather than
	// silently gaining owner privileges.
	ctx = services.WithUnpromptedTurn(ctx)
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	sendAllowed := s.heartbeat.CanSend(state, s.now())
	agentContext, err := s.context.Load(ctx)
	if err != nil {
		return err
	}
	alias, err := s.runtime.SelectedModel(ctx)
	if err != nil {
		return err
	}
	effort, err := s.runtime.ReasoningEffort(ctx)
	if err != nil {
		return err
	}
	enabledSkills, err := s.skills.Enabled(ctx)
	if err != nil {
		return err
	}
	manifest := s.capabilityManifest(state, alias, enabledSkills)
	options := HeartbeatTools()
	// Registered but not steerable: /stop can cancel a heartbeat turn, and a
	// second one cannot start while it runs, but an owner message never joins
	// it.
	heartbeatContext, endTurn := s.registry.Begin(ctx, false)
	defer endTurn()
	heartbeatContext = services.WithSelectedModel(heartbeatContext, alias)
	transcript, closeTranscript := s.openTranscript(ctx, "heartbeat")
	defer closeTranscript()
	if transcript != nil {
		options.Transcript = transcript
		ctx = services.WithTranscript(ctx, transcript.session)
	}
	manifest.Tools = s.loop.ToolNames(options)
	history := agent.BuildInstructions(agentContext, manifest, agent.TemporalContext{Now: s.now().In(s.location), Timezone: s.timezone})
	history = append(history, agent.HeartbeatChecklistMessage(agentContext.Heartbeat))
	history = append(history, ports.Message{Role: ports.RoleSystem, Content: "Heartbeat context only: an isolated turn with no recent-conversation history. This is a check-in on the owner, not a work tick: decide whether anything is worth telling them, and curate durable context. You carry no repository write tools here, so do not plan or promise repository work -- if something needs changing, say so and let the owner ask."})
	instruction := "Separately, review durable context for any stable fact, preference, or decision worth curating into USER.md or MEMORY.md: use the read tool to see the current document first, append or replace a section for new or changed facts, and remove a section outright once it is stale, superseded, or duplicated. Curation does not require sending a check-in."
	if sendAllowed {
		instruction = "Evaluate whether one concise proactive check-in is useful now, using the HEARTBEAT.md checklist as a starting point. " + instruction + fmt.Sprintf(" Reply with exactly %q and nothing else when no check-in is useful.", services.HeartbeatNoReportSentinel)
	} else {
		instruction = "A proactive check-in cannot be sent right now (quiet hours or the proactive-message limit). Do not attempt one. " + instruction + fmt.Sprintf(" Reply with exactly %q.", services.HeartbeatNoReportSentinel)
	}
	result, runErr := s.loop.Run(heartbeatContext, alias, effort, instruction, history, options)
	usageErr := s.runtime.RecordUsage(ctx, alias, result.Usage)
	if runErr != nil {
		return runErr
	}
	if usageErr != nil {
		return usageErr
	}
	if !sendAllowed || services.HeartbeatHasNothingToReport(result.Message.Content) {
		return nil
	}
	if err := s.heartbeat.Record(ctx, s.store, s.now()); err != nil {
		return err
	}
	return s.channel.Deliver(ctx, result.Message.Content)
}

// Approval executes what an owner's approve tap authorized, or reports the
// rejection. The destination is taken from the approval itself, so the
// outcome reaches the surface the approval was issued on.
func (s *Service) Approval(ctx context.Context, decision events.ApprovalDecision) error {
	preState, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	ctx = destination.With(ctx, preState.Approvals[decision.ApprovalID].Destination)
	if err := s.approvals.Decide(ctx, decision.ApprovalID, decision.Approved); err != nil {
		return s.deliverApprovalFailure(ctx, decision.MessageID, err)
	}
	if !decision.Approved {
		return s.presenter.DeliverOutcome(ctx, decision.MessageID, "Action rejected.")
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return s.deliverApprovalFailure(ctx, decision.MessageID, err)
	}
	approval := state.Approvals[decision.ApprovalID]
	executor, ok := s.executors[approval.Action]
	if !ok {
		return s.deliverApprovalFailure(ctx, decision.MessageID, errors.New("unknown approval action"))
	}
	result, err := executor.ExecuteApproved(ctx, approval)
	if err != nil {
		return s.deliverApprovalFailure(ctx, decision.MessageID, err)
	}
	return s.presenter.DeliverOutcome(ctx, decision.MessageID, fmt.Sprintf("Approved action completed: %v", result))
}

// deliverApprovalFailure tells the owner an approve/reject tap didn't go
// through, instead of leaving execErr to only reach the server log. Without
// this, a tap that produces no visible outcome at all is indistinguishable
// from a broken button, and the owner has no way to learn what actually
// failed. Still returns execErr so the failure remains logged server-side.
func (s *Service) deliverApprovalFailure(ctx context.Context, messageID string, execErr error) error {
	if deliverErr := s.presenter.DeliverOutcome(ctx, messageID, fmt.Sprintf("Action failed: %v", execErr)); deliverErr != nil {
		return errors.Join(execErr, deliverErr)
	}
	return execErr
}

// turnEvents fans the loop's event stream out to the live "Calling <tool>..."
// indicator. Everything durable goes through the loop's own Transcript
// instead: a transcript belongs to a turn, not to whichever turns happened to
// be editing a repository.
func turnEvents(onToolCall func(string)) func(agent.Event) {
	return func(event agent.Event) {
		if event.Kind == agent.EventToolStart {
			onToolCall(event.Call.Name)
		}
	}
}

// capabilityManifest is the base manifest narrowed to what this turn can
// actually do. Readiness flags report shipping-adapter availability, so they
// are meaningless without a configured repository and are cleared when there
// is none.
func (s *Service) capabilityManifest(state ports.State, activeModel string, skills []ports.SkillSummary) agent.CapabilityManifest {
	manifest := s.manifest
	manifest.ActiveModel = activeModel
	manifest.Repositories = make([]string, 0, len(state.Repositories))
	for name := range state.Repositories {
		manifest.Repositories = append(manifest.Repositories, name)
	}
	configured := len(manifest.Repositories) > 0
	manifest.RepositoryCommitReady = configured && manifest.RepositoryCommitReady
	manifest.RepositoryPushReady = configured && manifest.RepositoryPushReady
	manifest.PullRequestReady = configured && manifest.PullRequestReady
	manifest.Skills = make([]agent.SkillDescriptor, 0, len(skills))
	for _, skill := range skills {
		manifest.Skills = append(manifest.Skills, agent.SkillDescriptor{Name: skill.Name, Description: skill.Description})
	}
	return manifest
}

// truncateThreadTitle cheaply derives a web thread's auto-title from its
// first user message: no separate model call for v1.
func truncateThreadTitle(text string) string {
	const maxRunes = 60
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "…"
}

func newTranscriptID() string {
	data := make([]byte, 6)
	_, _ = rand.Read(data)
	return "turn-" + hex.EncodeToString(data)
}
