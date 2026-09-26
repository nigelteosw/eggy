package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nigelteosw/eggy/internal/core/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

func (a *App) Run(ctx context.Context) error {
	if a.database != nil {
		defer a.database.Close()
	}
	defer a.workers.Wait()
	if a.mcp != nil {
		defer a.mcp.Close()
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
	// The gateway opens after recovery. On the way out it closes before the
	// deferred workers.Wait drains in-flight turns: intake stops first, and
	// replies still go out, because sends are REST calls that need no
	// gateway. Close is idempotent, so a restart closes the old transport
	// exactly once.
	if err := a.discord.open(ctx, a.logger); err != nil {
		return fmt.Errorf("open discord gateway: %w", err)
	}
	defer a.discord.close(a.logger)
	scheduleTicker := time.NewTicker(time.Minute)
	defer scheduleTicker.Stop()
	heartbeat := a.heartbeatTicks()
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-a.restart:
			// Deliberately not a cancellation: ctx stays live so the deferred
			// workers.Wait drains the turn that asked for the restart -- and
			// any other turn mid-flight -- instead of cutting it off. Only
			// once they are done do the stores and MCP clients close.
			slog.Info("restart requested, draining in-flight turns")
			return ErrRestart
		case <-heartbeat.C():
			// A dispatched beat re-arms the clock when it finishes, so the
			// next gap starts from the end of the beat rather than from a
			// fixed phase the beat's own duration eats into. A skipped tick
			// has nothing to wait for.
			if !a.onHeartbeatTick(ctx) {
				heartbeat.Reset(a.nextHeartbeatWake(0))
			}
		case requested := <-a.heartbeatWake:
			wake := a.nextHeartbeatWake(requested)
			// The one record that a beat happened and what it decided.
			// Without it a self-paced heartbeat is unobservable: a quiet beat
			// writes nothing anywhere, so nobody can tell good pacing from a
			// stopped clock.
			slog.Info("heartbeat paced", "requested", requested, "next", wake)
			heartbeat.Reset(wake)
		case event := <-a.eventQueue:
			a.workers.Go(func() {
				if err := a.HandleEvent(ctx, event); err != nil {
					slog.Error("event failed", "event_id", event.ID, "correlation_id", event.CorrelationID, "error", err)
				}
			})
		case now := <-scheduleTicker.C:
			if err := a.onScheduleTick(ctx, now); err != nil {
				return err
			}
		}
	}
}

// onScheduleTick is what one minute does: reap idle checkouts, then claim
// and dispatch every due schedule as the account that owns it.
func (a *App) onScheduleTick(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-a.config.Runner.Retention.Value())
	// A checkout belongs to its thread, so there is exactly one
	// reaper for it: the change that branched it never owned it and
	// has nothing to release.
	if a.workspaces != nil {
		if _, err := a.workspaces.CleanupIdle(ctx, cutoff); err != nil {
			return err
		}
	}
	due, err := a.scheduler.Due(ctx, now)
	if err != nil {
		return err
	}
	for _, schedule := range due {
		ownerCtx := ports.WithPrincipal(ctx, ports.Principal{AccountID: schedule.Owner})
		// A schedule whose account has been removed does not run, and is
		// switched off rather than failed: failing it would only claim it
		// again next minute, for a person who is no longer here to
		// receive it. The record stays, disabled, with their other data.
		if _, ok := a.config.Account(schedule.Owner); !ok {
			slog.Warn("disabling schedule for a removed account", "schedule_id", schedule.ID, "account", schedule.Owner)
			if err := a.scheduler.Disable(ownerCtx, schedule.ID); err != nil {
				slog.Error("could not disable schedule", "schedule_id", schedule.ID, "error", err)
			}
			continue
		}
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
		// The owner is the stored schedule's, never the instruction's
		// and never a configured default: the dispatcher validates it
		// and the completion below acts as it.
		event := events.Event{ID: "schedule:" + schedule.ID + ":" + schedule.PendingRun.Format(time.RFC3339Nano), Type: eventType, Owner: schedule.Owner, Timestamp: now, Destination: proactiveDestination(), Payload: payload}
		a.workers.Go(func() {
			if err := a.HandleEvent(ctx, event); err != nil {
				if failErr := a.scheduler.Fail(ownerCtx, schedule.ID, schedule.PendingRun); failErr != nil {
					slog.Error("schedule failure acknowledgement failed", "schedule_id", schedule.ID, "error", failErr)
				}
				slog.Error("scheduled event failed", "schedule_id", schedule.ID, "error", err)
				return
			}
			if err := a.scheduler.Complete(ownerCtx, schedule.ID, schedule.PendingRun, a.now()); err != nil {
				slog.Error("schedule completion acknowledgement failed", "schedule_id", schedule.ID, "error", err)
			}
		})
	}
	return nil
}
