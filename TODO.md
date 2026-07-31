# Eggy cleanup plan

A plan for one thing: **separation of concerns and code cleanup.** Product
direction, new capabilities, and deployment chores are parked at the bottom
rather than mixed in here.

Unfinished work only. Completed work lives in git; current behavior lives in
`README.md` and `docs/src/content/docs/`. Delete an item once it lands.

The bar every item clears: **it deletes or moves code.** An item that only adds
code needs an argument for why the thing it prevents is worse than the thing it
costs. More code is harder to maintain, so a refactor that nets larger is a
refactor that failed.

## Design rules this plan enforces

- **A capability that is not configured costs nothing at runtime** — no tool
  schema, no goroutine, no store, no HTTP route, no prompt bytes.
- **One way to do a job.** A second implementation of something that already
  exists is the defect this plan exists to find.
- **Boundaries are one-way.** `internal/kernel` and `internal/ports` stay
  provider-neutral; adapters live under `plugins/<category>/<provider>` and are
  wired only in `internal/bootstrap`; the direction is
  `config <- web <- bootstrap`.

## Baseline (re-measured 2026-07-31, after the bootstrap pass)

Non-test `.go` outside `docs/` and `website/`:

- **15,755 production lines**, 13,184 test lines;
- largest packages: `plugins/tools/mcp` 1,634, `internal/config` 1,561,
  `internal/bootstrap` 1,469, `internal/kernel/services` 1,260, `internal/web`
  911, `internal/commands` 770;
- **75 YAML-tagged fields** in `internal/config/config.go`;
- machine-managed persistence spans `state.json`, `auth.json`, `cron/`, and
  `eggy.db`; Markdown context and skills remain files by design.

Bootstrap fell 1,524 → 1,469 while `internal/kernel/services` rose 1,097 →
1,260: the tools moved rather than vanished, which is what that pass was for.
Production lines still rose overall (15,622 → 15,755), because every pass so
far has traded scattered code for named code plus the comments explaining it.
Re-measure before claiming progress against the targets, not from memory.

## Targets

- **≤ 12,500 production lines** excluding generated web assets — currently
  15,755, and moving *away*. Three cleanup passes have each netted slightly
  larger, because moving code to where it belongs costs the naming and the
  comments. This target is not reachable by cleanup: it needs deleting a
  capability. Decide whether the target or the scope moves.
- **≤ 50 config fields** — currently 75.
- **three durable forms**: YAML for startup config, Markdown for owner-facing
  documents, SQLite for everything machine-managed.
- **one runtime administration authority** — every config write goes through
  `internal/config` under one lock; Telegram and web are views onto it.

---

## P1: Collapse the config surface

75 YAML fields across a 1,526-line package. The document duality is gone;
what is left of this section is a *scope* question, not a cleanup one.

- [ ] **The ≤ 50 field target needs a capability decision, not tidying.** The
      75 fields are not redundant: 20+ are MCP server options (transport, auth,
      OAuth client, timeouts, failure policy, tool filters), 7 are Google, and
      the rest are server, runner, and repository settings that each do
      something. Nothing here is a second way to do a job. Reaching 50 means
      removing a capability or accepting fewer knobs on MCP — decide which,
      then this becomes actionable.

Three items from this section were investigated and **closed without work**;
see "Decided" below for `enabled` flags, the unknown-key message, and
`config.example.yaml` coverage.

---

## P2: Mechanical cleanups

- [ ] `plugins/webui/dist/index.html` is force-tracked out of an otherwise
      ignored `dist/`, and references content-hashed assets
      (`/assets/index-<hash>.js`) that are **not** tracked. A fresh clone serves
      a page pointing at files that do not exist, and every `make build`
      rewrites the hashes and dirties the tree. Track the built assets too, or
      track neither and build the UI as a release step. The half-measure gives
      the worst of both.
- [ ] 23 non-test `sort.Strings`/`sort.Slice` calls predate the Go 1.26
      baseline and read as `slices.Sort`/`slices.SortFunc`. Do it when a change
      already touches those files, not as its own churn commit.
- [ ] Add two characterization tests to `plugins/tools/google`: that
      `access_type=offline` + `prompt=consent` reach the authorization URL, and
      that a mismatched state is rejected. MCP pins both; Google pins neither,
      and both failures are silent — a grant that stops renewing weeks later.
- [ ] Decide whether `Endpoints` stays in-package in `plugins/tools/google`. It
      is settable only by tests today and must never become config: an
      operator-settable API host is a credential exfiltration primitive.

---

## Decided — do not re-propose

**The two OAuth flows stay separate.** Unifying `plugins/tools/google/oauth.go`
and `plugins/tools/mcp/oauth.go` behind one parameterized adapter was
considered and rejected. Shared: state and verifier generation, the pending
window check, the exchange, token persistence — 50-60 lines. Not shared, and
structural, all on the MCP side: protected-resource discovery,
authorization-server metadata with trailing-slash tolerance and synthesized
fallback endpoints, RFC 7591 dynamic registration, a `resource` parameter
threaded through every call, per-server keying, and conformance to the MCP
SDK's `auth.OAuthHandler`. Google has fixed endpoints and one loopback
constant. One adapter would carry all of that past a provider that never uses
it, adding branches to delete ~50 lines. Revisit only if a *third* provider
appears on the MCP side of the split.

**`GoogleRuntime` and `MCPRuntime` stay separate.** Four lines each; collapsing
them needs a server key Google ignores or a named-grant registry. Machinery to
delete eight lines.

**`parseOAuthRedirect` stays in `internal/commands`.** Its header calls itself
homeless, but both callers *are* the command surface, and moving it would have
two plugin packages import a third.

**Owner authentication stays separate from outbound authorization.**
`plugins/auth/session` answers "who may talk to Eggy"; the OAuth grants under
`plugins/tools/*` answer "what may Eggy do on the owner's behalf". They point
in opposite directions of trust, and one "auth" package owning both is a
security god-object. Telegram's webhook-secret and owner-allowlist checks are
the only things that may later join `session`.

**Tool registration stays inline in `NewApp`.** Extracting a
`buildToolRegistry` helper was tried and rejected: it needs eleven
collaborators, so it trades a long function for an eleven-parameter one and
hides the coupling instead of removing it. It becomes worth revisiting only if
what a turn's tool set is assembled from gets smaller.

**Adding a model backend is usually config, not code.** `openai_compatible` is
OpenAI's Chat Completions wire format — `/chat/completions`, `tools` /
`tool_calls`, `reasoning_effort`, `usage.cached_tokens` — so OpenAI itself,
DeepSeek, OpenRouter, and Groq are all reachable by adding a `providers` entry
with the right `base_url` and `api_key_env`. Writing an `openai` adapter that
duplicates `openaicompat` is the wrong turn, and a test pins that. A new
adapter is earned by a genuinely different wire format: Anthropic's Messages
API, with its top-level system prompt, content blocks, and required
`max_tokens`, is the real example. That costs a plugin package, a name in
`config.supportedModelAdapters`, and one case in `bootstrap.newModelAdapter`.

**`enabled` flags stay on MCP servers and Google.** The rule "an empty section
is the off switch" does not hold when the section carries state that is
expensive to recreate. An MCP server entry holds a URL, auth mode, OAuth client
id, timeouts, and tool filters; Google holds a client id and product list.
`enabled: false` turns the capability off while keeping all of it, which
deleting the section does not. The MCP manager also reads `Enabled` at runtime
to gate reconnection, so the field is load-bearing rather than declarative.
Removing it would break every existing config (`KnownFields(true)` rejects the
now-unknown key) and cost migration code to fix.

**The unknown-key message is already a repair.** `KnownFields(true)` reports
the line, the key, and the section type: `line 34: field enabld not found in
type config.GoogleConfig`. Adding a "did you mean" suggestion needs edit
distance over yaml tags plus mapping a type name in an error string back to a
`reflect.Type` — real machinery for a message that already names the file
position and the exact key.

**`config.example.yaml` stays a starter, not a reference.** Asserting it
documents all 75 fields would force the advanced MCP options, tool filters, and
failure policy into a file whose job is to get someone running. Field reference
belongs on the docs site. `TestLoadConfigAcceptsExample` already pins that the
example parses and produces the expected values, which is the property worth
holding.

**`knownGoogleProducts` stays duplicated in `internal/config`.** The boundary
rule forces it: config may not import a plugin package. If a third copy
appears, invert it — let the adapter validate its own product names and have
config check only the shape.

### Invariants that cost a defect to learn

- Google product names are canonicalized once, in `config.applyDefaults`.
  Validation accepts any casing, so when scope selection matched exactly and
  the adapter matched case-insensitively, `products: ["Gmail"]` registered the
  tool and requested no scope — every call 403'd, reading as a broken API.
- `Secrets.Values()` is the only list of live credentials. Bootstrap built a
  second one for the durable-context secret guard and it drifted: the Google
  client secret and the MCP OAuth client secrets were absent, so those two
  alone could reach `MEMORY.md` and recall unmasked. A reflection test now
  fails when a field is added to `Secrets` but not to `Values`.
- Either OAuth flow must keep: forced consent so a refresh token is really
  issued, granted-scope recording, the pending window, the loopback redirect
  matching byte for byte, and per-record associated data binding a grant to the
  server it was issued for.

---

## Not this pass

Real work, tracked so it is not lost, but not separation of concerns or
cleanup. Each adds capability or is a deployment chore.

- **Consolidate durable state in SQLite** — move approvals, processed event
  IDs, selected model, and schedules out of `state.json`/`cron/` behind the
  existing ports; keep the Markdown files as files. Needs a schema-versioned,
  retry-safe migration design written first, and preserves encrypted payloads.
  The largest net-new-code item on the list, which is why it is parked.
- **Approval-gated MCP tools** — a per-server `require_approval: [tool names]`
  list routing a matching call through the existing approval flow. This is the
  open safety gap left by deleting Calendar: a configured MCP server is trusted
  wholesale, so calendar mutations now carry no per-call approval.
- **Does Eggy write code?** — undecided between read-only inspection and a
  bounded edit tool plus bounded shell. Both defensible, not compatible. Until
  decided, do not reintroduce write tools opportunistically.
- **Prompt and tool budget** — re-measure the per-turn context floor, report
  MCP schema bytes separately from kernel schema bytes, and do not build
  deferred tool loading until MCP schemas alone exceed ~10K tokens.
- **Docs pass** — `README.md` becomes a short operator guide;
  `docs/src/content/docs/project/architecture.md` is the only architectural
  narrative. Audit `AGENTS.md`, the docs site, and `config.example.yaml` after
  every phase.
- **Railway operations** — set `server.trusted_proxy_hops: 1`; reset
  `/data/config.yaml` to the current shape before the next deploy (retired
  `scheduler`, `embeddings`, `implementation_sessions`, `calendar` sections);
  remove the `calendar` MCP server, which never authorized; keep
  `GOOGLE_CLIENT_SECRET` set to the **Desktop** client's secret.

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
