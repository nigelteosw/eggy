package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/channels/channelutil"
	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

// This file is App's runtime behavior once NewApp has wired it: the event
// loop (Run), event dispatch (HandleEvent/Enqueue/processEvent), a
// conversation turn (handleMessage), approval execution (handleApproval),
// and the periodic heartbeat (handleHeartbeat). See app.go for
// construction.

func (a *App) HandleEvent(ctx context.Context, event events.Event) error {
	return a.dispatcher.Handle(ctx, event)
}

func (a *App) Enqueue(ctx context.Context, event events.Event) error {
	select {
	case a.eventQueue <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return errors.New("event queue is full")
	}
}

func (a *App) processEvent(ctx context.Context, event events.Event) error {
	switch event.Type {
	case events.TypeMessage:
		message, err := decodeMessage(event, a.config.Telegram.OwnerID)
		if err != nil {
			return err
		}
		source := strings.TrimSpace(event.Source)
		if source == "" {
			source = "telegram"
		}
		ctx = approvals.WithDestination(ctx, destinationFromEvent(event, message))
		return a.handleMessage(ctx, message, agent.RunOptions{}, messageHandlingPolicy{
			includeRecentHistory: true,
			recordConversation:   true,
			source:               source,
		})
	case events.TypeSchedule:
		// A scheduled agent turn is self-contained: it starts with no ambient
		// recent-conversation history, so an owner's earlier chat cannot
		// silently steer instructions the owner never reviewed at the time
		// this schedule fires.
		message, err := decodeMessage(event, a.config.Telegram.OwnerID)
		if err != nil {
			return err
		}
		return a.handleMessage(ctx, message, readOnlyRunOptions(), messageHandlingPolicy{})
	case events.TypeScheduledMessage:
		// A deterministic, pre-rendered notification (a reminder or
		// watchdog-style check-in): delivered verbatim with no model call at
		// all, as distinct from TypeSchedule above.
		message, err := decodeMessage(event, a.config.Telegram.OwnerID)
		if err != nil {
			return err
		}
		return a.channel.Deliver(ctx, message.ChatID, message.Text)
	case events.TypeApproval:
		var decision events.ApprovalDecision
		if err := json.Unmarshal(event.Payload, &decision); err != nil {
			return err
		}
		return a.handleApproval(ctx, decision)
	case events.TypeHeartbeat:
		return a.handleHeartbeat(ctx)
	default:
		return errors.New("unsupported event type")
	}
}

// destinationFromEvent derives a turn's destination from the triggering
// event: web populates events.Message.ChatID with the thread ID (see
// newThreadSendHandler); every other source (Telegram, schedules,
// heartbeats) maps to the fixed Telegram destination.
func destinationFromEvent(event events.Event, message events.Message) approvals.Destination {
	if event.Source == "web" {
		return approvals.Destination{Kind: approvals.DestinationWeb, ThreadID: message.ChatID}
	}
	return approvals.Destination{Kind: approvals.DestinationTelegram}
}

func decodeMessage(event events.Event, ownerID int64) (events.Message, error) {
	var message events.Message
	if err := json.Unmarshal(event.Payload, &message); err != nil {
		return events.Message{}, err
	}
	if message.ChatID == "" {
		message.ChatID = strconv.FormatInt(ownerID, 10)
	}
	return message, nil
}

func readOnlyRunOptions() agent.RunOptions {
	return agent.RunOptions{AllowedTools: map[string]bool{
		"status": true, "repository_list": true, "calendar_list": true,
		"read_file": true, "terminal": true, "repository_github": true,
		"skill_read": true,
	}}
}

// heartbeatRunOptions extends readOnlyRunOptions with the narrow memory-
// curation tools so a heartbeat turn can proactively write stable facts to
// USER.md/MEMORY.md, mirroring Hermes's periodic-nudge curation without
// adding a separate subsystem: it is the same explicit, guarded tool call a
// direct conversation turn can already make.
func heartbeatRunOptions() agent.RunOptions {
	options := readOnlyRunOptions()
	for _, tool := range []string{
		"user_append", "user_replace_section", "user_remove_section", "user_read",
		"memory_append", "memory_replace_section", "memory_remove_section", "memory_read",
		"skill_disable", "skill_enable",
	} {
		options.AllowedTools[tool] = true
	}
	return options
}

type messageHandlingPolicy struct {
	includeRecentHistory bool
	recordConversation   bool
	source               string
}

func (a *App) handleMessage(ctx context.Context, message events.Message, options agent.RunOptions, policy messageHandlingPolicy) error {
	if output, handled, err := a.commands.Execute(ctx, message.Text); handled {
		if err != nil {
			return err
		}
		return a.channel.Deliver(ctx, message.ChatID, output)
	}
	agentContext, err := a.context.Load(ctx)
	if err != nil {
		return err
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return err
	}
	alias, err := a.agentRuntime.SelectedModel(ctx)
	if err != nil {
		return err
	}
	effort, err := a.agentRuntime.ReasoningEffort(ctx)
	if err != nil {
		return err
	}
	enabledSkills, err := a.skillsService.Enabled(ctx)
	if err != nil {
		return err
	}
	manifest := a.capabilityManifest(state, alias, enabledSkills)
	manifest.Tools = a.loop.ToolNames(options)
	history := agent.BuildInstructions(agentContext, manifest, agent.TemporalContext{Now: a.now().In(a.location), Timezone: a.timezone})
	destination := approvals.DestinationFromContext(ctx)
	if policy.includeRecentHistory {
		recent, err := a.conversation.RecentMessages(ctx, destination.ConversationID())
		if err != nil {
			a.logger.Error("recent conversation window unavailable", "conversation_id", destination.ConversationID(), "error", err)
		} else {
			history = append(history, recent...)
		}
	}
	finishToolProgress := func() {}
	if policy.recordConversation {
		options.OnToolCall, finishToolProgress = a.toolCallProgress(ctx, message.ChatID)
	}
	stopTyping := channelutil.StartTyping(ctx, a.channel, message.ChatID, 4*time.Second)
	result, runErr := a.loop.RunSelected(ctx, alias, effort, message.Text, history, options)
	stopTyping()
	finishToolProgress()
	usageErr := a.agentRuntime.RecordUsage(ctx, alias, result.Usage)
	if errors.Is(runErr, agent.ErrToolStepLimit) {
		if usageErr != nil {
			return usageErr
		}
		return a.channel.Deliver(ctx, message.ChatID, "I ran out of tool-call steps working on that before I could finish. Try a narrower request, or ask me to continue.")
	}
	if runErr != nil {
		return runErr
	}
	if usageErr != nil {
		return usageErr
	}
	if policy.recordConversation {
		conversationID := destination.ConversationID()
		if err := a.conversation.Record(ctx, conversationID, ports.Message{Role: ports.RoleUser, Content: message.Text}, policy.source); err != nil {
			return err
		}
		if err := a.conversation.Record(ctx, conversationID, result.Message, policy.source); err != nil {
			return err
		}
		if destination.Kind == approvals.DestinationWeb {
			if err := a.memory.SetThreadTitle(ctx, destination.ThreadID, truncateThreadTitle(message.Text)); err != nil {
				a.logger.Error("thread auto-titling failed", "thread_id", destination.ThreadID, "error", err)
			}
		}
	}
	if strings.TrimSpace(result.ReasoningContent) != "" {
		showThinking, err := a.agentRuntime.ShowThinking(ctx)
		if err != nil {
			return err
		}
		if showThinking {
			if err := a.channel.Deliver(ctx, message.ChatID, "Thinking:\n"+result.ReasoningContent); err != nil {
				return err
			}
		}
	}
	return a.channel.Deliver(ctx, message.ChatID, result.Message.Content)
}

// toolCallProgress returns an agent.RunOptions.OnToolCall callback and a
// matching finish function that surface a live "Calling <tool>..."
// indicator on the channel ctx resolves to, editing one message in place as
// more tools are called during the turn -- the same DeliverTrackable/
// EditText mechanism a coding run's progress already uses, reused here so
// an ordinary tool call (e.g. current_time) is visible mid-turn too, not
// folded silently into the final reply. finish is always safe to call, a
// no-op if no tool was ever called.
func (a *App) toolCallProgress(ctx context.Context, chatID string) (onToolCall func(string), finish func()) {
	var messageID string
	var calls []string
	render := func(text string) {
		if messageID != "" && a.channel.EditText(ctx, chatID, messageID, text) == nil {
			return
		}
		if id, err := a.channel.DeliverTrackable(ctx, chatID, text); err == nil {
			messageID = id
		}
	}
	onToolCall = func(name string) {
		calls = append(calls, name)
		render("Calling " + strings.Join(calls, ", ") + "...")
	}
	finish = func() {
		if len(calls) == 0 {
			return
		}
		render("Called " + strings.Join(calls, ", ") + ".")
	}
	return onToolCall, finish
}

// truncateThreadTitle cheaply derives a web thread's auto-title from its
// first user message: no separate model call for v1 (see the design spec's
// Auto-titling section).
func truncateThreadTitle(text string) string {
	const maxRunes = 60
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "…"
}

func (a *App) handleApproval(ctx context.Context, decision events.ApprovalDecision) error {
	preState, err := a.store.Load(ctx)
	if err != nil {
		return err
	}
	ctx = approvals.WithDestination(ctx, preState.Approvals[decision.ApprovalID].Destination)
	chatID := strconv.FormatInt(a.config.Telegram.OwnerID, 10)
	if decision.CallbackQueryID != "" {
		_ = a.channel.AnswerCallback(ctx, decision.CallbackQueryID)
	}
	if err := a.approvals.Decide(ctx, decision.ApprovalID, decision.Approved); err != nil {
		return a.deliverApprovalFailure(ctx, chatID, decision.MessageID, err)
	}
	if !decision.Approved {
		return channelutil.DeliverOutcome(ctx, a.channel, chatID, decision.MessageID, "Action rejected.")
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return a.deliverApprovalFailure(ctx, chatID, decision.MessageID, err)
	}
	approval := state.Approvals[decision.ApprovalID]
	executor, ok := a.approvalExecutors[approval.Action]
	if !ok {
		return a.deliverApprovalFailure(ctx, chatID, decision.MessageID, errors.New("unknown approval action"))
	}
	result, err := executor.ExecuteApproved(ctx, approval)
	if err != nil {
		return a.deliverApprovalFailure(ctx, chatID, decision.MessageID, err)
	}
	return channelutil.DeliverOutcome(ctx, a.channel, chatID, decision.MessageID, fmt.Sprintf("Approved action completed: %v", result))
}

// deliverApprovalFailure tells the owner an approve/reject tap didn't go
// through, instead of leaving execErr to only reach the server log (see
// Run's event-loop goroutine, which logs and otherwise discards any
// HandleEvent error). Without this, a tap that produces no visible outcome
// at all is indistinguishable from a broken button, and the owner has no
// way to learn what actually failed -- the Telegram/web message is the only
// channel back to them. Still returns execErr so the failure remains
// logged server-side exactly as before.
func (a *App) deliverApprovalFailure(ctx context.Context, chatID, messageID string, execErr error) error {
	if deliverErr := channelutil.DeliverOutcome(ctx, a.channel, chatID, messageID, fmt.Sprintf("Action failed: %v", execErr)); deliverErr != nil {
		return errors.Join(execErr, deliverErr)
	}
	return execErr
}

func (a *App) Run(ctx context.Context) error {
	if a.memory != nil {
		defer a.memory.Close()
	}
	defer a.workers.Wait()
	if a.mcp != nil {
		defer a.mcp.Close()
	}
	if _, err := a.coding.RecoverInterrupted(ctx); err != nil {
		return err
	}
	if err := a.scheduler.Recover(ctx); err != nil {
		return err
	}
	if err := a.invalidateStaleShippingApprovals(ctx); err != nil {
		return err
	}
	if a.memoryWorker != nil {
		a.workers.Add(1)
		go func() {
			defer a.workers.Done()
			a.runMemoryEmbeddingWorker(ctx)
		}()
	}
	scheduleTicker := time.NewTicker(time.Minute)
	defer scheduleTicker.Stop()
	heartbeatCadence := a.config.Scheduler.HeartbeatCadence.Value()
	if heartbeatCadence <= 0 {
		heartbeatCadence = 30 * time.Minute
	}
	heartbeatTicker := time.NewTicker(heartbeatCadence)
	defer heartbeatTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-a.eventQueue:
			a.workers.Add(1)
			go func() {
				defer a.workers.Done()
				if err := a.HandleEvent(ctx, event); err != nil {
					slog.Error("event failed", "event_id", event.ID, "correlation_id", event.CorrelationID, "error", err)
				}
			}()
		case now := <-scheduleTicker.C:
			if err := a.coding.CleanupExpired(ctx, now.Add(-a.config.Runner.Retention.Value())); err != nil {
				return err
			}
			due, err := a.scheduler.Due(ctx, now)
			if err != nil {
				return err
			}
			for _, schedule := range due {
				schedule := schedule
				// A ScheduleExecutionMessage schedule is a deterministic,
				// pre-rendered notification (reminder or watchdog): it is
				// delivered verbatim on TypeScheduledMessage with no model
				// call. Everything else starts a self-contained,
				// no-ambient-history agent turn on TypeSchedule.
				eventType := events.TypeSchedule
				if schedule.Execution == ports.ScheduleExecutionMessage {
					eventType = events.TypeScheduledMessage
				}
				payload, _ := json.Marshal(events.Message{ChatID: strconv.FormatInt(a.config.Telegram.OwnerID, 10), Text: schedule.Instruction})
				event := events.Event{ID: "schedule:" + schedule.ID + ":" + schedule.PendingRun.Format(time.RFC3339Nano), Type: eventType, Owner: strconv.FormatInt(a.config.Telegram.OwnerID, 10), Timestamp: now, Payload: payload}
				a.workers.Add(1)
				go func() {
					defer a.workers.Done()
					if err := a.HandleEvent(ctx, event); err != nil {
						if failErr := a.scheduler.Fail(ctx, schedule.ID, schedule.PendingRun); failErr != nil {
							slog.Error("schedule failure acknowledgement failed", "schedule_id", schedule.ID, "error", failErr)
						}
						slog.Error("scheduled event failed", "schedule_id", schedule.ID, "error", err)
						return
					}
					if err := a.scheduler.Complete(ctx, schedule.ID, schedule.PendingRun, a.now()); err != nil {
						slog.Error("schedule completion acknowledgement failed", "schedule_id", schedule.ID, "error", err)
					}
				}()
			}
		case now := <-heartbeatTicker.C:
			_ = a.HandleEvent(ctx, events.Event{ID: "heartbeat:" + now.Format(time.RFC3339Nano), Type: events.TypeHeartbeat, Owner: strconv.FormatInt(a.config.Telegram.OwnerID, 10), Timestamp: now, Payload: json.RawMessage(`{}`)})
		}
	}
}

func (a *App) runMemoryEmbeddingWorker(ctx context.Context) {
	interval := a.memoryEmbeddingInterval
	if interval <= 0 {
		interval = time.Minute
	}
	for {
		err := a.memoryWorker.Run(ctx, interval)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			a.logger.Error("memory embedding worker failed", "error", err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

// hasActiveProtectedWork reports whether an implementation run is currently
// executing. A heartbeat tick is skipped entirely while one is active rather
// than interleaving a curation/check-in turn with it.
func (a *App) hasActiveProtectedWork(ctx context.Context) (bool, error) {
	sessions, err := a.coding.List(ctx)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if session.Phase == ports.PhaseRunning {
			return true, nil
		}
	}
	return false, nil
}

// handleHeartbeat runs a small, self-contained heartbeat turn: no ambient
// recent-conversation history, so instructions from an old chat cannot be
// silently revived. Its context is the durable docs (SOUL/USER/MEMORY), the
// owner-editable HEARTBEAT.md checklist, and the capability manifest — never
// state.RecentMessages.
//
// Silent context curation (USER.md/MEMORY.md) is never gated by quiet hours
// or the weekly proactive-message limit; only the owner-facing Telegram
// check-in is. HeartbeatPolicy.CanSend governs sending the check-in and
// recording it against the weekly limit, not whether the turn runs at all.
func (a *App) handleHeartbeat(ctx context.Context) error {
	if active, err := a.hasActiveProtectedWork(ctx); err != nil {
		return err
	} else if active {
		return nil
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return err
	}
	sendAllowed := a.heartbeat.CanSend(state, a.now())
	agentContext, err := a.context.Load(ctx)
	if err != nil {
		return err
	}
	alias, err := a.agentRuntime.SelectedModel(ctx)
	if err != nil {
		return err
	}
	effort, err := a.agentRuntime.ReasoningEffort(ctx)
	if err != nil {
		return err
	}
	enabledSkills, err := a.skillsService.Enabled(ctx)
	if err != nil {
		return err
	}
	manifest := a.capabilityManifest(state, alias, enabledSkills)
	options := heartbeatRunOptions()
	manifest.Tools = a.loop.ToolNames(options)
	history := agent.BuildInstructions(agentContext, manifest, agent.TemporalContext{Now: a.now().In(a.location), Timezone: a.timezone})
	history = append(history, agent.HeartbeatChecklistMessage(agentContext.Heartbeat))
	history = append(history, ports.Message{Role: ports.RoleSystem, Content: "Heartbeat context only: an isolated turn with no recent-conversation history. Protected writes are forbidden."})
	instruction := "Separately, review durable context for any stable fact, preference, or decision worth curating into USER.md or MEMORY.md: use the read tool to see the current document first, append or replace a section for new or changed facts, and remove a section outright once it is stale, superseded, or duplicated. Curation does not require sending a check-in."
	if sendAllowed {
		instruction = "Evaluate whether one concise proactive check-in is useful now, using the HEARTBEAT.md checklist as a starting point. " + instruction + fmt.Sprintf(" Reply with exactly %q and nothing else when no check-in is useful.", services.HeartbeatNoReportSentinel)
	} else {
		instruction = "A proactive check-in cannot be sent right now (quiet hours or the proactive-message limit). Do not attempt one. " + instruction + fmt.Sprintf(" Reply with exactly %q.", services.HeartbeatNoReportSentinel)
	}
	result, runErr := a.loop.RunSelected(ctx, alias, effort, instruction, history, options)
	usageErr := a.agentRuntime.RecordUsage(ctx, alias, result.Usage)
	if runErr != nil {
		return runErr
	}
	if usageErr != nil {
		return usageErr
	}
	if !sendAllowed || services.HeartbeatHasNothingToReport(result.Message.Content) {
		return nil
	}
	if err := a.heartbeat.Record(ctx, a.store, a.now()); err != nil {
		return err
	}
	ownerChatID := strconv.FormatInt(a.config.Telegram.OwnerID, 10)
	return a.channel.Deliver(ctx, ownerChatID, result.Message.Content)
}

// invalidateStaleShippingApprovals discards any pending Commit/Push/CreatePR
// approval found at startup. ShippingService.Ship issues, decides, and
// authorizes that whole chain itself in one call now, so a pending shipping
// approval can only be a leftover from before that change -- no code path
// still creates one and waits for a human Decide call.
func (a *App) invalidateStaleShippingApprovals(ctx context.Context) error {
	state, err := a.store.Load(ctx)
	if err != nil {
		return err
	}
	for id, approval := range state.Approvals {
		if approval.Status != approvals.Pending {
			continue
		}
		if approval.Action != approvals.Commit && approval.Action != approvals.Push && approval.Action != approvals.CreatePR {
			continue
		}
		if err := a.approvals.Invalidate(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func newRunID() string {
	data := make([]byte, 6)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}

func repositoryNamesFromState(state ports.State) []string {
	names := make([]string, 0, len(state.Repositories))
	for name := range state.Repositories {
		names = append(names, name)
	}
	return names
}

func (a *App) capabilityManifest(state ports.State, activeModel string, skills []ports.SkillSummary) agent.CapabilityManifest {
	manifest := a.manifest
	manifest.ActiveModel = activeModel
	manifest.Repositories = repositoryNamesFromState(state)
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
