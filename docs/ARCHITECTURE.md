# Eggy architecture

Eggy is a single-user personal agent that runs continuously on Railway and is
reachable through Telegram, with a companion `eggy` CLI reading the same
files. It is a Go ports-and-adapters modular monolith with file-backed state,
supporting exactly one owner and one `eggyd` replica.

For operator-facing setup and commands, see [`README.md`](../README.md).

## Architectural style

The kernel (`internal/kernel`, `internal/ports`) contains domain types and
use-case orchestration only. It does not import Telegram, model-provider,
GitHub, Google, YAML, JSON-file persistence, Docker, or Railway packages.
Provider implementations sit behind small interfaces owned by the kernel;
`internal/bootstrap` is the only package that constructs adapters, validates
capabilities, and wires them into kernel services. Adding a provider (model
backend, chat channel, repository host, calendar backend) means adding a
package under `plugins/<category>/<provider>/` plus wiring in
bootstrap — never a kernel or port change. See `AGENTS.md` for the concrete
steps.

Bootstrap is the composition root and nothing more. Three sibling packages own
what it would otherwise accumulate: `internal/config` parses, defaults, and
mutates `config.yaml`; `internal/commands` holds the command catalog shared by
Telegram, the CLI, and the web API; `internal/web` serves the embedded UI and
its JSON routes. They depend one way only — `config` <- `commands` <- `web` <-
`bootstrap` — which Go's acyclic import rule enforces on its own, since
bootstrap imports all three.

What happens *inside* a turn is not bootstrap's either: `internal/kernel/turns`
owns the kinds of turn (owner message, checks resumption, scheduled,
heartbeat), the tool allowlist each runs with, the per-turn transcript, and the
owner/unprompted distinction the safety invariants key off. Bootstrap routes an
event type to an entry point on it and nothing more, which is what lets a
kernel test guard those invariants.

Each of these packages declares the narrow interfaces it needs rather than
taking a whole service — `commands.ChangeLister` is one method where
`services.Changes` has eleven, `web.ThreadDirectory` is three where
`ports.ThreadStore` has eight, and `turns.Runtime` takes `RecordUsage` while
`commands.AgentSettings` deliberately does not. The concrete services satisfy
them structurally, so this costs the kernel nothing and keeps each surface from
reaching past what it needs.

```mermaid
flowchart TB
    subgraph entry[Entry points]
        owner[Single owner]
        telegram[Telegram webhook]
        cli[eggy CLI]
        clock[Schedule and heartbeat ticks]

        owner --> telegram
        owner --> cli
    end

    subgraph composition[Composition root - internal/bootstrap]
        daemon[eggyd App and event dispatcher]
        registry[Native and discovered tool wiring<br/>and capability manifest]
    end

    subgraph surfaces[Config, commands, and HTTP]
        settings[internal/config<br/>parsing, defaults, mutation]
        commands[internal/commands<br/>deterministic command catalog]
        httpapi[internal/web<br/>web UI, JSON API, chat routes]
    end

    telegram --> daemon
    clock --> daemon
    cli --> commands
    daemon -->|slash command| commands

    subgraph kernel[Provider-neutral kernel - internal/kernel and internal/ports]
        turnservice[internal/kernel/turns<br/>turn kinds, tool allowlists, transcripts]
        outer[The agent loop]
        services[Domain services<br/>context, scheduling, Calendar, repositories]
        coding[Session bookkeeping and shipping services]
        ports[Small provider-neutral ports]

        turnservice --> outer
        outer --> services
        outer -->|workspace_edit, propose_change| coding
        services --> ports
        coding --> ports
    end

    daemon -->|owner message| turnservice
    daemon -->|unprompted propose-only turn| turnservice
    commands --> services
    registry --> outer

    subgraph adapters[Adapters - /plugins]
        channelAdapter[Telegram channel]
        modelAdapter[OpenAI-compatible model]
        repositoryAdapter[GitHub and Git adapter]
        runnerAdapter[Restricted local-process runner]
        calendarAdapter[Google Calendar adapter]
        mcpAdapter[Generic MCP client manager]
        stores[File-backed state, context, and session stores]
        schedulerAdapter[Local scheduler]
    end

    adapters -. implement .-> ports
    commands --> stores

    subgraph systems[External systems and durable storage]
        telegramAPI[Telegram API]
        modelAPI[Configured model provider]
        github[GitHub]
        google[Google Calendar]
        mcpServers[Remote MCP servers<br/>Railway first]
        process[Git and local processes]
        data[Railway volume - /data<br/>config, state, context, sessions, runs]
    end

    channelAdapter --> telegramAPI
    modelAdapter --> modelAPI
    repositoryAdapter --> github
    runnerAdapter --> process
    calendarAdapter --> google
    mcpAdapter --> mcpServers
    stores --> data
    schedulerAdapter --> data
```

Solid arrows show runtime calls and message flow. The dotted adapter-to-port
arrow shows dependency inversion: kernel code depends on ports, while adapters
implement those ports and are selected only by bootstrap. Direct commands and
deterministic scheduled messages skip the model loop; scheduled agent turns
and heartbeat turns enter it through `internal/kernel/turns` with a restricted
tool set and no ambient conversation history. Repository changes are not a
separate lane: they are tool calls in the same loop.

## The agent loop

One tool-calling loop (`internal/kernel/agent.Loop`) handles every owner
turn — conversation, Calendar, scheduling, and repository work alike. There
is no separate "coding model" or CLI subprocess. Deterministic slash commands
(`/status`, `/model`, `/config`, ...) and a schedule created with
`ports.ScheduleExecutionMessage` (a reminder or watchdog-style notification)
bypass the model entirely, the latter delivering its instruction text
verbatim. Everything else enters the loop with the selected model alias, the
tools available for that message's source, current runtime capabilities, and
durable context (`SOUL.md`, `memories/USER.md`, `memories/MEMORY.md`).

A turn ends on exactly one condition: the model stops calling tools. No tool
is terminal — shipping a change is an action the model takes mid-turn and
whose result it reads, so it reports the pull-request URL conversationally
and can keep working afterwards. A failing tool is handed back as that tool's
result rather than ending the turn, which is why a rejected `patch` is
recoverable without starting anything new.

Length does not end a turn either. `agent.ContextPolicy` is a context budget,
not a work budget: when the exchange the loop itself produced outgrows it, the
oldest whole steps (an assistant message plus the tool results answering it,
never a lone result) are folded into a checkpoint summary the model keeps
reading, and the turn continues. The instructions, durable context, and the
owner's actual request are never compacted away. `maxToolStepsPerTurn` is what
is left of the old cap: a runaway guard against a model that calls tools
forever without ever answering.

Every turn writes a durable transcript through `agent.Transcript`, whether or
not it is editing anything, under `/data/sessions/<id>/`. A transcript records
what was asked and what happened, in order. It has no repository, branch, or
phase, because a conversation about the weather has none of those.

Branched work is a separate record: a `Change` (`/data/changes/<id>.json`),
holding base revision, diff, validation evidence, commit, pull request, and
check state, with the three-phase lifecycle. `Thread.ChangeID` binds the
thread's writable checkout to the change its edits belong to. A change stores
no workspace path — the live checkout belongs to the thread and is reaped with
it, while the change outlives it. `Repository` and `Branch` are not that same
duplication: they are what the change *was*, which stays meaningful long after
the checkout is gone, and is what makes `/runs` history readable.

Direct owner Telegram messages additionally see recent conversation history
and get the full tool set, including `workspace_edit` and `propose_change`
plus discovered MCP tools. Scheduled agent turns and heartbeat turns are
self-contained instead: no ambient recent-conversation history, so an old
chat instruction cannot be silently revived.

They differ in what they are *for*, and the allowlists follow that. A
scheduled turn runs work the owner wrote a schedule to ask for, so it carries
the repository write tools (but no MCP tool) and may branch a checkout and
ship it — only ever as a *proposal*, though: the pull request is a draft, the
branch is one it created, and a change the owner already has open in that
thread is off limits. What holds is not that it cannot write but that nothing
it does lands without a payload-bound authorization and a human-reviewed pull
request. The manifest's `self_repository` names the registered repository
holding Eggy's own source, so such a turn knows where its `AGENTS.md` and
`docs/ARCHITECTURE.md` live.

A heartbeat is a check-in on the owner rather than a work tick: read-only
plus the memory-curation tools, and deliberately none of the repository write
tools. Its job is to decide whether anything is worth saying and to curate
durable context, not to start work nobody asked for — which also keeps its
cost proportionate, since every tick is a model call whether or not it
produces a check-in. A heartbeat turn
additionally sees the owner-editable `HEARTBEAT.md` checklist and is skipped
entirely, with no model call, while any turn is active; silent `USER.md`/`MEMORY.md` curation on a heartbeat turn is never
gated by quiet hours or the weekly proactive-message limit, only the
Telegram check-in itself is.

Unprompted output — heartbeat, scheduled agent turns, and scheduled messages
— is Telegram-only by decision, not by default. Each of those paths stamps
`proactiveDestination()` on the turn's ctx explicitly rather than relying on
`destination.FromContext`'s Telegram fallback, so an event carrying a web
destination is still overridden. The web UI is a pull surface the owner
opens, and one proactive channel keeps `HeartbeatPolicy`'s quiet-hours and
weekly-limit accounting meaningful rather than per-channel.

It follows that a web-only deployment produces no unprompted output at all.
`newRoutedChannel` gives it a router over a *noop* Telegram rather than the
web channel unwrapped, so Telegram-addressed delivery is dropped instead of
redirected into a thread the owner never asked to be pushed to. The turns
themselves still run — a heartbeat's silent `USER.md`/`MEMORY.md` curation is
unaffected — only the outbound check-in disappears.

There is exactly one selected model per turn with no automatic escalation: the
owner picks the alias with `/model`, and no code path silently swaps in a more
expensive one. Repository edits run as ordinary tool calls in this same loop
rather than being delegated to a coding-agent CLI subprocess, so editing and
conversation share one transcript, one tool surface, and one termination
condition.

### MCP tools

`plugins/tools/mcp` is a generic MCP client built on the official Go SDK. Bootstrap creates one runtime per configured `mcp.servers` entry, discovers every `tools/list` page, applies exact include/exclude filters, and projects each selected tool into the existing `ports.Tool` interface as `<server>__<normalized_tool>`. The kernel and ports have no MCP dependency.

The catalog is live rather than a boot-time snapshot: the manager owns one tool set derived from each server's last successful discovery, and the loop reads it once per turn through a provider registered on `services.ToolRegistry`. Reconnecting a server that was down at boot or whose session died, refreshing on a `tools/list_changed` notification, and dropping one server's tools on logout therefore all take effect on the next turn without restarting the process. `Manager.Reconnect` is the single repair path, reached from `/mcp reload`, a completed login, a probe that found a dead session, and the call gate when a tool's cooldown expires.

Only direct-owner turns receive these projected tools; the explicit scheduled/heartbeat allowlists omit them. Server connection, authentication, discovery, and catalog-staleness state are isolated per server; readiness remains based on Eggy's local stores rather than remote MCP availability. Failure accounting is per *tool*: `failure_threshold` consecutive failures of one tool cool that tool down for `cooldown`, leaving every other tool on the same server callable. A projected tool name already claimed by another server is skipped with a warning on the losing server rather than disabling it.

#### Trust model: a configured MCP server is trusted code

MCP tool calls carry **no payload-bound approval**, unlike commits, pushes,
pull-request creation, and Calendar mutations. This is a stated decision, not
an oversight.

A server enters the catalog only through `mcp.servers` in `config.yaml`,
edited on the host or through the owner-authenticated web settings panel, and
only takes effect on restart. The agent has no tool that adds, edits, or
enables a server; there is deliberately no runtime `/mcp add`. A server
definition carries an auth mode, a tool filter, and for stdio a command line
and an environment allowlist — all of which belong in reviewed configuration
rather than in a chat message. The settings panel refuses stdio entirely, so a
command line only ever comes from the file.

So a configured MCP server is trusted the same way a configured repository is:
the owner reviewed it, and its tools then run ungated inside a turn. Eggy is a
single-owner agent, and the environment that names an MCP server already holds
the provider keys, the GitHub token, and the encryption key.

What still bounds an MCP tool, none of which is an approval:

- Unprompted turns cannot reach one at all. Scheduled and heartbeat turns run
  with explicit allowlists of kernel tool names, enforced twice — projected
  tools are filtered out of the definitions sent to the model, and the loop
  refuses to execute a call outside the allowlist.
- The per-server `tool_filter` include/exclude list decides which of a
  server's tools are projected at all. Railway's `list-variables` is excluded
  in the shipped example precisely because its results would place deployment
  secrets into model context.
- Per-*tool* failure accounting: `failure_threshold` consecutive failures cool
  that one tool down for `cooldown`.
- A stdio child gets a constructed environment and its own process group.

What is **not** bounded: an owner-prompted turn may invoke any projected tool
with any arguments, with no per-call confirmation. A malicious or compromised
server can do anything its projected tools expose. Narrowing a server's
`tool_filter`, or disabling it, is the control — there is no runtime gate.

*This decision should be re-opened if Eggy ever gains more than one owner, or
if MCP servers start being added by someone other than the person who accepts
the risk.*

OAuth uses the SDK's `auth.OAuthHandler` seam and exported metadata/DCR helpers, with standard PKCE and `oauth2` exchange/refresh. Dynamic client information and tokens are stored as one AES-256-GCM record per server inside the shared `/data/auth.json` document (section `mcp`), independently from `state.json`. Bearer credentials are resolved only from the configured environment-variable name.

Two transports are supported, selected per server by `transport` and resolved in one place (`session.go`): `streamable-http` against a hosted `url`, and `stdio` against a locally spawned `command`. A stdio child's environment is *constructed* from the server's `env_allowlist` plus `PATH` and `HOME` rather than inherited, so Eggy's other credentials cannot reach it, and it is started in its own process group so closing the session kills anything it spawned in turn. Stdio deliberately adds no isolation beyond that: the child is the same user with the same filesystem access as Eggy, the same trusted-code assumption the `terminal` tool already makes, and container isolation would address both rather than one. Config rejects mixing the two transports' fields, and the web settings panel refuses to edit a stdio server, so a command line and an environment allowlist only ever come from reviewed configuration. Resources, prompts, roots, sampling, elicitation, legacy SSE, and MCP Apps remain out of scope.

### The primitive tool set

`services.NewPrimitiveTools` builds the one kernel-owned CRUD-over-a-workspace
set — `read_file`, `write_file`, `patch`, `terminal`. Bootstrap builds it
*once* into the one registry the one loop runs on, so a primitive name
resolves to one definition and one implementation. `services.PrimitiveNames`
names them, and a bootstrap test asserts exactly one definition per name;
because `ToolRegistry.Register` rejects duplicates, an adapter that tries to
shadow a primitive fails bootstrap. MCP tools are not registered at all --
they are a live view merged in per turn, where the same invariant holds
because a registered tool always wins the name and the dynamic one is
dropped.

None of them takes a `repository` argument. Each resolves its workspace from
`services.WorkspaceSessions.Resolve`, which has exactly one source: the
checkout attached to the calling conversation thread. Without one, the call
fails with `ErrNoWorkspace`.

Writes are gated by *result*, not by registry membership: `patch` and
`write_file` are always in the model's tool list and return
`ErrWorkspaceReadOnly` when the thread's checkout has no branch yet.
The model learns why an edit was refused instead of silently losing the
capability between turns.

### Repository and workspace tools

- `workspace_open` — attaches a read-only checkout of a configured repository
  to the calling thread. It persists across turns *and across restarts*, so
  successive greps and reads accumulate against one clone rather than paying a
  clone per call. No branch, no diff, no approval.
- `workspace_edit` — branches that same checkout in place (`eggy/<session-id>`)
  so the write primitives resolve it as writable, and opens the session that
  records the edits. It never re-clones: the checkout the owner was reading is
  the one the edits land in.
- `workspace_close` — detaches and destroys that checkout.

The binding lives on `ports.ThreadStore` (a `threads.workspace` /
`threads.workspace_repository` column pair), not in memory. Three consequences
follow, all in `services.WorkspaceSessions`:

- **Boot reconciliation.** `Recover` runs before the first turn is served. It
  asks the runner (`ports.WorkspaceProbe`) whether each recorded checkout still
  exists and detaches the ones whose directory is gone, so a primitive never
  resolves onto a path a volume wipe removed. A runner that cannot probe causes
  every binding to be dropped: an unverifiable record is not a trustworthy one.
- **Idle reaping.** `CleanupIdle` runs on the minute ticker against the
  `runner.retention` cutoff and destroys the checkout of any thread whose last
  activity predates it. It is the *only* checkout reaper: a change never owned
  the workspace, so it has nothing to release.
- **Attach upserts.** Telegram's fixed thread never calls `CreateThread` — it
  has no sidebar entry — so `AttachWorkspace` creates the row when absent.

Shutdown deliberately destroys nothing: surviving a restart is the point.
- `repository_list` — configured repository names and safe metadata.
- `repository_github` — read-only GitHub issue/PR/check-run metadata; never
  clones.
- `propose_change` — ships whatever is in the thread's branched checkout:
  Eggy captures the diff itself, verifies the checkout is still on the branch
  and HEAD it recorded, then runs the commit → push → pull-request chain and
  returns the pull-request URL as an ordinary tool result. It is not terminal,
  so a second round of edits in the same thread updates the same pull request.

## Pull-request checks

A proposed change is not the end of the loop. On the same minute ticker,
`services.ChecksWatcher` polls the check runs for each change that opened a
pull request and whose thread still has the branched checkout attached,
reading through the same `ports.RepositoryReader.Checks` path that backs
`repository_github`'s `checks` kind rather than adding a second GitHub
surface. A suite that is still running is not a result and is ignored; a green
one is recorded and stays silent.

A failed suite is enqueued as `events.TypeChecksCompleted` against that
thread's destination and handled as an ordinary turn whose instruction carries
the failing checks as evidence. The workspace is still on the branch, so the
agent fixes the failure with the same primitives and calls `propose_change`
again, updating the same pull request. The commit whose checks were handled is
recorded on the change, so one failure resumes the thread exactly once, and
the event ID (`checks:<change>:<commit>`) makes the dispatcher's own dedupe
cover a restart mid-poll.

## Changes

`workspace_edit` branches the thread's checkout and opens a change;
`propose_change` ships it. Neither drives a nested loop — the editing in
between is ordinary turns. The workspace lives under the configured
`runner.root`, which must be on the durable Railway volume (e.g. `/data/runs`)
for a session to survive a restart.

Both records live outside `/data/state.json` so the core state schema needs no
migration for them:

```text
/data/sessions/<transcript-id>/
  session.json   # instruction and timestamps
  events.jsonl   # append-only event log and concise semantic milestones
/data/changes/<change-id>.json   # repository, branch, base revision, diff,
                                 # validation, commit, pull request, checks
```

The transcript document is still named `session.json`: a transcript loads from
exactly the file the previous shape wrote, and the fields that went away are
ignored, so no data migration was needed. The stores are the `CodingRun` in `state.json` remains the source of truth for the
commit/push/PR lifecycle, using the session ID as its run ID.

Continuing is ordinary conversation: the thread's workspace is still open and
still branched, so the owner simply says what to do next. On restart, a
session persisted as `running` is marked `interrupted` and is never
auto-resumed. Progress renders as concise semantic milestones (`Inspected:
...`, `Edited: ...`, `Validation: ...`), not raw tool-call noise, on the
surface that started the turn: a `ports.ProgressReporter` takes the turn's own
context, so a turn from a web thread reports into that thread and one from
Telegram reports into Telegram.

Turns run in the background: every surface enqueues its event and `App.Run`
dispatches each one on its own goroutine, so no inbound request is held open
for a turn's duration. That makes a turn something the owner can talk to
while it runs. `services.ActiveTurns` holds both halves of that:

- **Steering.** A message arriving while a steerable turn is running joins it
  at the next step boundary (`agent.RunOptions.PendingInput`) instead of
  starting a competing turn — the owner redirects work in progress rather than
  racing it. Only direct owner turns are steerable; a scheduled or heartbeat
  turn refuses, because folding an owner message into one would hand it the
  ambient instruction that self-contained turns exist to prevent.
- **Stopping.** `/stop` cancels the turn running in the calling conversation,
  and `agent.Loop.Run` checks `ctx.Err()` at each step boundary so the stop is
  honoured even by a tool that ignores ctx. The checkout and its session
  survive: stopping is not a rollback, so a stopped turn leaves the work
  inspectable and resumable by simply saying so.

## Shipping and authorization

Commit, push, and pull-request creation each still require their own
independent, expiring, payload-digest-bound approval — but `ShippingService`
decides each one automatically in sequence instead of waiting for an owner
Telegram tap. The owner review that matters happens on the pull request, not on
three consecutive taps that approve a payload they cannot see; automating the
decision removes the taps without removing a single check.

Unchanged regardless of automatic decision:

- A changed diff, branch, or commit invalidates the pending approval
  (`ApprovalService.Authorize` checks the payload digest).
- Local and remote HEAD are revalidated immediately before push and before
  pull-request creation.
- Protected branches are denied at push time even with an approved payload.
- Eggy never merges a pull request.

Calendar create/update/delete still requires an explicit owner tap in
Telegram — that approval path is unchanged. Repository registration
(`add_repository`) is the other action that still waits for an explicit
owner decision rather than deciding itself.

## Models and providers

Version 2 configuration defines named OpenAI-compatible providers and named
model aliases; `agent.default_model` picks the default, and the owner can
inspect or override it with `/model`. Provider request/response types,
authorization, and usage parsing stay inside the model adapter — the kernel
only depends on `ports.Model`. Version 1 configuration (a single DeepSeek
Flash/Pro pair) still loads and is mapped to an implicit single alias; it
does not gain access to the escalation behavior that alias name might imply,
because that behavior no longer exists.

## Telegram and CLI surfaces

Telegram is the primary channel: webhook signature and owner-ID verification,
update deduplication, HTML replies with plain-text fallback, and in-place
message edits for approval outcomes and run progress. The `eggy` CLI reads
the same `config.yaml`/`state.json`/session files for local/offline
inspection and `config` management without constructing the full runtime.

Both surfaces share one command set: `/status`, `/capabilities`, `/context`,
`/repositories`, `/runs`, `/runs show <id>`, `/stop`, `/schedules`,
`/memory`, `/clear`, `/model [alias|default]`, `/config get|set ...`,
`/usage [reset]`, `/calendar_auth`, `/mcp [status|probe|login|logout|reload]`, and `/restart`. `/restart` triggers a
self-exec-in-place process restart to pick up an edited `config.yaml`/`.env`
without an external supervisor, which keeps the single-replica deployment model
intact: no orchestrator is required to bring the process back, and no second
`eggyd` is ever briefly live alongside the old one.

`/capabilities` and `/context` are answered by `services.Diagnostics` rather
than by command code. Both reports are measured through the same assembly a
turn uses — `agent.Instructions` for the injected system sections and
`Loop.ToolDefinitions` for the tool set that turn would carry — so the
diagnostic and the turn cannot disagree. Diagnostics is read-only by
construction and reports names, byte counts, and readiness flags only: no
credential, environment value, or credential path can reach either view.

### The channel port

`ports.Channel` carries no chat or thread identifier. A turn's target is the
`approvals.Destination` stamped on its context, and each channel resolves it
itself: the Telegram client is bound to the single owner chat at
construction, and webchat reads the destination's thread ID off the same
context. `bootstrap.routedChannel` therefore only *chooses* a channel. A tool
or helper built once at startup consequently reports into whichever
conversation is actually running rather than into a fixed one, which is what
makes progress, typing indicators, and approval prompts follow the surface
that triggered them.

The port covers delivery only. Acknowledging a Telegram callback query is
part of receiving an update, so `telegram.WebhookHandler` does it as the tap
arrives; no other surface implements a concept it doesn't have, and the
callback query ID never travels on an `events.Event`.

`Channel` itself is only the floor — `Deliver` and `DeliverApproval` — the two
things any surface must be able to do. In-place edits
(`ports.TrackableChannel`: `DeliverTrackable`/`EditText`) and typing
indicators (`ports.TypingChannel`: `SendTyping`) are optional extensions, so a
surface without them implements the port honestly instead of stubbing methods
it cannot honour. Consumers never assert for them by hand: the
`channelutil.DeliverTrackable`/`EditText` helpers and `channelutil.StartTyping`
do it once and degrade predictably — a fresh message per update instead of one
live message, and no typing indicator at all. Telegram and webchat both
implement all three interfaces today; `bootstrap.noopChannel`, used when no
surface is configured, implements only `Channel`.

## Safety invariants

These hold regardless of what else changes in the kernel or an adapter:

- `internal/kernel` and `internal/ports` stay provider-neutral; adapters
  register only through `internal/bootstrap`.
- File locking and atomic writes for config, state, context, and session
  persistence.
- Telegram webhook authentication, owner allowlisting, update deduplication.
- Runner root restriction, path validation, environment allowlisting,
  timeout/output bounds, process-group cancellation, workspace cleanup.
- Active-secret filtering and secret-like content rejection before any
  `USER.md`/`MEMORY.md` write.
- Calendar mutation approvals, OAuth refresh-token encryption, idempotent
  creates, ETag-bound updates/deletes.
- MCP credentials never enter YAML or `state.json`; remote results remain
  bounded, and binary content is represented only by metadata.
- Independent commit, push, and pull-request authorization with
  protected-branch denial, whether decided by an owner tap or automatically.
- An unprompted turn may only *propose*: its pull request is always a draft,
  always on a branch of its own, never on a base or protected branch, and
  never on top of a change the owner has open. The draft flag rides inside the
  shipping payload, so it is covered by the same payload-digest authorization
  as the branch and diff.
- Heartbeat turns cannot reach `workspace_edit`, `propose_change`, or the
  write primitives at all: a check-in is not a work tick.
- Scheduled and heartbeat turns cannot reach MCP tools.
- Eggy captures the diff and verifies branch/HEAD equality itself before
  shipping, independently of what the model reports.
- Exactly one `eggyd` replica while operational state is file-backed.
