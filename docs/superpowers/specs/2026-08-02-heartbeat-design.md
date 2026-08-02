# Heartbeat: a proactive check-in that is allowed to say nothing

Date: 2026-08-02
Status: implemented 2026-08-02, as designed. One departure: the ticker
construction and the tick action are named functions (`heartbeatTicks`,
`onHeartbeatTick`) rather than an inline `case` body, so each half is
separately testable and a second thing a tick could trigger later — an
external workflow runner, say — is added beside the turn rather than by
growing the daemon loop.

## Problem

Eggy is purely reactive. It speaks when the owner speaks, when a schedule the
owner created fires, or when an approval resolves. There is no path by which it
notices something and says so unprompted.

`schedule_recurring` looks like it should cover this, and does not. A scheduled
turn always delivers `result.Message.Content` (`internal/kernel/turns/turns.go`,
final line of `run`). A 30-minute recurring check-in therefore messages the
owner 48 times a day, 46 of them to say nothing is wrong. **The missing
capability is not timing — Eggy has timing. It is permission to stay silent.**

## Prior art

Both reference agents split proactive behaviour three ways, and neither makes
"reminder" one of the three.

**OpenClaw** has automations (cron), heartbeat, and hooks. Automations are
precise timing with isolated execution — "an exact alarm clock". Heartbeat is
approximate timing with full session context — "a security guard on patrol";
their framing is that cron starts work and the heartbeat decides whether to
continue, end, or change course. Reminders are one-shot automations (`--at`),
not a fourth mechanism. The heartbeat prompt comes from configuration
(`agents.defaults.heartbeat.prompt`); a workspace `HEARTBEAT.md` is **legacy**,
migrated into a database-backed monitor scratch by `openclaw doctor --fix`.
`HEARTBEAT_OK` is treated as an acknowledgement when it leads or trails the
reply: the token is stripped and the reply dropped if what remains is at most
300 characters.

**Hermes** has cron and webhooks. Cron stores jobs as JSON and a background
thread checks for due jobs every 60 seconds — the same design as Eggy's
`plugins/scheduler/cronfile` plus the ticker in `internal/bootstrap`, down to
the interval. Its suppression marker is a generic `[SILENT]`, not
heartbeat-specific. Script-only (no-LLM) cron jobs are the analogue of Eggy's
`kind: reminder`, and are a variant of cron rather than a separate system.
Cron is explicitly time-based only; event-driven execution is the open gap
(issue #491).

Two conclusions carried into this design:

1. The heartbeat prompt belongs in configuration, not a Markdown file. Both
   references store it with the job or in config, and OpenClaw actively
   migrated away from the file.
2. Reminders are not a peer mechanism. Eggy already distinguishes them the way
   that matters — `kind: agent | reminder` on the same two tools, differing in
   whether a model runs at all. Promoting them further would create a second
   way to schedule something at a time, which is the defect TODO.md exists to
   find.

## Non-goals

- **Event-driven triggers.** OpenClaw's hooks and Hermes' webhooks are a real
  third leg and Eggy has nothing for it. Out of scope here; it needs an event
  source port, a subscription store, and per-source authentication.
- **OpenClaw's full-session-context heartbeat.** Eggy's unprompted turns are
  isolated on purpose, and that isolation is a standing safety constraint.
  See "Isolation" below.
- **Quiet hours.** Deliberately deferred; see "Deliberate omissions".
- **Rate limiting.** The deleted `HeartbeatPolicy` had a weekly cap and a
  minimum interval over `state.ProactiveMessages`. Both become
  unpredictable-silence bugs once the interval is explicit and silence is
  already the default behaviour.
- **A `HEARTBEAT.md` document.** Ruled out by the prior art above.
- **Pruning `state.ProcessedEvents`.** A real pre-existing leak found while
  designing this (see "Why not the dispatcher"), tracked separately.

## History

Eggy had a heartbeat. Commit `7ae6835` ("Simplify to a configurable core")
deleted `internal/kernel/services/heartbeat.go`,
`internal/bootstrap/heartbeat_isolation_test.go`, and the `scheduler:` config
section. `internal/config/config_init.go` still lists `{"scheduler"}` among
`retiredConfigFields`, annotated "heartbeat and proactive messaging".

This design is a narrower re-introduction: ~75 production lines against the
~300 that were removed, because the deleted version carried its own policy
object, its own durable state, and its own background loop, none of which
survive here. The new section is named `heartbeat:`, not `scheduler:`, so the
retired-field prune is unaffected and existing deployed configs stay loadable.

## Design

Three capabilities, of which two already exist:

| | trigger | model call | speaks |
|---|---|---|---|
| Cron (`kind: agent`) | a time the owner named | yes | always |
| Reminder (`kind: reminder`) | a time the owner named | no, verbatim | always |
| **Heartbeat** | a fixed interval | yes | **only when warranted** |

The heartbeat is a separate mechanism, not an entry in the cron store. It has
its own interval, its own turn kind, and no schedule record.

### Isolation

`HeartbeatTurn` inherits both safety properties of `ScheduledTurn` by
construction rather than by re-deciding them:

- **`ReadOnlyTools()`** — the same allowlist. No mutation, and no MCP tool,
  since no unprompted allowlist names one.
- **No ambient conversation history** — `Policy.IncludeRecentHistory` stays
  false by omission, so an owner's earlier chat cannot silently steer a turn
  they are not present for and did not review at fire time.

OpenClaw's heartbeat deliberately runs with full session context. Eggy's does
not. This is the one place the design departs from the reference, and it is a
decision rather than an oversight.

### Why not a plugin

Considered and rejected. `plugins/<category>/<provider>` is for provider
adapters: something that implements a port, so that a second provider could
implement it too. The heartbeat implements no port, and there is no alternative
provider of "deciding whether to speak unprompted" to swap in.

The decisive reason is that isolation is the entire safety story here, and
`internal/kernel/turns` exists to hold exactly that. Its package comment records
why it was extracted from `internal/bootstrap`: that is core agentic behaviour
rather than wiring, and the safety-relevant part — which turns are unprompted,
and what those turns may reach — sat where no kernel test could guard it.
Putting `HeartbeatTurn` in a plugin would move `ReadOnlyTools()` and the
no-ambient-history rule back outside the kernel.

"The heartbeat is Telegram-only" is true but is not an argument for a Telegram
plugin, because it is already true of *all* unprompted output and is enforced
in one place. `proactiveDestination()` stamps `destination.Telegram` on every
unprompted path, and its comment states that this is a decision rather than a
default, so that making the surface configurable later is a change to that one
function. `TestUnpromptedTurnsAlwaysReportToTelegram` and the web-only case in
`routed_channel_test.go` already pin the behaviour. The heartbeat inherits it by
using `proactiveDestination()`; a Telegram-specific plugin would instead
hard-code a coupling that is currently deliberately soft, and a later Slack or
Discord channel would need the heartbeat rewritten rather than rerouted.

### Why not the dispatcher

`internal/kernel/services/dispatcher.go` writes every handled event's ID into
`state.ProcessedEvents`, and nothing in the tree ever prunes that map. Every
Telegram message and every cron fire already adds a permanent entry to
`state.json`, and every dispatch does a full `Load` plus `Update` of the
growing map.

A 30-minute heartbeat would add roughly 17,500 entries a year and become the
dominant contributor to a pre-existing leak. Dedup buys a heartbeat nothing in
return: a single in-process ticker cannot deliver the same tick twice.

So the ticker calls `HeartbeatTurn` directly in a worker goroutine, exactly as
the schedule branch beside it already does. This removes `events.TypeHeartbeat`,
its payload marshalling, and a `processEvent` case from the change.

### Silence protocol

Adopted from OpenClaw, because a strict equality check delivers
"HEARTBEAT_OK — all quiet!" to the owner's phone, and models reliably append a
pleasantry to a sentinel.

`HEARTBEAT_OK` is recognised when it leads or trails the reply. The token is
stripped and the message dropped when what remains is under a named threshold.
An empty reply is likewise silence. The leniency only applies once the model
has already declared nothing to report, so it cannot swallow a genuine short
alert.

The protocol lives in the system message, not in the configured instruction, so
an owner who overrides `heartbeat.instruction` cannot accidentally delete it.

## Changes

Four files. No new packages, ports, interfaces, event types, durable records,
or goroutines.

### 1. `internal/config/config.go`

```go
type HeartbeatConfig struct {
    Interval    Duration `yaml:"interval,omitempty"`
    Instruction string   `yaml:"instruction,omitempty"`
}
```

Reuses the existing `Duration` YAML type, as `Runner.Retention` does. Absent
section or zero interval means off. A built-in default instruction applies when
`Instruction` is empty, so the minimal configuration is:

```yaml
heartbeat:
  interval: 3h
```

Validation rejects a negative interval. ~12 lines, +2 config keys.

### 2. `internal/kernel/agent/prompt.go`

`HeartbeatTurnMessage()`, a sibling of the existing `ScheduledTurnMessage()`.
Carries the same read-only framing plus the `HEARTBEAT_OK` protocol. ~5 lines.

### 3. `internal/kernel/turns/turns.go`

```go
// Policy gains one field.
SuppressSilentReply bool

// HeartbeatTurn is a periodic check-in the owner is not present for. Its
// isolation is ScheduledTurn's, unchanged; the only difference is that it is
// allowed to conclude there is nothing worth saying.
func (s *Service) HeartbeatTurn(ctx context.Context, text string) error {
    return s.run(ctx, text, ReadOnlyTools(), Policy{
        Extra:               []ports.Message{agent.HeartbeatTurnMessage()},
        SuppressSilentReply: true,
    })
}
```

plus the suppression check at `run`'s final delivery and the sentinel helper.
~30 lines.

### 4. `internal/bootstrap/app_events.go`

```go
var heartbeat <-chan time.Time
if interval := a.config.Heartbeat.Interval.Value(); interval > 0 {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()
    heartbeat = ticker.C
}
```

A nil channel blocks forever in a `select`, so an unconfigured heartbeat costs
nothing at runtime: no goroutine, no ticker, no branch ever taken. This is what
satisfies the standing rule that an unconfigured capability is free.

The ticker additionally requires a configured Telegram channel. Unprompted
output is addressed to `proactiveDestination()`, and `newRoutedChannel` gives a
web-only deployment a *noop* Telegram deliberately, so that "no Telegram
configured" means "no unprompted output" rather than output redirected into a
web thread the owner never asked to be pushed to. Without this guard a web-only
deployment with `heartbeat.interval` set would wake every interval, run a full
model turn, and deliver the result into the noop — an unbounded standing token
cost that can never produce a visible message. Cron has the same shape today,
but a cron entry is owner-created and therefore self-inflicted; a heartbeat is
a background cost the owner does not see accruing.

Startup logs once at warn level when `heartbeat.interval` is set with no
Telegram channel configured, rather than failing: the rest of the deployment is
valid, and a silent no-op would be indistinguishable from a broken heartbeat.

One new `case` beside the existing `scheduleTicker.C`, guarded on
`a.turnService.Active()` — which already exists. That guard does two things for
one line: ticks cannot pile up when a heartbeat runs longer than its interval,
and a heartbeat never interrupts a live owner conversation.

Output goes to `proactiveDestination()`, the existing Telegram-only push
surface, alongside scheduled turns and scheduled messages.

`time.NewTicker` first fires one interval after start, so a restart produces no
boot-time heartbeat storm.

## Error handling

A failing heartbeat is logged through the existing `slog.Error` path in the
worker and is not retried: the next tick is the retry, and a heartbeat has no
durable claim to release. This is the one behavioural difference from a
scheduled turn, which calls `scheduler.Fail` to clear `PendingRun` — a
heartbeat has no schedule record, so there is nothing to unstick.

An empty configured instruction falls back to the built-in default rather than
running a turn with no input.

## Deletion budget

| | |
|---|---|
| production lines | +75 |
| config keys | +2 |
| tools | 0 |
| durable records | 0 |
| background loops | 0 |
| new files | 0 |
| ports changes | 0 |

The honest accounting: this adds and deletes nothing. The argument for it is
that what it prevents cannot be fixed by the code already present — a recurring
scheduled turn always speaks, so the silence protocol is the whole feature and
the rest is wiring that already exists.

## Testing

Test-first, mirroring the existing patterns in `turns_test.go`.

- `HEARTBEAT_OK`, `"HEARTBEAT_OK — all quiet"`, and `""` deliver nothing; a
  real finding is delivered.
- A heartbeat turn's allowlist names no MCP tool and no mutation tool — the
  same assertions as `TestNoUnpromptedAllowlistNamesAnMCPTool` and
  `TestUnpromptedTurnsRunWithARestrictedAllowlist`.
- A heartbeat turn carries no ambient conversation history.
- An unset interval runs no turn ever, and starts no ticker.
- A set interval with no Telegram channel configured starts no ticker and runs
  no turn, so a web-only deployment carries no standing model cost.
- A config with no `heartbeat:` section still loads under `KnownFields(true)`.
- A heartbeat tick is skipped while a turn is already active.

## Deliberate omissions

**Quiet hours.** An interval-only heartbeat can fire at 03:00. Accepted for v1:
it is silent unless something warrants speaking, and something that warrants
speaking at 03:00 is arguably worth hearing. Roughly 15 lines to add later if
it proves annoying; muting the Telegram chat is the interim answer.

**A cron expression instead of an interval.** Considered — `0 9-22 * * *` would
encode waking hours for free using the parser that already exists. Rejected
because the heartbeat is deliberately not a cron entry, and giving it cron
syntax invites the assumption that it is one.

## Deploy note

`internal/config` parses with `KnownFields(true)`. This change only adds an
optional section, so existing `/data/config.yaml` files stay loadable and no
migration is required.
