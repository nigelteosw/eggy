// Package turns owns what happens during one turn: its tools, context,
// persistence, and surface delivery.
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
	"encoding/json"
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
	// Begin registers the turn and returns the release function, which
	// reports whatever was steered into the turn but never drained.
	Begin(ctx context.Context, steerable bool) (context.Context, func() []ports.Message)
	Steer(ctx context.Context, message ports.Message) bool
	Pending(ctx context.Context) []ports.Message
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
}

// Service runs turns. One instance serves every surface: Telegram and web are
// peers that each only decide which entry point to call.
//
// It embeds Options rather than restating all nineteen collaborators as
// lowercase fields and copying them across one at a time. The two lists were
// identical apart from case, so the only thing the copy bought was a third
// place to forget a field when adding one. A Service is built once by New and
// never written again.
type Service struct {
	Options
}

func New(options Options) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Location == nil {
		options.Location = time.UTC
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Service{Options: options}
}

// Policy is what differs between kinds of turn: whether it carries ambient
// recent-conversation history, whether its exchange is recorded, and what to
// attribute the messages to.
type Policy struct {
	IncludeRecentHistory bool
	RecordConversation   bool
	Source               string
	// Extra are system messages appended after the shared instructions, for
	// rules that govern one kind of turn only. Keeping them here rather than in
	// agent.Instructions is what stops one turn kind's rules from riding along
	// on every other turn -- see agent.ScheduledTurnMessage.
	Extra []ports.Message
	// SuppressSilentReply lets a turn conclude there is nothing worth saying.
	// Only the heartbeat sets it: every other kind of turn answers something
	// the owner asked for and must always deliver.
	SuppressSilentReply bool
	// IncludeWatchDocument appends the watch list as a per-turn system
	// message. Only the heartbeat sets it: the document is the heartbeat's
	// own working memory and has no bearing on a turn the owner is present
	// for.
	IncludeWatchDocument bool
	// InputAlreadyRecorded marks a turn whose input is a message the
	// conversation already holds: a steer that its turn never got to read,
	// re-run as a turn of its own. Recording it a second time would show the
	// owner saying the same thing twice.
	InputAlreadyRecorded bool
	// Kind labels the turn in its trace: ports.TraceKindOwner,
	// TraceKindScheduled or TraceKindHeartbeat. It records which entry point
	// ran, which is the first thing worth knowing about a turn nobody
	// remembers asking for.
	Kind string
}

// ReadOnlyTools is the floor every restricted turn starts from.
//
// read_file and terminal resolve their workspace from session state, so a
// read-only turn needs workspace_open/close to have anything to read. Both
// stay read-only: an attached checkout has no branch, and the write
// primitives remain off this list.
func ReadOnlyTools() agent.RunOptions {
	return agent.RunOptions{AllowedTools: map[string]bool{
		"status": true, "repository_list": true,
		"read_file": true, "repository_github": true,
		"workspace_open": true, "workspace_close": true,
		"skill_read": true,
		// Reading what is scheduled is read-only, and it is the one thing a
		// heartbeat most plausibly needs: it shares a clock with those jobs
		// and should be able to say what else is due. The grant is scoped to
		// the schedule tool's list action: create and cancel stay off, because
		// an unprompted turn must not change what runs later.
		"schedule:list": true,
	}}
}

// OwnerMessage runs a direct owner turn: the complete tool set, ambient
// recent-conversation history, and a recorded exchange. It is the only kind
// of turn a later owner message can steer.
func (s *Service) OwnerMessage(ctx context.Context, message ports.Message, source string) error {
	if strings.TrimSpace(source) == "" {
		source = "telegram"
	}
	return s.run(ctx, message, agent.RunOptions{}, Policy{
		IncludeRecentHistory: true,
		RecordConversation:   true,
		Source:               source,
		Kind:                 ports.TraceKindOwner,
	})
}

// ScheduledTurn runs a turn the owner scheduled but is not present for. It is
// self-contained: no ambient recent-conversation history, so an owner's
// earlier chat cannot silently steer instructions they never reviewed at the
// time this schedule fires. It is marked unprompted, which is what confines
// it to proposing.
func (s *Service) ScheduledTurn(ctx context.Context, text string) error {
	return s.run(ctx, ports.Message{Role: ports.RoleUser, Content: text}, ReadOnlyTools(), Policy{
		Extra: []ports.Message{agent.ScheduledTurnMessage()},
		Kind:  ports.TraceKindScheduled,
	})
}

// HeartbeatTurn is a periodic check-in the owner is not present for. Its
// isolation is ScheduledTurn's, unchanged -- the same read-only allowlist and
// no ambient conversation history -- and the only difference is that it is
// allowed to conclude there is nothing worth saying.
// It carries the watch list and heartbeat_respond, which together are what
// let it conclude that it has already said this.
//
// The response is attached to the context and deliberately not stored on the
// Service: one Service instance serves every surface, so a field would be
// shared mutable state across turns. run reads it back off the context.
// includeRecentHistory relaxes the no-ambient-history rule, and is the
// owner's explicit choice rather than a default: see
// config.HeartbeatConfig.IncludeRecentHistory. The allowlist is unchanged
// either way, so this changes what a beat knows and never what it can do.
// The response is returned so the caller can act on what the beat decided --
// today, when it asked to be woken next. A beat that failed or never called
// the tool returns a zero response, which the caller reads as "no decision".
func (s *Service) HeartbeatTurn(ctx context.Context, text string, includeRecentHistory bool) (services.HeartbeatResponse, error) {
	ctx, response := services.WithHeartbeatResponse(ctx)
	err := s.run(ctx, ports.Message{Role: ports.RoleUser, Content: text}, heartbeatTools(), Policy{
		Extra:                []ports.Message{agent.HeartbeatTurnMessage()},
		SuppressSilentReply:  true,
		IncludeWatchDocument: true,
		IncludeRecentHistory: includeRecentHistory,
		Kind:                 ports.TraceKindHeartbeat,
	})
	return *response, err
}

// heartbeatTools is the read-only floor plus the heartbeat's own reply
// channel. heartbeat_respond stays out of ReadOnlyTools because a scheduled
// turn has no beat to end and must always deliver.
func heartbeatTools() agent.RunOptions {
	options := ReadOnlyTools()
	options.AllowedTools[services.HeartbeatRespondToolName] = true
	return options
}

// silentReply reports whether a heartbeat reply amounts to "nothing to say".
//
// A strict equality check is not enough: models reliably append a pleasantry
// to a sentinel, and "HEARTBEAT_OK -- all quiet!" on the owner's phone is the
// exact notification the heartbeat exists to avoid. So the token is
// recognised when it leads or trails the reply, stripped, and the reply
// dropped when what remains is short enough to be a pleasantry. The leniency
// only applies once the model has already declared nothing to report, so it
// cannot swallow a genuine short alert.
func silentReply(content string) bool {
	const pleasantryLimit = 300
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}
	for _, sentinel := range agent.HeartbeatSentinels {
		remainder, found := strings.CutPrefix(trimmed, sentinel)
		if !found {
			remainder, found = strings.CutSuffix(trimmed, sentinel)
		}
		if found && len(strings.TrimSpace(remainder)) <= pleasantryLimit {
			return true
		}
	}
	return false
}

// run is one turn, whatever kind. Everything above differs only in the tool
// allowlist, the policy, and whether the context is marked unprompted.
func (s *Service) run(ctx context.Context, input ports.Message, options agent.RunOptions, policy Policy) (err error) {
	// A steer that landed after the turn's last step boundary was accepted
	// and never read. It becomes a turn of its own once this one has
	// delivered, rather than being dropped: from the owner's side a steer is
	// a message they sent, and the one thing it may never do is disappear.
	//
	// A defer, so it runs after every path that delivers a reply, and it runs
	// after the deferred release below because defers unwind in reverse. The
	// error paths leave err set and take nothing further on: a turn that
	// failed has already told the owner something went wrong, and following
	// it with an answer to the steer would bury that.
	var steered []ports.Message
	defer func() {
		if err != nil || len(steered) == 0 {
			return
		}
		followUp := policy
		followUp.InputAlreadyRecorded = true
		err = s.run(ctx, mergeSteered(steered), options, followUp)
	}()
	text := input.Content
	durableText := durableMessageText(input)
	if s.Commands != nil && len(input.Parts) == 0 {
		if output, handled, err := s.Commands.Execute(ctx, text); handled {
			if err != nil {
				return err
			}
			return s.Channel.Deliver(ctx, output)
		}
	}
	// A message that arrives while a steerable turn is already running joins
	// that turn rather than starting a competing one. The owner gets to
	// redirect work in progress -- "actually, skip the tests" -- instead of
	// waiting for it to finish or racing it.
	//
	// Silently: the running turn's own reply is the acknowledgement, and it
	// is the only one that can say anything true about what the steer did.
	// A canned "folding that in" arrives before the turn has read the
	// message, claims on its behalf, and turns every mid-turn aside into two
	// notifications instead of one.
	if policy.RecordConversation && s.Registry.Steer(ctx, input) {
		if policy.InputAlreadyRecorded {
			return nil
		}
		return s.Conversation.Record(ctx, destination.FromContext(ctx).ConversationID(), ports.Message{Role: ports.RoleUser, Content: durableText}, policy.Source)
	}
	agentContext, err := s.Context.Load(ctx)
	if err != nil {
		return err
	}
	state, err := s.Store.Load(ctx)
	if err != nil {
		return err
	}
	alias, err := s.Runtime.SelectedModel(ctx)
	if err != nil {
		return err
	}
	effort, err := s.Runtime.ReasoningEffort(ctx)
	if err != nil {
		return err
	}
	enabledSkills, err := s.Skills.Enabled(ctx)
	if err != nil {
		return err
	}
	manifest := s.capabilityManifest(state, alias, enabledSkills)
	manifest.Tools = s.Loop.ToolNames(options)
	history := agent.BuildInstructions(agentContext, manifest, agent.TemporalContext{Now: s.Now().In(s.Location), Timezone: s.Timezone})
	history = append(history, policy.Extra...)
	if policy.IncludeWatchDocument && strings.TrimSpace(agentContext.Watch) != "" {
		history = append(history, agent.WatchDocumentMessage(agentContext.Watch))
	}
	dest := destination.FromContext(ctx)
	if policy.IncludeRecentHistory {
		recent, err := s.Conversation.RecentMessages(ctx, dest.ConversationID())
		if err != nil {
			s.Logger.Error("recent conversation window unavailable", "conversation_id", dest.ConversationID(), "error", err)
		} else {
			history = append(history, recent...)
		}
	}
	finishToolProgress := func() {}
	onToolCall := func(string) {}
	if policy.RecordConversation && s.Presenter != nil {
		onToolCall, finishToolProgress = s.Presenter.ShowToolCalls(ctx)
	}
	// The trace opens here, before the loop's context is derived, so every
	// model call and tool call the loop makes carries the trace ID. Reassigning
	// ctx is deliberate: everything downstream -- the loop, its tools, and the
	// delivery that follows -- must be on the traced context, and a second
	// variable would be a way to forget one of them.
	// A failed lookup costs the trace its grouping, not the turn: an
	// ungrouped trace is still the whole record of what ran.
	session, err := s.Conversation.SessionID(ctx, dest.ConversationID())
	if err != nil {
		s.Logger.Error("conversation session unavailable", "conversation_id", dest.ConversationID(), "error", err)
	}
	ctx, trace := s.Traces.Begin(ctx, ports.Trace{
		ConversationID: dest.ConversationID(),
		Session:        session,
		Channel:        string(dest.Kind),
		Source:         policy.Source,
		Kind:           policy.Kind,
		Model:          alias,
		Effort:         effort,
		Input:          durableText,
	})
	options.OnEvent = turnEvents(onToolCall, trace)
	// Only a direct owner turn is steerable: a scheduled turn is deliberately
	// self-contained, and folding an owner message into one would hand it the
	// ambient instruction that isolation exists to prevent.
	turnContext, endTurn := s.Registry.Begin(ctx, policy.RecordConversation)
	defer endTurn()
	turnContext = services.WithSelectedModel(turnContext, alias)
	options.PendingInput = func() []ports.Message { return s.Registry.Pending(ctx) }
	stopTyping := func() {}
	if s.Presenter != nil {
		stopTyping = s.Presenter.StartTyping(ctx)
	}
	result, runErr := s.Loop.Run(turnContext, alias, effort, input, history, options)
	stopTyping()
	finishToolProgress()
	steered = endTurn()
	// Completed here rather than in a defer, and before any of the branches
	// below can return: a turn that hit the step limit or that the owner
	// stopped is the one whose trace is most worth having.
	trace.Complete(ctx, result.Message.Content, runErr, result.Usage)
	if errors.Is(runErr, context.Canceled) && ctx.Err() == nil {
		// The turn was stopped by the owner, not by the surface going away:
		// the milestone is reported on ctx so it still reaches them.
		if usageErr := s.Runtime.RecordUsage(ctx, alias, result.Usage); usageErr != nil {
			return usageErr
		}
		return s.Channel.Deliver(ctx, "Stopped. The workspace is left as it was, so you can look at it or ask me to continue.")
	}
	usageErr := s.Runtime.RecordUsage(ctx, alias, result.Usage)
	if errors.Is(runErr, agent.ErrToolStepLimit) {
		if usageErr != nil {
			return usageErr
		}
		return s.Channel.Deliver(ctx, "I ran out of tool-call steps working on that before I could finish. Try a narrower request, or ask me to continue.")
	}
	if runErr != nil {
		return runErr
	}
	if usageErr != nil {
		return usageErr
	}
	if policy.RecordConversation {
		conversationID := dest.ConversationID()
		if !policy.InputAlreadyRecorded {
			if err := s.Conversation.Record(ctx, conversationID, ports.Message{Role: ports.RoleUser, Content: durableText}, policy.Source); err != nil {
				return err
			}
		}
		if err := s.Conversation.Record(ctx, conversationID, result.Message, policy.Source); err != nil {
			return err
		}
		if dest.Kind == destination.Web && s.Threads != nil {
			if err := s.Threads.SetThreadTitle(ctx, dest.ThreadID, truncateThreadTitle(text)); err != nil {
				s.Logger.Error("thread auto-titling failed", "thread_id", dest.ThreadID, "error", err)
			}
		}
	}
	// A structured decision wins over the text reply: a model that called
	// heartbeat_respond has already said what it wants delivered, and its
	// prose is working notes. The sentinel stays as the fallback for a model
	// that answered without calling the tool.
	//
	// Checked before the thinking block, not just before the reply: a silent
	// heartbeat that still pushed its reasoning would be the notification the
	// silence protocol exists to prevent.
	if policy.SuppressSilentReply {
		if response := services.HeartbeatResponseFromContext(ctx); response != nil && response.Responded {
			if !response.Notify {
				return nil
			}
			return s.Channel.Deliver(ctx, response.Text)
		}
		if silentReply(result.Message.Content) {
			return nil
		}
	}
	if strings.TrimSpace(result.ReasoningContent) != "" {
		showThinking, err := s.Runtime.ShowThinking(ctx)
		if err != nil {
			return err
		}
		if showThinking {
			if err := s.Channel.Deliver(ctx, "Thinking:\n"+result.ReasoningContent); err != nil {
				return err
			}
		}
	}
	return s.Channel.Deliver(ctx, result.Message.Content)
}

// mergeSteered folds undrained steers into the single input a turn takes.
//
// They are one turn rather than one each because that is what they would have
// been had the turn drained them: steering appends everything pending to the
// same step, and two messages the owner typed seconds apart are almost always
// one thought. Parts carry over so a steered image is not silently reduced to
// its caption.
func mergeSteered(messages []ports.Message) ports.Message {
	if len(messages) == 1 {
		return messages[0]
	}
	merged := ports.Message{Role: ports.RoleUser}
	texts := make([]string, 0, len(messages))
	for _, message := range messages {
		if trimmed := strings.TrimSpace(message.Content); trimmed != "" {
			texts = append(texts, trimmed)
		}
		merged.Parts = append(merged.Parts, message.Parts...)
	}
	merged.Content = strings.Join(texts, "\n")
	return merged
}

const imageAttachmentMarker = "[image attached]"

func durableMessageText(message ports.Message) string {
	text := strings.TrimSpace(message.Content)
	if len(message.Parts) == 0 {
		return text
	}
	if text == "" || text == "Describe this image." {
		return imageAttachmentMarker
	}
	return text + "\n" + imageAttachmentMarker
}

// Active reports whether a turn is currently executing.
func (s *Service) Active() bool { return s.Registry.Active() }

// Approval executes what an owner's approve tap authorized, or reports the
// rejection. The destination is taken from the approval itself, so the
// outcome reaches the surface the approval was issued on.
func (s *Service) Approval(ctx context.Context, decision events.ApprovalDecision) error {
	preState, err := s.Store.Load(ctx)
	if err != nil {
		return err
	}
	ctx = destination.With(ctx, preState.Approvals[decision.ApprovalID].Destination)
	if err := s.Approvals.Decide(ctx, decision.ApprovalID, decision.Approved); err != nil {
		return s.deliverApprovalFailure(ctx, decision.MessageID, err)
	}
	if !decision.Approved {
		return s.Presenter.DeliverOutcome(ctx, decision.MessageID, "Action rejected.")
	}
	state, err := s.Store.Load(ctx)
	if err != nil {
		return s.deliverApprovalFailure(ctx, decision.MessageID, err)
	}
	approval := state.Approvals[decision.ApprovalID]
	executor, ok := s.Executors[approval.Action]
	if !ok {
		return s.deliverApprovalFailure(ctx, decision.MessageID, errors.New("unknown approval action"))
	}
	result, err := executor.ExecuteApproved(ctx, approval)
	if err != nil {
		return s.deliverApprovalFailure(ctx, decision.MessageID, err)
	}
	return s.Presenter.DeliverOutcome(ctx, decision.MessageID, approvalOutcomeText(result))
}

// approvalOutcomeText renders what the owner reads after an approve tap.
//
// A tool that can say what it just did says it in its own result, under
// "summary" -- in the file whoever added the action was already editing, the
// same reason a tool declares its own effect there rather than in a table here.
// Anything else falls back to the record itself, which is right for an MCP tool
// this repository cannot write a sentence for, and wrong enough for the tools
// it can that they should carry a summary.
func approvalOutcomeText(result any) string {
	text, isText := result.(string)
	if !isText {
		return fmt.Sprintf("Approved action completed: %v", result)
	}
	var payload struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err == nil && strings.TrimSpace(payload.Summary) != "" {
		return "Done. " + payload.Summary
	}
	return "Approved action completed: " + text
}

// deliverApprovalFailure tells the owner an approve/reject tap didn't go
// through, instead of leaving execErr to only reach the server log. Without
// this, a tap that produces no visible outcome at all is indistinguishable
// from a broken button, and the owner has no way to learn what actually
// failed. Still returns execErr so the failure remains logged server-side.
func (s *Service) deliverApprovalFailure(ctx context.Context, messageID string, execErr error) error {
	if deliverErr := s.Presenter.DeliverOutcome(ctx, messageID, fmt.Sprintf("Action failed: %v", execErr)); deliverErr != nil {
		return errors.Join(execErr, deliverErr)
	}
	return execErr
}

// turnEvents fans the loop's event stream out to the live "Calling <tool>..."
// indicator.
// turnEvents is the one subscriber to the loop's event stream. It feeds two
// consumers that want the same moments for different reasons: the surface's
// live "Calling X..." indicator, and the trace. Keeping them on one
// subscription is what guarantees the record and what the owner watched
// describe the same turn.
func turnEvents(onToolCall func(string), trace *services.TraceTurn) func(agent.Event) {
	return func(event agent.Event) {
		switch event.Kind {
		case agent.EventToolStart:
			onToolCall(event.Call.Name)
			trace.ToolStarted(event.Call)
		case agent.EventToolEnd, agent.EventToolError:
			trace.ToolFinished(event.Call, event.Output, event.Err)
		}
	}
}

// capabilityManifest is the base manifest populated with the active model,
// configured repositories, and available skills for this turn.
func (s *Service) capabilityManifest(state ports.State, activeModel string, skills []ports.SkillSummary) agent.CapabilityManifest {
	manifest := s.Manifest
	manifest.ActiveModel = activeModel
	manifest.Repositories = make([]string, 0, len(state.Repositories))
	for name := range state.Repositories {
		manifest.Repositories = append(manifest.Repositories, name)
	}
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
