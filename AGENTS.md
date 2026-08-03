# Eggy development guidance

Eggy is a Go 1.26 ports-and-adapters modular monolith.

This file holds the rules that outlive any one change: boundaries, safety
invariants, decisions already argued, and capabilities deliberately declined.
`TODO.md` holds unfinished work only. If something here stops being true, change
it here — do not restate it in `TODO.md`.

## Design rules

- **A capability that is not configured costs nothing at runtime** — no tool
  schema, no goroutine, no store, no HTTP route, no prompt bytes.
- **One way to do a job.** A second implementation of something that already
  exists is a defect, not a feature.
- **The footprint ladder.** Before a capability becomes a core tool, try, in
  order: extend existing code; a command plus a skill; a tool gated off when
  unconfigured; an MCP server. Core tool is last, because every core tool ships
  its schema on every API call for every owner, including ones who will never
  use it.
- **Every feature proposal states its deletion budget**: production lines,
  config keys, tools, durable records, background loops. A refactor that nets
  larger is a refactor that failed.
- **Three durable forms, and only three**: YAML for startup config, Markdown for
  owner-facing documents, SQLite for everything machine-managed. A fourth needs
  an argument, not a use case.
- **One runtime administration authority.** Every config write goes through
  `internal/config` under one file lock with the same validation; Telegram and
  the web panel are views onto it, never second implementations of it.
- Production remains a single `eggyd` process and one replica.

## Boundaries

- `internal/kernel/services` is the base kernel-service package;
  `internal/kernel/services/repo` holds read-only repository and workspace
  inspection. The dependency is one-way — `repo` may
  import `services`, never the reverse — so anything `repo` needs from the base
  package must be exported there (see `services.DecodeToolInput`). Test fakes
  are duplicated across the two rather than shared through an exported
  package: a fake is not API.
- Keep `internal/kernel` and `internal/ports` provider-neutral. They must not import Telegram, DeepSeek, Codex, GitHub, YAML, JSON-file persistence, Docker, or Railway packages.
- Provider request/response types and credentials stay inside their adapter packages.
- Register adapters and tools only through `internal/bootstrap`. Bootstrap is the composition root and nothing else: it wires adapters into services and owns the event loop. Config parsing and mutation belong in `internal/config`, the direct Telegram commands in `internal/commands`, and the HTTP surface in `internal/web`. The dependency direction is one-way — `config` <- `web` <- `bootstrap` — so neither config nor web may import `internal/bootstrap`.
- Treat configured repositories as trusted, but keep path, environment, timeout, output, and process-group restrictions intact.
- Never reintroduce repository mutation or shipping through a generic tool or Telegram selection. Any future protected mutation must use an independent approval check.
- Any protected mutation keeps one `approvals.Action`, one executor, and one payload-bound approval per operation; consolidating tools is fine, consolidating their approvals is not. `services.ApprovalToolCall` is the mechanism's one registered action: a tool named under an MCP server's `require_approval` is wrapped by `services.NewApprovalGatedTool` at the provider boundary in bootstrap, and `services.ApprovalToolExecutor` authorizes and runs it. A server without `require_approval` is still trusted wholesale at configuration time, which is the default.
- Restarting is a request, not an exit. `App.Restart` makes `App.Run` return `bootstrap.ErrRestart`, and `cmd/eggyd`'s supervisor loop — the same one safe mode uses — builds a fresh App from the current `config.yaml`. Run must keep draining in-flight turns rather than cancelling them, because the turn that asked for the restart still owes its reply; and `commands.Restart` must keep loading the config before signalling, so a bad file is refused instead of landing the owner in safe mode. `/restart` and `POST /api/restart` both go through that one function; a second restart path that skipped the pre-flight would be a way to break Eggy the other surface refuses.
- `/auto` (`state.ApprovalAutoMode`) disables every gate at once. It is a deliberate, durable bypass reported by `/status`; it must never become the default, and nothing may enable it on the owner's behalf.

## Adding a new adapter (open for extension, closed for modification)

A new provider (model backend, chat channel, repository host, runner, etc.)
should only ever add a new package under
`plugins/<category>/<provider>/` plus a wiring change in
`internal/bootstrap`. It should never require changing `internal/kernel`,
`internal/ports`, or an existing adapter package.

1. Find the port(s) your provider must satisfy in `internal/ports/ports.go`
   (`Model`, `Channel`, `ContextStore`, `StateStore`, `Scheduler`, `Runner`,
   `RepositoryCheckout`, `RepositoryReader`, `Tool`, ...).
   Do not change the
   interface's method signatures to fit one new provider — every existing
   adapter implements them and would break.
2. If the capability is genuinely new (no existing port fits), add a small,
   narrowly-scoped interface to `ports.go` rather than widening an existing
   one, and keep it provider-neutral (no provider-specific types, no
   credentials in the signature).
3. Implement the interface in the new adapter package. Keep that provider's
   wire types, HTTP/CLI calls, and credentials entirely inside the package —
   `internal/kernel` and `internal/ports` must never import it.
4. Wire construction only in `internal/bootstrap` (`app.go`'s `NewApp` for
   constructing the adapter and handing it to the relevant kernel service
   constructor, or `registry.Register` for a new `Tool`). This is the one
   place allowed to know every adapter exists. New config or secret fields go
   in `internal/config` (`config.go`), not in bootstrap.
5. Prefer branching on an existing selector over hardcoding one adapter. Two
   `ProviderConfig.Adapter` (`internal/config/config.go`) is in the config
   for exactly this: it is meant to pick a model adapter per provider instead
   of `app.go` always calling `openaicompat.New`. Route new provider kinds
   through that switch rather than adding another special case.
6. Add adapter-level tests against a fake HTTP server or fake subprocess in
   the new package, plus a `FakeAdapters`-mode path in `app.go` if the
   adapter needs one for `make smoke`/integration tests.

## Workflow

- Add or change behavior test-first and run the focused test before the full suite.
- Prefer the standard library and small interfaces. Do not introduce a web framework, ORM, DI framework, agent framework, native plugin runtime, or database.
- Run `make fmt vet test race build` before completing a change. Run `make smoke` when Docker is available; report an unavailable Docker daemon as an environment blocker, not a passing smoke test.
- Preserve `/data/state.json` schema compatibility or introduce an explicit migration and schema-version change.

## Do not introduce

- a runtime-loaded native plugin system;
- a DI, agent, web, or ORM framework;
- build tags for ordinary feature selection;
- package splits that export internals only to satisfy directory boundaries;
- generic feature/module lifecycle interfaces whose only caller is bootstrap;
- a second way to do a job that already has one.

## Safety invariants

- Telegram keeps webhook authentication, owner allowlisting, and update
  deduplication.
- Any protected mutation retains an independent payload-bound approval, with one
  `approvals.Action` and one executor per operation. Consolidating tools never
  consolidates their approvals.
- Generic Telegram selections can never satisfy an approval, authorize a
  mutation, or be read as approve/reject.
- Eggy has no repository commit, push, pull-request, or merge capability. If any
  returns, independent payload-bound approvals and protected-branch denial are
  mandatory, and none of it arrives as a side effect of another change.
- Unprompted turns cannot use MCP or mutate anything.
- Durable context retains active-secret filtering.
- Config and owner-facing files retain locking and atomic writes.
- Configured repositories and stdio MCP servers are trusted code running as
  Eggy's user; timeouts and environment allowlists are not a sandbox.

### Invariants that cost a defect to learn

- Google product names are canonicalized once, in `config.applyDefaults`.
  Validation accepts any casing, so when scope selection matched exactly and the
  adapter matched case-insensitively, `products: ["Gmail"]` registered the tool
  and requested no scope — every call 403'd, reading as a broken API.
- `Secrets.Values()` is the only list of live credentials. Bootstrap built a
  second one for the durable-context secret guard and it drifted: the Google
  client secret and the MCP OAuth client secrets were absent, so those two alone
  could reach `MEMORY.md` and recall unmasked. A reflection test now fails when a
  field is added to `Secrets` but not to `Values`.
- Either OAuth flow must keep: forced consent so a refresh token is really
  issued, granted-scope recording, the pending window, the loopback redirect
  matching byte for byte, and per-record associated data binding a grant to the
  server it was issued for.

## Decided — do not re-propose

**The two OAuth flows stay separate.** Unifying `plugins/tools/google/oauth.go`
and `plugins/tools/mcp/oauth.go` behind one parameterized adapter was considered
and rejected. Shared: state and verifier generation, the pending window check,
the exchange, token persistence — 50-60 lines. Not shared, and structural, all on
the MCP side: protected-resource discovery, authorization-server metadata with
trailing-slash tolerance and synthesized fallback endpoints, RFC 7591 dynamic
registration, a `resource` parameter threaded through every call, per-server
keying, and conformance to the MCP SDK's `auth.OAuthHandler`. Google has fixed
endpoints and one loopback constant. One adapter would carry all of that past a
provider that never uses it, adding branches to delete ~50 lines. Revisit only if
a *third* provider appears on the MCP side of the split.

**`GoogleRuntime` and `MCPRuntime` stay separate.** Four lines each; collapsing
them needs a server key Google ignores or a named-grant registry. Machinery to
delete eight lines.

**`parseOAuthRedirect` stays in `internal/commands`.** Its header calls itself
homeless, but both callers *are* the command surface, and moving it would have
two plugin packages import a third.

**Owner authentication stays separate from outbound authorization.**
`plugins/auth/session` answers "who may talk to Eggy"; the OAuth grants under
`plugins/tools/*` answer "what may Eggy do on the owner's behalf". They point in
opposite directions of trust, and one "auth" package owning both is a security
god-object. Telegram's webhook-secret and owner-allowlist checks are the only
things that may later join `session`.

**Tool registration stays inline in `NewApp`.** Extracting a `buildToolRegistry`
helper was tried and rejected: it needs eleven collaborators, so it trades a long
function for an eleven-parameter one and hides the coupling instead of removing
it. It becomes worth revisiting only if what a turn's tool set is assembled from
gets smaller.

**Adding a model backend is usually config, not code.** `openai_compatible` is
OpenAI's Chat Completions wire format — `/chat/completions`, `tools` /
`tool_calls`, `reasoning_effort`, `usage.cached_tokens` — so OpenAI itself,
DeepSeek, OpenRouter, and Groq are all reachable by adding a `providers` entry
with the right `base_url` and `api_key_env`. Writing an `openai` adapter that
duplicates `openaicompat` is the wrong turn, and a test pins that. A new adapter
is earned by a genuinely different wire format: Anthropic's Messages API, with
its top-level system prompt, content blocks, and required `max_tokens`, is the
real example.

**Config field count is not a cleanup target.** The fields are not redundant:
~20 are MCP server options (transport, auth, OAuth client, timeouts, failure
policy, tool filters), 7 are Google, and the rest are server, runner, and
repository settings that each do something. None is a second way to do a job.
Cutting the count means removing a capability or removing knobs from MCP — and
MCP's knobs are what let capability arrive as configuration instead of code,
which is the property the whole design is built on.

**Production line-count targets are retired.** A `≤ 12,500 production lines`
target counted the tree rather than what a turn costs to run, and it moved away:
successive cleanup passes each netted slightly larger, because moving code where
it belongs costs naming and comments. It was not reachable without deleting a
capability, which is a product decision to argue on its own merits rather than
smuggle in as a line-count exercise. Deleting genuinely dead code still counts;
shrinking to hit a number does not.

**`enabled` flags stay on MCP servers and Google.** The rule "an empty section is
the off switch" does not hold when the section carries state that is expensive to
recreate. An MCP server entry holds a URL, auth mode, OAuth client id, timeouts,
and tool filters; Google holds a client id and product list. `enabled: false`
turns the capability off while keeping all of it, which deleting the section does
not. The MCP manager also reads `Enabled` at runtime to gate reconnection, so the
field is load-bearing rather than declarative. Removing it would break every
existing config (`KnownFields(true)` rejects the now-unknown key) and cost
migration code to fix.

**The unknown-key message is already a repair.** `KnownFields(true)` reports the
line, the key, and the section type: `line 34: field enabld not found in type
config.GoogleConfig`. Adding a "did you mean" suggestion needs edit distance over
yaml tags plus mapping a type name in an error string back to a `reflect.Type` —
real machinery for a message that already names the file position and the exact
key.

**`config.example.yaml` stays a starter, not a reference.** Asserting it
documents every field would force the advanced MCP options, tool filters, and
failure policy into a file whose job is to get someone running. Field reference
belongs on the docs site. `TestLoadConfigAcceptsExample` already pins that the
example parses and produces the expected values, which is the property worth
holding.

**`knownGoogleProducts` stays duplicated in `internal/config`.** The boundary
rule forces it: config may not import a plugin package. If a third copy appears,
invert it — let the adapter validate its own product names and have config check
only the shape.

## Declined capabilities — do not re-propose

Each is something a comparable project (OpenClaw, Hermes Agent) has and Eggy will
not build. Recorded with the reason so re-reading their READMEs does not restart
the argument.

**Channel breadth.** Both front 10-20+ messaging platforms. Eggy has one owner,
reachable on Telegram and on the web. A tenth channel does not let the owner do
anything an existing one does not — it multiplies adapters, webhook
authentication, deduplication ledgers, and approval-delivery paths for zero new
capability. `ports.Channel` stays the extension point if that ever changes;
adding a channel should remain a plugin package plus one line of bootstrap, and
that property is worth more than any particular channel.

**Multi-agent routing and per-sender session isolation.** OpenClaw isolates
sessions per agent, workspace, and sender because it serves groups. Eggy is
single-owner by definition — every message is from the same person — so the
routing key has one value and the isolation protects nothing.

**Subagent delegation (`delegate_task`).** Hermes spawns isolated subagents with
spawn-depth and concurrency bounds, an orchestrator/leaf role split, and an
async-completion queue re-entering the parent conversation. That is an agent
framework, which "Do not introduce" forbids by name, and it needs a second loop,
which the boundary rules forbid by name. It is also the single largest net-new-
code item either reference has. If parallelism is ever needed, the answer is a
scheduled turn, not a child loop.

**A plugin marketplace or hub.** An on-demand installer for third-party skills
and plugins. Eggy's skills are reviewed files placed by the owner in `skills/`,
and that review is the security model. An installer that fetches agent-readable
instructions from the internet deletes it. Letting the *agent* write skills into
the owner's own directory is a different trust boundary from fetching a
stranger's.

**Runtime-loaded plugins.** Both discover plugins from directories and entry
points at runtime. Go's build model makes it worse than it is for Python or Node.
Compile-time wiring in `internal/bootstrap` plus MCP for out-of-tree capability
covers the same ground.

**A TUI, a desktop app, or companion nodes.** Each is a whole client to maintain.
Eggy's surfaces are a phone the owner already has and a browser, and production
stays a single `eggyd` process.

**Pluggable memory providers.** Hermes plugs several in behind an ABC — and its
own policy now refuses new in-tree ones, because the maintenance burden is what a
provider interface actually buys. Eggy has three durable forms on purpose — YAML
for startup config, Markdown for owner-facing documents, SQLite for everything
machine-managed. A memory provider port would be a second way to do a job that
SQLite and Markdown already do.

**Profiles / multi-instance isolation.** Eggy has one home, one owner, one
replica; `EGGY_CONFIG` already relocates it for tests and local runs.
