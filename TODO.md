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
- [ ] `NewApp` is ~255 lines and is the last straight-line stretch left. The
      obvious extraction — lifting tool registration into `buildToolRegistry` —
      was tried and rejected: it needs eleven collaborators, so it trades a long
      function for an eleven-parameter one. Do it only after the items above
      shrink what a turn's tool set is assembled from; extracting it first just
      moves the coupling somewhere it is harder to see. The Telegram wiring
      (client, channel, selector, webhook, command registration) is the one
      genuinely separable piece, and it is scattered across four points in the
      function.

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

## Decided: Google Workspace is a native adapter, not MCP

Settled 2026-07-31 after the MCP path could not be authorized, and shipped:
`plugins/tools/google` speaks Google's REST APIs from Go. Hermes' *auth model*
was adopted; its *vehicle* was not, because Hermes is Python only because
Hermes is a Python skill host.

Why the auth model works, and how it differs from the MCP path that stays in
the tree for everything else:

| | MCP servers | `plugins/tools/google` |
|---|---|---|
| OAuth client type | Web application | Desktop (installed app) |
| Redirect | `{public_base_url}/auth/mcp/{server}/callback`, registered exactly | `http://localhost:1`, no entry needed — loopback matching ignores the port |
| Needs a public address | Yes | No |
| Completing a login | Browser delivers to the callback route; pasted redirect as fallback | Paste only — nothing listens on the redirect by design |
| Grants | One server, one record, one consent screen per product | One grant, one record, every product |
| Contacts | Unreachable — Google hosts no People MCP server | Covered |
| Tool schemas | Whatever each server publishes | One per configured product; unlisted products cost nothing |
| Token at rest | Sealed in `auth.json`, bound to server name and URL | Sealed in `auth.json` under `google` |

A web client *can* register `http://localhost:1` — Google exempts localhost
from its HTTPS rule — but the match is exact including the port, which makes
`LoopbackRedirect` load-bearing configuration in a console. That is the
remaining reason to prefer a desktop client, and the only one.

Carried over from Hermes exactly: the dead loopback redirect, paste either a
full redirect URL or a bare code, check state only when the paste carried one,
`access_type=offline` with `prompt=consent` so a refresh token is really
issued, and store the scopes the grant reports rather than the ones requested.

Not copied: Python and Google's client libraries in the image, a plaintext
token file, and the shell tool their `$GAPI` invocation depends on. The bounded
shell remains undecided above and this never needed it.

**Remaining:**

- [ ] Establish whether the consent screen is External/Testing. If it is,
      refresh tokens expire after seven days and the grant dies weekly. An
      Internal client on a Workspace account, or a published app, is the fix.
- [ ] Decide whether `Endpoints` stays in-package. It is settable only by
      tests today and must not become config: an operator-settable API host is
      a credential exfiltration primitive, not a feature.

---

## P1: One auth surface

The boundaries in `AGENTS.md` hold where they are checked: `internal/kernel`
and `internal/ports` import no adapter, and the `config <- web <- bootstrap`
direction is intact. Auth is where they are not checked, and it has spread
into five packages with no owner:

- **outbound authorization** — `plugins/tools/google/oauth.go` and
  `plugins/tools/mcp/oauth.go` are two implementations of authorization-code +
  PKCE. Both generate a state and a verifier, both bound a pending login to a
  ten-minute window, both send `access_type=offline` with `prompt=consent` for
  the same documented reason, both exchange, both persist a refreshed token,
  both record granted rather than requested scopes;
- **the same interface, twice** — `commands.GoogleRuntime` and
  `commands.MCPRuntime` are `BeginLogin`/`CompleteLogin`/`Logout`/status,
  differing only in that MCP keys every call by server name;
- **completion is already shared and already homeless** —
  `internal/commands/oauth_paste.go` says so in its own header: it lives there
  "because neither owns it";
- **owner authentication is somewhere else again** — `plugins/webui` serves the
  asset bundle *and* holds `SignSession`, `VerifySession`, and the login
  throttle, and `internal/web` reaches in for them. An HTTP surface doing its
  own session crypto out of a package named for a JavaScript bundle is the
  clearest case of no owner at all;
- **storage is the one part that is already right** — `plugins/auth/authfile`
  owns the container, and the sealed-record envelope is now one implementation
  there rather than one per provider.

### The shape to build

- [ ] Add one provider-neutral port for a grant — `BeginLogin`,
      `CompleteLogin`, `Status`, `Logout` — and let `GoogleRuntime` and
      `MCPRuntime` collapse into it. `internal/commands` and `internal/web`
      then speak to grants by name (`google`, `mcp:railway`) instead of
      carrying one interface and one command path per provider.
- [ ] Move the authorization-code + PKCE flow into a single adapter under
      `plugins/auth/`, and make Google and MCP *configuration* of it rather
      than two implementations: fixed endpoints plus a loopback redirect for
      Google, discovery plus optional dynamic registration for MCP. The
      per-provider differences are real but they are parameters, not flows.
- [ ] Move `parseOAuthRedirect` and `googleAuthError` out of
      `internal/commands` into that adapter. Pasted-redirect completion is part
      of the flow, not part of the command surface that happens to call it.
- [ ] Keep **owner authentication separate from outbound authorization.** They
      point in opposite directions of trust — who may talk to Eggy, versus what
      Eggy may do on the owner's behalf — and merging them produces one
      security god-object rather than one surface. What should move is the
      session crypto: `SignSession`, `VerifySession`, and `LoginThrottle` leave
      `plugins/webui` (which should serve assets and nothing else) for an owner
      -authentication adapter that Telegram's webhook-secret and owner-allowlist
      checks can also live behind.
- [ ] Preserve every property the current code documents while moving it: forced
      consent to guarantee a refresh token, granted-scope recording, the pending
      window, the loopback redirect matching byte for byte, and per-record
      associated data binding a grant to the server it was issued for. These are
      the lessons the comments exist to keep; a rewrite that drops one is a
      regression the tests will not all catch.

Success criteria: one flow implementation, one grant port, one place that
knows what a pasted redirect is, and `plugins/webui` importable for assets
alone. Adding the next OAuth provider is a config entry, not a third copy.

---

## Code-smell sweep (2026-07-31): what is left

A read of the whole tree for duplication and drift. The fixes that landed are
in git; these are the findings that did **not** get fixed, with the reason.

- [ ] `plugins/webui/dist/index.html` is force-tracked out of an otherwise
      ignored `dist/`, and it references content-hashed assets
      (`/assets/index-<hash>.js`) that are **not** tracked. So a fresh clone
      serves a page pointing at files that do not exist, and every `make build`
      rewrites the hashes and dirties the tree. Decide one way: track the built
      assets too, or track neither and build the UI as a release step. The
      current half-measure gives the worst of both.
- [ ] 23 non-test `sort.Strings`/`sort.Slice` calls predate the Go 1.26
      baseline and read as `slices.Sort`/`slices.SortFunc` now. Pure
      modernization with no behavior change — worth doing in one pass when a
      change already touches those files, not as its own churn commit.
- [ ] `knownGoogleProducts` in `internal/config` duplicates the adapter's
      product list, and the boundary rule is what forces it: `internal/config`
      may not import a plugin package. Left as is and left commented. If a
      third copy ever appears, that is the signal to invert it — let the
      adapter validate its own product names at construction and have config
      check only the shape.

Two of the fixes that landed were defects rather than style, and are recorded
here only so the invariants are not re-broken:

- Google product names are canonicalized once, in `config.applyDefaults`.
  Validation accepts any casing, so when scope selection matched exactly and
  the adapter matched case-insensitively, `products: ["Gmail"]` registered the
  tool and requested no scope — every call 403'd, reading as a broken API.
- `Secrets.Values()` is the only list of live credentials. Bootstrap used to
  build a second one for the durable-context secret guard, and it had drifted:
  the Google client secret and the MCP OAuth client secrets were absent, so
  those two alone could be written into `MEMORY.md` and recall unmasked. A
  reflection test now fails when a field is added to `Secrets` but not to
  `Values`.

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
- [ ] Remove the `calendar` MCP server from Railway's `/data/config.yaml`
      (`/mcp remove calendar`, then restart). It never authorized, its tools
      answer with permission errors beside the working ones, and two live
      routes to one calendar is the second way to do a job the constraints
      below forbid.
- [ ] Keep `GOOGLE_CLIENT_SECRET` in Railway's environment — `google.enabled`
      reads it through `client_secret_env` and startup fails when it is empty
      — and make sure it is the **Desktop** client's secret, not the web
      client's. `GOOGLE_CLIENT_ID` is unread: the client id lives in
      `config.yaml`.

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
