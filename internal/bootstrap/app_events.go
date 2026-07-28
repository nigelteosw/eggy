package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

// This file is App's runtime behavior once NewApp has wired it: the daemon
// loop (Run), event dispatch (HandleEvent/Enqueue/processEvent), and the
// background workers. What happens *inside* a turn -- the tool allowlists,
// the context it is built from, its transcript, and the owner/unprompted
// distinction -- is internal/kernel/turns, which this file only routes into.
// See app.go for construction, and turn_presenter.go for the surface-side
// rendering that package asks for.

func (a *App) HandleEvent(ctx context.Context, event events.Event) error {
	return a.dispatcher.Handle(ctx, event)
}

// Enqueue hands an event to the loop without blocking: a web request or a
// webhook must not park waiting for queue space.
//
// ctx is checked explicitly rather than as a third select case. With default
// present, a `case <-ctx.Done()` can never fire -- select takes default the
// moment no other case is ready -- so a cancelled context reported "event
// queue is full", which is both wrong and misleading when debugging.
func (a *App) Enqueue(ctx context.Context, event events.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case a.eventQueue <- event:
		return nil
	default:
		return errors.New("event queue is full")
	}
}

func (a *App) processEvent(ctx context.Context, event events.Event) error {
	switch event.Type {
	case events.TypeMessage:
		message, err := decodeMessage(event)
		if err != nil {
			return err
		}
		source := strings.TrimSpace(event.Source)
		if source == "" {
			source = "telegram"
		}
		return a.turnService.OwnerMessage(destination.With(ctx, event.Destination), message.Text, source)
	case events.TypeSchedule:
		message, err := decodeMessage(event)
		if err != nil {
			return err
		}
		return a.turnService.ScheduledTurn(destination.With(ctx, proactiveDestination()), message.Text)
	case events.TypeScheduledMessage:
		// A deterministic, pre-rendered notification (a reminder or
		// watchdog-style check-in): delivered verbatim with no model call at
		// all, as distinct from TypeSchedule above.
		message, err := decodeMessage(event)
		if err != nil {
			return err
		}
		return a.channel.Deliver(destination.With(ctx, proactiveDestination()), message.Text)
	case events.TypeChecksCompleted:
		var completed events.ChecksCompleted
		if err := json.Unmarshal(event.Payload, &completed); err != nil {
			return err
		}
		return a.turnService.ChecksTurn(destination.With(ctx, event.Destination), completed.Instruction)
	case events.TypeApproval:
		var decision events.ApprovalDecision
		if err := json.Unmarshal(event.Payload, &decision); err != nil {
			return err
		}
		return a.turnService.Approval(ctx, decision)
	case events.TypeHeartbeat:
		return a.turnService.Heartbeat(destination.With(ctx, proactiveDestination()))
	default:
		return errors.New("unsupported event type")
	}
}

func decodeMessage(event events.Event) (events.Message, error) {
	var message events.Message
	if err := json.Unmarshal(event.Payload, &message); err != nil {
		return events.Message{}, err
	}
	return message, nil
}

// proactiveDestination is where every unprompted turn -- heartbeat,
// scheduled agent turn, scheduled message -- reports. Unprompted output is
// Telegram's, deliberately and for now: the web UI is a pull surface the
// owner opens, not one Eggy pushes to, and one proactive channel keeps the
// quiet-hours and weekly-limit accounting in HeartbeatPolicy meaningful
// rather than per-channel.
//
// This is a decision, not a default. Every proactive path stamps it on ctx
// explicitly instead of relying on destination.FromContext's Telegram
// fallback, so making the surface configurable later means changing this
// one function rather than finding the paths that silently fell through.
func proactiveDestination() destination.Destination {
	return destination.Destination{Kind: destination.Telegram}
}

func (a *App) Run(ctx context.Context) error {
	if a.memory != nil {
		defer a.memory.Close()
	}
	defer a.workers.Wait()
	if a.mcp != nil {
		defer a.mcp.Close()
	}
	if _, err := a.changes.MarkInterrupted(ctx); err != nil {
		return err
	}
	// Thread-attached checkouts are durable, so a restart inherits whatever
	// the last process left on the volume. Reconcile before serving a turn:
	// a binding whose directory is gone must not resolve.
	if a.workspaces != nil {
		if _, err := a.workspaces.Recover(ctx); err != nil {
			return err
		}
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
	// Every tick is a model call whether or not it produces a check-in, since
	// silent USER.md/MEMORY.md curation runs regardless of quiet hours. Three
	// hours is the cadence that keeps that cost proportionate; the weekly
	// proactive limit, not this, is what bounds how often the owner hears
	// from a heartbeat.
	heartbeatCadence := a.config.Scheduler.HeartbeatCadence.Value()
	if heartbeatCadence <= 0 {
		heartbeatCadence = 3 * time.Hour
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
			cutoff := now.Add(-a.config.Runner.Retention.Value())
			// A checkout belongs to its thread, so there is exactly one
			// reaper for it: the change that branched it never owned it and
			// has nothing to release.
			if a.workspaces != nil {
				if _, err := a.workspaces.CleanupIdle(ctx, cutoff); err != nil {
					return err
				}
			}
			// Poll the pull requests Eggy has open for finished checks. A
			// failed suite is enqueued as an ordinary turn against the thread
			// that proposed it; nothing is enqueued for a green or
			// still-running one. This is a poll rather than a webhook
			// deliberately: it needs no new inbound surface, and it reads
			// through the same GitHub read path repository_github uses.
			a.enqueueFailedChecks(ctx, now)
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
				payload, _ := json.Marshal(events.Message{Text: schedule.Instruction})
				event := events.Event{ID: "schedule:" + schedule.ID + ":" + schedule.PendingRun.Format(time.RFC3339Nano), Type: eventType, Owner: a.config.Owner.ID, Timestamp: now, Destination: proactiveDestination(), Payload: payload}
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
			_ = a.HandleEvent(ctx, events.Event{ID: "heartbeat:" + now.Format(time.RFC3339Nano), Type: events.TypeHeartbeat, Owner: a.config.Owner.ID, Timestamp: now, Destination: proactiveDestination(), Payload: json.RawMessage(`{}`)})
		}
	}
}

// enqueueFailedChecks turns each newly failed pull-request check suite into
// a TypeChecksCompleted event for the thread that proposed the change. A
// polling failure is logged rather than returned: GitHub being briefly
// unreachable must not stop the scheduler tick that also reaps workspaces
// and fires schedules.
func (a *App) enqueueFailedChecks(ctx context.Context, now time.Time) {
	if a.checks == nil {
		return
	}
	completions, err := a.checks.Poll(ctx)
	if err != nil {
		a.logger.Error("pull-request checks poll failed", "error", err)
	}
	for _, completion := range completions {
		payload, err := json.Marshal(events.ChecksCompleted{
			Change: completion.Change, Repository: completion.Repository,
			PullRequestNumber: completion.PullRequestNumber, Ref: completion.Ref,
			Conclusion: completion.Conclusion, Instruction: completion.ChecksInstruction(),
		})
		if err != nil {
			a.logger.Error("checks event could not be encoded", "session_id", completion.Change, "error", err)
			continue
		}
		event := events.Event{
			ID: completion.ChecksEventID(), Type: events.TypeChecksCompleted, Owner: a.config.Owner.ID,
			Timestamp: now, Destination: completion.Destination, Payload: payload,
		}
		if err := a.Enqueue(ctx, event); err != nil {
			a.logger.Error("checks event could not be enqueued", "session_id", completion.Change, "error", err)
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
