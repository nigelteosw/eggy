# Eggy roadmap

Unfinished work and standing constraints only. Completed work lives in git;
current behavior lives in `README.md` and `docs/ARCHITECTURE.md`. Delete an item
once it lands.

## What Eggy is

One owner's always-on agent daemon: a chat surface, a model, durable memory,
and a small tool set. It is not a personal-assistant product, not a scheduler,
and not a CI system.

The design target is a **pi-shaped core**: a handful of tools, a small prompt,
and everything beyond the core reached through *configuration* rather than
through a compiled-in feature. When a capability is not configured, it must
cost nothing at runtime — no tool schema, no goroutine, no store, no HTTP
route, no prompt bytes.

**Core (always present, not configurable away):**

- one `eggyd` process, one owner, one replica;
- model routing over configured providers and aliases;
- conversation memory in SQLite with full-text recall;
- owner-facing Markdown context (`SOUL.md`, `USER.md`, `MEMORY.md`);
- one tool registry with exactly one tool source;
- at least one chat surface.

**Configurable (absent unless configured):** Telegram channel, web chat and
settings, read-only repository inspection, MCP servers, schedules, native
Calendar.

**Extension mechanism:** MCP for capabilities, Markdown skills for procedure.
A native adapter is otherwise only justified when a port must stay
provider-neutral (models, channels, storage), or when a capability needs
per-call authorization that MCP structurally cannot give it. New product
features do not get compiled in.

**Calendar is the one deliberate native product adapter.** It is in daily use,
and its mutations carry independent payload-bound approvals — an MCP calendar
server would be trusted wholesale at configuration time, with no prompt when
the agent moves or deletes a real event. That safety difference, not
convenience, is what earns it a place in the binary. Anything else asking for
the same exemption must make the same argument.

## Measured post-P0 baseline (2026-07-30)

- 12,299 production Go lines and 10,788 test lines;
- largest remaining packages: `internal/kernel/services` 1,682,
  `plugins/tools/mcp` 1,526, `internal/bootstrap` 1,276, `internal/config`
  1,107, and `internal/web` 692;
- 7 kernel tools without Telegram, repositories, or MCP; 12 with repositories,
  and 13 with Telegram too, before the configured MCP catalog;
- 64 YAML-tagged config fields and 21 HTTP route registrations;
- machine-managed persistence still spans `state.json`, `auth.json`, `cron/`,
  and `eggy.db`; Markdown context and skills remain files by design.

These figures were taken with Calendar deleted. Restoring it (below) adds back
roughly 1,100 production lines and its config section; every other number in
this file already accounts for that.

## Targets for this pass

- **≤ 12,500 production Go lines** excluding generated web assets — the
  post-P0 figure above plus the restored Calendar adapter and no more;
- **≤ 6 tools** in the default core; **≤ 13** with Telegram and repositories
  configured; **≤ 4** additional Calendar tools; MCP counted separately;
- **three durable forms**: YAML for startup config, Markdown for owner-facing
  documents, SQLite for everything machine-managed;
- **≤ 50 config fields**;
- **one** runtime administration surface.

---

## P0: Consolidate the Calendar tool surface

Calendar is restored and wired (five tools, payload-bound approvals, tools
beside `CalendarService`, absence as the off switch). One reduction remains.

- [ ] Consolidate the five `calendar_*` tools into at most three.
      `calendar_calendars` is a lookup that belongs inside `calendar_list`'s
      response; create/update/delete can be one mutation tool with an explicit
      operation. Each mutation keeps its own `approvals.Action`, its own
      executor, and its own payload-bound approval — one tool must not become
      one blanket authorization.

---

## P1: Make the config surface the product surface

"Configurable" is the whole design claim, so the config must be small, flat,
and validated in one place.

- [ ] Collapse `internal/config` (now 1,107 lines, 64 YAML-tagged fields) to
      the sections that survive: `server`, `data_dir`, `agent`, `providers`,
      `models`, `telegram`, `repositories`, `runner`, `calendar`, `mcp`. Delete
      `commonConfigDocument`/`configDocument` duality once no legacy shape
      needs reading.
- [ ] Reject unknown keys with a message naming the key and the nearest valid
      one. A silently-ignored key is how a "configurable" system becomes a
      guessing game.
- [ ] Make every optional section's absence the off switch. No `enabled: true`
      field where an empty section already means "not configured".
- [ ] `config.example.yaml` documents every surviving field exactly once and is
      asserted against the parsed shape in a test.

---

## P1: Consolidate durable state in SQLite

- [ ] Write the schema-versioned migration design first: it must cover
      `state.json`, `cron/`, and `auth.json`, preserve encrypted payloads, and
      be safe to retry after interruption.
- [ ] Move approvals, processed event IDs, selected model, and schedules into
      SQLite tables behind the existing provider-neutral ports. Give the
      scheduler a narrow `ScheduleStore` interface instead of importing
      `cronfile`.
- [ ] Keep `SOUL.md`, `USER.md`, `MEMORY.md`, and skill Markdown as files.
- [ ] After one production deployment has imported and verified the old
      records, delete the legacy JSON/YAML stores and the migration reader.
      Document the last release that can import the old layout.
- [ ] Backup set becomes exactly `eggy.db`, `config.yaml`, `.env`, and the
      Markdown files. Readiness checks verify those and nothing historical.

Success criteria: `openStores` opens one database; one transaction can update
related approval and conversation state; `/data/state.json` compatibility is an
explicit migration, never a silent break.

---

## P1: Bootstrap composes, nothing more

- [ ] Move surviving tool definitions and input decoding next to the service
      that owns them, `internal/bootstrap/assistant_tools.go` included. The
      restored Calendar tools go beside `CalendarService`, not back into
      bootstrap.
- [ ] Make model adapter construction one selector function keyed by
      `ProviderConfig.Adapter`. A new provider adds one plugin package and one
      case.
- [ ] Stop retaining construction-only collaborators on `App`. Keep only what
      `Run`, `Ready`, `Close`, event handling, and HTTP handling use. Tests
      assert public behavior or use a fixture; they do not force production
      fields to stay reachable.
- [ ] Replace positional constructors with small dependency structs only where
      one already takes six or more collaborators. No containers, no service
      locators, no lifecycle interfaces whose only caller is bootstrap.

Success criteria: `internal/bootstrap` holds composition, event-loop
ownership, and surface routing only; adding an adapter changes no kernel
behavior and no other adapter.

---

## P2: Keep the prompt and tool budget honest

- [ ] Re-measure the per-turn context floor after the P0 cuts and record it
      once here. Tool schemas were the largest section by a wide margin; that
      is the number that matters, not prose bytes.
- [ ] One home per policy fact: tool descriptions explain invocation, runtime
      policy explains cross-tool constraints. Delete the duplicate.
- [ ] Report MCP schema bytes separately from kernel schema bytes. Do not
      build deferred tool loading until MCP schemas alone exceed ~10K tokens.
- [ ] Reject duplicate, secret-like, prompt-injection, exfiltration, and
      invisible-Unicode content before durable context writes.
- [ ] Do not describe lexical workspace containment as a sandbox. If isolation
      is needed for repository or stdio-MCP subprocesses, it is a container.
- [ ] Evaluate **approval-gated MCP tools**: a per-server
      `require_approval: [tool names]` list that routes a matching call through
      the existing approval flow, rendering the tool's arguments as the bound
      payload. This is the only thing that would let Calendar — or any future
      product capability — leave the binary without losing per-call
      authorization. Cost is a payload presenter for arbitrary MCP arguments
      and the loss of typed handling (timezones, relative ranges) the native
      service does today. Do not attempt it in the same pass as the Calendar
      restore.

---

## P2: Documentation and test weight

- [ ] `README.md` becomes a short operator guide: install, configure, run.
      `docs/ARCHITECTURE.md` is the only architectural narrative; ADRs hold
      durable trade-offs.
- [ ] Delete comments that narrate removed implementations or rejected
      alternatives. Keep comments stating a non-obvious invariant, an exported
      contract, or a security reason.
- [ ] Delete tests for removed behavior in the same change as the behavior. Do
      not chase a test-line target; keep focused safety and adapter contract
      tests even when they exceed the implementation.
- [ ] Audit `AGENTS.md`, `README.md`, `docs/ARCHITECTURE.md`, and
      `config.example.yaml` after every phase.

---

## Open decision: does Eggy write code?

Eggy is becoming a read-only code *reader*: `workspace_open`, `read_file`,
repository search and metadata, `workspace_close`. A pi-shaped *coding* agent
would instead ship a bounded edit tool and a bounded shell, and still never
commit, push, or open a pull request.

Both are defensible; they are not compatible. Until this is decided, do not
reintroduce write tools opportunistically. If write capability returns, it
arrives with all of: workspace root containment, timeouts, output bounds,
process-group cancellation, an environment allowlist that excludes repository
credentials, and no git remote-write path — and the owner ships the result by
hand.

---

## Operational follow-ups

- [ ] Set `server.trusted_proxy_hops: 1` in Railway's `/data/config.yaml`.
- [ ] Reset Railway's `/data/config.yaml` to the current shape **before the
      next deploy**. The sweep removed the `scheduler`, `embeddings`, and
      `implementation_sessions` sections, and `calendar` is now
      `default_calendar` only — no `enabled`, no `timezone`. The loader uses
      `KnownFields(true)`, so a stale key fails startup rather than being
      ignored.

---

## Standing constraints

### Architecture

- `internal/kernel` and `internal/ports` remain provider-neutral.
- Adapters live under `plugins/<category>/<provider>` and are wired only in
  `internal/bootstrap`.
- `internal/kernel/services/repo` may import `internal/kernel/services`; never
  the reverse.
- The loop has exactly one tool source. Dynamic adapter catalogs are providers
  on the kernel registry, never a second loop or registry.
- Production remains a single `eggyd` process and one replica.

### Do not introduce

- a runtime-loaded native plugin system;
- a DI, agent, web, or ORM framework;
- build tags for ordinary feature selection;
- package splits that export internals only to satisfy directory boundaries;
- generic feature/module lifecycle interfaces whose only caller is bootstrap;
- a second way to do a job that already has one.

### Safety

- Telegram keeps webhook authentication, owner allowlisting, and update
  deduplication.
- Calendar mutations retain independent payload-bound approvals. Consolidating
  the calendar tools never consolidates their approvals.
- Generic Telegram selections can never satisfy an approval, authorize a
  mutation, or be read as approve/reject.
- Eggy has no repository commit, push, pull-request, or merge capability. If
  any returns, independent payload-bound approvals and protected-branch denial
  are mandatory.
- Unprompted turns cannot use MCP or mutate anything.
- Durable context retains active-secret filtering.
- Config and owner-facing files retain locking and atomic writes.
- Configured repositories and stdio MCP servers are trusted code running as
  Eggy's user; timeouts and environment allowlists are not a sandbox.

### Process

- Change behavior test-first and run the focused test before the full suite.
- Run `make fmt vet test race build` before completing each phase.
- Run `make smoke` when Docker is available; report an unavailable Docker
  daemon as an environment blocker, not a passing smoke test.
- Every feature proposal states its deletion budget: production lines, config
  keys, tools, durable records, background loops.
