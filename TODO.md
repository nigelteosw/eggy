# Eggy roadmap

Unfinished work and standing constraints only. Completed work lives in git;
current behavior lives in `README.md` and the docs site under
`docs/src/content/docs/`. Delete an item once it lands.

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
settings, read-only repository inspection, MCP servers, schedules.

**Extension mechanism:** MCP for capabilities, Markdown skills for procedure.
A native adapter is otherwise only justified when a port must stay
provider-neutral (models, channels, storage), or when a capability needs
per-call authorization that MCP structurally cannot give it. New product
features do not get compiled in.

**There is no native product adapter left.** Calendar was the one exemption,
justified by payload-bound approvals an MCP server cannot express; it was
deleted on 2026-07-31 in favour of a configured Google Calendar MCP server.
The consequence is explicit: calendar mutations now carry no per-call
approval, because a configured MCP server is trusted wholesale. Approval-gated
MCP tools (P2 below) is what would close that gap. Anything asking to be
compiled in again must make the argument Calendar no longer gets to make.

## Measured baseline (2026-07-31, Calendar deleted)

Counted as non-test `.go` outside `docs/` and `website/`:

- 13,201 production Go lines and 11,574 test lines (2026-07-31, after the MCP
  administration work);
- largest packages: `plugins/tools/mcp` 1,526, `internal/bootstrap` 1,381,
  `internal/config` 1,343, `internal/kernel/services` 1,105, `internal/web`
  872, and `internal/commands` 427;
- 64 YAML-tagged fields in `internal/config/config.go`;
- machine-managed persistence still spans `state.json`, `auth.json`, `cron/`,
  and `eggy.db`; Markdown context and skills remain files by design.

Tool counts and HTTP route registrations are not re-counted here; that is the
P2 re-measurement below, and it should be done once rather than guessed at
twice.

## Targets for this pass

- **≤ 12,500 production Go lines** excluding generated web assets — currently
  over, at 13,201;
- **≤ 6 tools** in the default core; **≤ 13** with Telegram and repositories
  configured; MCP counted separately;
- **three durable forms**: YAML for startup config, Markdown for owner-facing
  documents, SQLite for everything machine-managed;
- **≤ 50 config fields**;
- **one** runtime administration *authority* — every config write goes through
  `internal/config` under one lock; Telegram and web are views onto it, not
  separate implementations.

---

## P1: Make the config surface the product surface

"Configurable" is the whole design claim, so the config must be small, flat,
and validated in one place.

- [ ] Collapse `internal/config` (now 1,343 lines, 64 YAML-tagged fields) to
      the sections that survive: `server`, `data_dir`, `agent`, `providers`,
      `models`, `telegram`, `repositories`, `runner`, `mcp`. Delete
      `commonConfigDocument`/`configDocument` duality once no legacy shape
      needs reading.
- [ ] Reject unknown keys with a message naming the key and the nearest valid
      one. A silently-ignored key is how a "configurable" system becomes a
      guessing game.
- [ ] Make every optional section's absence the off switch. No `enabled: true`
      field where an empty section already means "not configured". The one
      remaining offender is `mcp.servers.<name>.enabled` — decide whether a
      listed-but-disabled server is worth a field or whether removing the entry
      is the off switch.
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
      that owns them, `internal/bootstrap/assistant_tools.go` included.
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

- [ ] Re-measure the per-turn context floor now that Calendar's tools and
      prompt text are gone, and record it once here alongside the tool count
      and HTTP route count the baseline above deliberately left open. Tool
      schemas were the largest section by a wide margin; that is the number
      that matters, not prose bytes.
- [ ] One home per policy fact: tool descriptions explain invocation, runtime
      policy explains cross-tool constraints. Delete the duplicate.
- [ ] Report MCP schema bytes separately from kernel schema bytes. Do not
      build deferred tool loading until MCP schemas alone exceed ~10K tokens.
- [ ] Reject duplicate, secret-like, prompt-injection, exfiltration, and
      invisible-Unicode content before durable context writes.
- [ ] Do not describe lexical workspace containment as a sandbox. If isolation
      is needed for repository or stdio-MCP subprocesses, it is a container.
- [ ] Build **approval-gated MCP tools**: a per-server
      `require_approval: [tool names]` list that routes a matching call through
      the existing approval flow, rendering the tool's arguments as the bound
      payload. With Calendar deleted this is no longer hypothetical — it is the
      only way a configured calendar (or any other mutating server) regains
      per-call authorization instead of being trusted wholesale. Cost is a
      payload presenter for arbitrary MCP arguments, and typed handling
      (timezones, relative ranges) is not coming back.

---

## P2: Documentation and test weight

- [ ] `README.md` becomes a short operator guide: install, configure, run.
      `docs/src/content/docs/project/architecture.md` is the only architectural
      narrative; ADRs hold durable trade-offs.
- [ ] Delete comments that narrate removed implementations or rejected
      alternatives. Keep comments stating a non-obvious invariant, an exported
      contract, or a security reason.
- [ ] Delete tests for removed behavior in the same change as the behavior. Do
      not chase a test-line target; keep focused safety and adapter contract
      tests even when they exceed the implementation.
- [ ] Audit `AGENTS.md`, `README.md`, the docs site, and `config.example.yaml`
      after every phase. `docs/ARCHITECTURE.md` no longer exists; grep for
      stale references to it when touching docs.

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

## Open decision: how Eggy reaches Google

Two ways in, and the Google Cloud console is on both. Neither avoids creating
a project, enabling APIs, configuring a consent screen, and issuing client
credentials — that cost is fixed. What differs is which client type, what has
to be reachable, and how many grants there are.

**Where it stands (2026-07-31):** the MCP path is built and committed. A
Google remote MCP server per product, a web OAuth client, and the callback at
`/auth/mcp/{server}/callback`, with a pasted-redirect fallback for when the
browser cannot deliver to it.

**Why the Hermes method works.** Hermes ships no MCP client for this; its
`google-workspace` skill is a CLI holding one token and calling Google's REST
APIs directly. Four properties do the work:

1. **A desktop client, not a web one.** Google allows loopback redirects on
   any port for desktop clients without registering them, so the flow needs no
   public address and no redirect entry per server. Eggy's web client must
   send the registered callback in both the authorization request and the
   exchange; the pasted-redirect path removes the need for the browser to
   *deliver* to that callback, not the need to have registered it.
2. **One grant, one token, every product.** Gmail, Calendar, Drive, Docs,
   Sheets, and Contacts come from a single consent against a single client.
   The MCP path is one server, one record, and one consent screen per product.
3. **No dependency on Google hosting an MCP server.** Verified live: Calendar,
   Gmail, Drive, Docs, Sheets, and Chat serve
   `https://<product>mcp.googleapis.com/mcp/v1`. Contacts/People serves
   nothing, so Hermes' contacts coverage has no MCP equivalent at all.
4. **No inbound HTTP in the auth path.** The redirect is pasted back, so
   authorization never needs `server.public_base_url` to be right or reachable.

**What it would cost Eggy.** A native Google adapter under
`plugins/tools/google` — the shape just deleted for Calendar, now larger by
five products, with its own tool schemas back in the per-turn budget and its
own token store beside the MCP one. That is a second way to do a job that has
one, which the standing constraints forbid. Do not start it to work around a
single broken login.

**Decide it on this, not on setup friction:**

- [ ] Establish whether the consent screen is External/Testing. If it is,
      refresh tokens expire after seven days and *both* paths break weekly on
      a daemon that is supposed to be always-on. Publishing the app, or an
      Internal client on a Workspace account, is the fix — and it is the same
      fix either way, so it cannot be an argument for switching.
- [ ] Confirm the MCP path actually authorizes end to end once the readable
      exchange error is deployed. A path that has never once succeeded is not
      evidence against itself.
- [ ] Only if it cannot: weigh a desktop-client Google adapter against the
      per-product MCP servers, and record the reason here. Contacts is the
      only capability MCP cannot reach at all.

---

## Operational follow-ups

- [ ] Set `server.trusted_proxy_hops: 1` in Railway's `/data/config.yaml`.
- [ ] Reset Railway's `/data/config.yaml` to the current shape **before the
      next deploy**. The sweep removed the `scheduler`, `embeddings`,
      `implementation_sessions`, and `calendar` sections. The loader uses
      `KnownFields(true)`, so a stale key fails startup rather than being
      ignored — though `LoadOrCreateConfig` prunes these particular retired
      sections in place first, carrying `calendar.timezone` over to
      `agent.timezone`.
- [ ] Configure the Google Calendar MCP server to replace the deleted native
      adapter. Google implements no dynamic client registration, so the client
      is the hand-registered one and the entry belongs in `/data/config.yaml`
      rather than in `/mcp add`, which has no `oauth_scopes` field and would
      otherwise ask consent for all twelve advertised calendar scopes:

      ```yaml
      calendar:
        url: "https://calendarmcp.googleapis.com/mcp/v1"
        transport: "streamable-http"
        auth: "oauth"
        oauth_client_id: "<web client id>"
        oauth_client_secret_env: "GOOGLE_CLIENT_SECRET"
        oauth_scopes: ["https://www.googleapis.com/auth/calendar"]
        enabled: true
      ```

      Then restart and run `/mcp login calendar`. Scopes are re-consented, not
      widened in place, so set them before the first login.
- [ ] Keep `GOOGLE_CLIENT_SECRET` in Railway's environment: with the calendar
      server configured, `oauth_client_secret_env` reads it and startup fails
      when it is empty. `GOOGLE_CLIENT_ID` is still unread — the client id
      lives in `config.yaml`, not the environment — and can go.

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
- Any protected mutation retains an independent payload-bound approval, with
  one `approvals.Action` and one executor per operation. Consolidating tools
  never consolidates their approvals.
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
