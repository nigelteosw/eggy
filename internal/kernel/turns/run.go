package turns

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

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
	// Admission owns the decision to join or execute. Registering the owner
	// before the remaining preparation closes the old Steer/Begin race while
	// leaving the durable preparation callback for Task 2.
	admission, err := s.Registry.Admit(ctx, input, policy.RecordConversation, func(bool) error { return nil })
	if err != nil {
		return err
	}
	if !admission.Owner {
		if policy.InputAlreadyRecorded {
			return nil
		}
		return s.Conversation.Record(ctx, destination.FromContext(ctx).ConversationID(), ports.Message{Role: ports.RoleUser, Content: durableText}, policy.Source)
	}
	ownerContext := admission.Context
	defer func() { s.Registry.Release(ownerContext) }()
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
	// An image or file handed to a model that has said it does not take that
	// kind is dropped silently by some providers, which answer as if it had
	// never been sent. Refusing here, before the model call, is the only
	// point at which the owner can be told the difference.
	if s.PartSupport != nil {
		for _, kind := range partKinds(input.Parts) {
			if supported, known := s.PartSupport(ctx, alias, kind); known && !supported {
				return s.Channel.Deliver(ctx, fmt.Sprintf("The current model (%s) does not accept %s. Send the content as text, or switch models with /model.", alias, partNoun(kind)))
			}
		}
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
	turnContext, cancelTurn := context.WithCancel(ctx)
	stopOwnerCancellation := context.AfterFunc(ownerContext, cancelTurn)
	defer func() {
		stopOwnerCancellation()
		cancelTurn()
	}()
	turnContext = services.WithSelectedModel(turnContext, alias)
	options.PendingInput = func() []ports.Message { return s.Registry.Pending(ownerContext) }
	stopTyping := func() {}
	if s.Presenter != nil {
		stopTyping = s.Presenter.StartTyping(ctx)
	}
	result, runErr := s.Loop.Run(turnContext, alias, effort, input, history, options)
	stopTyping()
	finishToolProgress()
	steered = s.Registry.Release(ownerContext)
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

// partKinds lists each content kind present in parts once, in first-seen
// order, so a message with three photos asks the model-support question once.
func partKinds(parts []ports.ContentPart) []ports.ContentType {
	var kinds []ports.ContentType
	for _, part := range parts {
		if !slices.Contains(kinds, part.Type) {
			kinds = append(kinds, part.Type)
		}
	}
	return kinds
}

func partNoun(kind ports.ContentType) string {
	switch kind {
	case ports.ContentTypeDocument:
		return "files"
	default:
		return "images"
	}
}

// durableMessageText is what the conversation record keeps of a message
// that carried parts: the bytes never persist, so the record names what was
// attached. The surface's placeholder prompts are dropped because they were
// never the owner's words.
func durableMessageText(message ports.Message) string {
	text := strings.TrimSpace(message.Content)
	if len(message.Parts) == 0 {
		return text
	}
	if text == "Describe this image." || text == "Read this file." {
		text = ""
	}
	for _, part := range message.Parts {
		marker := "[image attached]"
		if part.Type == ports.ContentTypeDocument {
			marker = "[file attached: " + part.Filename + "]"
		}
		if text != "" {
			text += "\n"
		}
		text += marker
	}
	return text
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
