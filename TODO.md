# Eggy roadmap

This file tracks unfinished work only. Current behavior belongs in `README.md`
and `docs/ARCHITECTURE.md`; completed implementation history remains in git.

Priorities are ordered by urgency, then by dependency. Remove an item when its
implementation and focused tests have landed — do not leave it here as a record
of finished work.

## P0: Separate the agent core from its control surfaces

The delivery seam is clean: `destination` is its own kernel package,
`events.Event` carries a typed `Destination` each surface builds for itself,
`config.Owner.ID` is the system-wide identity, and the Telegram block is
optional so a web-only deployment boots without it.

The surface extraction has landed: the command surface is `internal/commands`
and the HTTP surface is `internal/web`, leaving `internal/bootstrap` at ~2.4k
non-test lines holding two layers rather than four — the composition root
(`app.go`, `mcp.go`, `logging.go`, `web_search.go`, `assistant_tools.go`, the
`migrate_*.go` files) and the turn orchestrator (`app_events.go`, ~790 lines).

What remains is the turn orchestrator itself. It is core agentic behavior, not
wiring — including the read-only/heartbeat allowlists that encode a documented
safety invariant in the one package no kernel test can guard.

### Move the turn orchestrator into the kernel

- [ ] Move `handleMessage`, `handleHeartbeat`, `handleApproval`,
      `messageHandlingPolicy`, and the read-only/heartbeat tool allowlists out
      of `internal/bootstrap/app_events.go` into a kernel `TurnService` that
      accepts a neutral turn request (destination, text, policy). Telegram and
      web then become peers that each only build that request.
  - [ ] Move the remaining half of the unprompted-turn invariant into the
        kernel with it. The invariant itself is held in the kernel already
        (`internal/kernel/services/unprompted.go`, tested in
        `unprompted_test.go` and `change_tools_test.go`), but *which turns are
        unprompted* is still decided by `proposeOnlyRunOptions` and the
        `services.WithUnpromptedTurn` calls in `app_events.go`, where no kernel
        test can guard it.
- [ ] Give each surface a narrow interface onto the core rather than the whole
      40-field `App` struct. `internal/commands` and `internal/web` are their
      own packages now but still receive broad dependency sets.
- [ ] Drop the dead `case <-ctx.Done()` branch in `App.Enqueue`
      (`app_events.go:32`): with a `default` present it can never fire, so a
      cancelled context reports "event queue is full" instead of the context
      error. Drop the `ctx.Done()` case or drop the `default`, not both.

Unprompted output stays Telegram-only, deliberately. Heartbeat, scheduled agent
turns, and scheduled messages all stamp `proactiveDestination()`
(`app_events.go`) on ctx explicitly rather than relying on a channel fallback.
The web UI is a pull surface the owner opens, not one Eggy pushes to, and a
single proactive channel keeps `HeartbeatPolicy`'s quiet-hours and weekly-limit
accounting meaningful rather than per-channel. A web-only deployment therefore
produces no unprompted output at all. Revisit only if the web UI gains real
push delivery; turning this into *configuration* would change that one
function.

## P1: Make MCP servers dynamic

`plugins/tools/mcp` is complete within its design — OAuth with encrypted
storage, paginated discovery, include/exclude filtering, name normalization,
per-server cooldown, an opt-out for servers that cannot take parallel calls.
The limit is the design itself: it assumes MCP servers are boot-time static
and HTTP-only, so every operational question resolves to "restart Eggy."

### Reconnect and refresh without a process restart

These are one change, not two: reconnection is pointless while the tool
catalog is a slice copied at wiring time.

- [ ] Reconnect disconnected servers. `NewManager`
      (`plugins/tools/mcp/manager.go:33`) connects exactly once, so a server
      that is down at boot stays `unavailable` for the process lifetime. Worse,
      a session that drops mid-life never recovers: `callGate` restores
      `StateReady` once `cooldownUntil` passes but never re-establishes the
      session, so the server flips back to ready and fails again forever.
      `Probe` diagnoses this and repairs nothing.
- [ ] Make the agent's tool set a live view over `Manager.Tools()` rather than a
      slice snapshotted during wiring, so a reconnect or a changed catalog takes
      effect on the next turn.
- [ ] Honor `ToolListChangedHandler` for real. It currently only sets
      `status.ReloadRequired`, and `/mcp reload` resolves to `service.restart()`
      (`internal/commands/commands_mcp.go`). Same for `/mcp logout`, which
      restarts the process to drop one server's tools.
- [ ] Skip a colliding tool with a warning instead of disabling its whole
      server. `manager.go:114` sets `serverTools = nil` and marks the server
      `unavailable` because one tool name clashed with another server's.
- [ ] Make the failure policy per-tool and configurable. `resultHandler`
      hardcodes three consecutive failures into a 30-second cooldown counted
      per *server*, so one broken tool trips the breaker for every tool on it.

### Support stdio servers

- [ ] `ServerConfig` carries a `URL` and nothing else, so Eggy can reach hosted
      HTTP servers and none of the `npx`-launched stdio servers that make up
      most of the ecosystem. Add a stdio transport alongside the HTTP one.
- [ ] Decide the sandboxing story in the same change: stdio means spawning
      subprocesses, which is the same question as "P3: Evaluate stronger
      execution isolation". Credentials stay outside the child environment and
      only an explicit allowlist is forwarded, exactly as repository execution
      already does.

### Close the MCP authorization gap

- [ ] Decide whether MCP tool calls need an approval classification. Repository
      writes, commits, pushes, pull requests, and calendar mutations all require
      payload-bound authorization; an MCP tool is an arbitrary remote side
      effect gated by nothing but a cooldown counter. The only current
      mitigation is blunt — unprompted turns cannot reach MCP tools at all —
      and owner-prompted turns have no gating whatsoever. Recorded as a known
      gap in the standing constraints below until this is resolved.
- [ ] Add a test that an MCP tool can never shadow a kernel primitive.
      `normalizeToolName` guarantees a `server__tool` shape that cannot collide
      with `read_file`, `terminal`, `patch`, or `write_file`, but that is
      incidental today and the constraint is load-bearing.

Servers stay configured in `config.yaml`'s `mcp.servers` map. Runtime
`/mcp add <url>` is deliberately out of scope: a server definition carries an
auth mode and a tool filter, both of which belong in reviewed configuration
rather than in a chat message.

## P1: Make context and capabilities inspectable

- [ ] Add a deterministic `/capabilities` view showing the selected reasoning
      model, registered assistant tools, configured repositories, enabled
      integrations, and implementation-loop readiness.
- [ ] Add a deterministic `/context` view showing injected bytes or estimated
      tokens per context file, recent-history and session-context sizes,
      tool-schema overhead, truncation markers, and the known context limit and
      remaining budget.
- [ ] Extend `/runs` detail with the model, base revision, phase, provider
      session ID, elapsed time, and validation status.
- [ ] Derive every diagnostic from bootstrap and persisted runtime state. Never
      expose credentials, raw environment contents, or credential paths.

## P1: Harden durable context and recall

- [ ] Reject duplicate, secret-like, prompt-injection, exfiltration, and
      invisible-Unicode content before durable context writes.

Durable-context roles remain fixed: `SOUL.md` describes Eggy's identity and
tone, `USER.md` holds stable owner preferences, and `MEMORY.md` holds compact
facts, decisions, and reusable lessons. None may override runtime policy or
grant capabilities. Conversation recall is served by the SQLite-backed memory
store (`plugins/memory/sqlite`), not by file-backed excerpts.

## P1: Correctness and exposure fixes

Filed from a code review; each is small and independently landable.

- [ ] `lineEmitter` is quadratic and unbounded
      (`plugins/runner/localprocess/runner.go:248`). On each newline it calls
      `pending.String()` (a full copy), `Reset()`, then rewrites the remainder —
      O(n²) for a write chunk containing many lines. Unlike the `cappedBuffer`
      beside it, `pending` has no limit, so a command emitting megabytes without
      a newline (progress bars using `\r`, minified output, a runaway build log)
      grows it without bound. This streams agent-run `go test`/`build` output,
      so it is reachable in normal operation. Scan for `'\n'` by index, consume
      in place, and cap `pending` at `r.maxOutput`.
- [ ] The login throttle keys on the wrong address. `clientIP`
      (`internal/web/web.go:252`) reads only `r.RemoteAddr`, which behind
      Railway's proxy is the proxy's address for every request — so all attempts
      share one bucket, the throttle barely slows an attacker, and an attacker
      can lock the owner out. Needs an explicit trusted-proxy policy with
      `X-Forwarded-For` parsing; it has to be explicit, since blindly trusting
      the header is worse than not parsing it. Separately, `throttle.Delay` is
      applied via `time.Sleep` inside the handler (`web.go:202`), holding a
      server goroutine per throttled request.
- [ ] Path containment is lexical, not symlink-aware. Both
      `resolveWorkspacePath` (`internal/kernel/services/workspace_path.go:20`)
      and `Runner.withinRoot` (`runner.go:178`) use `filepath.Abs` +
      `filepath.Rel` with no `EvalSymlinks`, so a symlink inside the workspace
      pointing at `/` passes both checks. The caveat is that the terminal tool
      runs `sh -c` with an arbitrary command string, so containment was never a
      real boundary — the child can read outside the workspace directly. So this
      is a choice, not a bug: either add `EvalSymlinks` and make the workspace
      an actual boundary, or amend the comment that claims "a primitive can
      never touch a file outside the checkout the session is bound to." Prefer
      the comment fix plus an explicit threat-model note, since the stated model
      is that configured repositories are trusted.
- [ ] Give the migration code an exit plan. `legacy_coding_runs.go`,
      `migrate_auth.go`, and `migrate_cron.go` are accumulating with none; for a
      single-user, single-replica app where the deploy is under our control they
      become permanent carrying cost. Add a dated removal note to each or
      quarantine them in a `migrations/` subpackage.
- [ ] State plainly in `README.md` that the web password is a plaintext shared
      secret in config rather than a hash. Defensible for a single-owner app,
      but it should be a documented choice.

## P2: Build plug-and-play capabilities

New task workflows belong in isolated, on-demand procedural skills; new
providers and integrations belong in compiled packages under `plugins/`, wired
only in `internal/bootstrap`. Neither needs a generic, runtime-loaded plugin
mechanism, and neither may redefine a kernel-owned primitive tool.

- [ ] Store repeatable workflows and troubleshooting procedures in skills rather
      than expanding `MEMORY.md`. (Mechanism is in place; nothing yet steers the
      agent to prefer this over a `MEMORY.md` write in practice.)
- [ ] Add read-only `/skills browse <repo-url>` (lists `**/SKILL.md` paths,
      installs nothing) and `/skills clone <repo-url> <path>` (fetches one file,
      opens the normal approval request with the fetched body attached) instead
      of a bulk importer.

## P2: Improve run recovery and rollback

- [ ] When the owner continues an existing pull request, use its branch as the
      run base instead of branching from trunk and opening a duplicate pull
      request.
- [ ] Resolve the pull request a continuation belongs to from the thread alone.
      The session already records its URL and number (which is what the checks
      loop reads); what is missing is resolving it when the owner continues
      without the originating thread.
- [ ] Save a bounded patch and validation artifact before workspace cleanup so
      rejected or interrupted work remains inspectable without retaining the
      full checkout.
- [ ] Add cleanup and retention diagnostics for abandoned workspaces and
      provider sessions.
- [ ] Add an explicit discard operation that cannot affect the owner's checkout.
- [ ] Evaluate a "retry from base" operation for contaminated or partially
      failed workspaces.

The immutable base commit is the pre-run checkpoint. Resumption always requires
a new owner instruction, and rollback stays inside the isolated run workspace;
it must never destructively modify the owner's checkout.

## P3: Evaluate stronger execution isolation

- [ ] Evaluate container-per-run isolation while keeping the current
      trusted-repository assumption explicit.
- [ ] If adopted, run as a non-root user with explicit mounts, dropped
      capabilities, bounded resources, and an explicit network policy.
- [ ] Keep credentials outside coding workspaces and forward only the minimum
      environment required by each subprocess.
- [ ] Settle this alongside stdio MCP transport (see "Support stdio servers"),
      which raises the same subprocess question.

## Operational follow-ups

- [ ] Reset Railway's deployed `/data/config.yaml` so the next boot generates the
      current unversioned config shape. Manual deployment step, not a code
      change.

## Standing constraints

Every roadmap item must preserve these properties:

- `internal/kernel` and `internal/ports` remain provider-neutral; adapters live
  under `plugins/` and are registered only in `internal/bootstrap`.
- The primitive tool surface (`read_file`, `terminal`, `patch`, `write_file`)
  is kernel-owned and defined exactly once. Adapters, MCP servers, and skills
  extend around it with namespaced tools and never shadow a primitive.
- Config, state, context, and session stores retain file locking and atomic
  writes. Existing `/data/state.json` files remain compatible or receive an
  explicit, tested schema migration.
- Telegram retains webhook authentication, owner allowlisting, and update
  deduplication.
- Repository execution retains root and path restrictions, environment
  allowlisting, timeouts, output limits, process-group cancellation, isolated
  workspaces, and cleanup.
- Ambiguous requests are clarified before a modifying workflow starts. Progress
  is streamed as normalized provider-neutral events, and Eggy independently
  captures the complete final diff and validation evidence before shipping.
- Durable context retains active-secret filtering and secret-like content
  rejection.
- Calendar mutations retain explicit owner approval, OAuth token encryption,
  idempotency, and ETag binding.
- Commit, push, and pull-request creation retain independent payload-bound
  authorization; protected branches remain unpushable even with approval. Eggy
  never merges a pull request.
- MCP tool calls are the one capability with no payload-bound authorization.
  Owner-prompted turns invoke them ungated; the only mitigation is that
  unprompted turns cannot reach MCP tools at all. This is a known gap, tracked
  under "Close the MCP authorization gap", not a settled design.
- Unprompted turns may only *propose* repository changes: isolated branch,
  draft pull request, never a base or protected branch, and never a change the
  owner already has open in that thread. Work on an owner-facing branch
  remains explicitly owner-triggered. The invariant that holds unconditionally
  is that nothing lands without payload-bound authorization and a
  human-reviewed pull request; the draft flag travels inside the shipping
  payload so it is bound by that authorization too.
- A heartbeat is a check-in on the owner, not a work tick: read-only plus
  memory curation, and no repository write tools at all. Only a scheduled turn
  carries the propose path, because the owner wrote the schedule asking for
  it. Widening this is a product decision, not a safety one — the safety
  machinery above already covers it.
- Operational state remains file-backed, so production runs exactly one `eggyd`
  replica.
- Every capability has a small, swappable boundary: task workflows are
  on-demand skills, while providers and integrations are compiled plugins with
  explicit bootstrap configuration. Provider payloads, credentials, channel
  formatting, and CLI protocols remain inside plugins; no capability may load
  arbitrary native code at runtime.
- The capability manifest stays small and reflects only the tools actually
  available to the current turn.
- Changes are developed test-first and verified with focused tests followed by
  `make fmt vet test race build`; run `make smoke` when Docker is available.
