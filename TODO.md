# Eggy TODO

Unfinished work only; delete items when they land. Durable rules and settled
decisions live in `AGENTS.md`; current behavior belongs in `README.md` and the
docs site.

Commands and Telegram reviewed against the checkout on 2026-09-21; the
remaining performance backlog was last reviewed on 2026-09-06. Findings below
are from source inspection, not production measurements. S/M/L indicate
relative effort, not delivery promises. Line budgets are estimates to refine
before implementation.

## P2 — Make status actionable and bounded (S–M)

`CommandService.status` already reports model, mode, pending approval summaries,
and MCP readiness. Extend it rather than create a parallel diagnostics command.

- Add effective reasoning settings and a compact shared Google connection state
  using existing runtime reads. Show shared identity and a recovery command when
  authorization needs attention; omit unconfigured integration sections.
- Bound approval summaries and integration detail, with remaining counts and a
  `/web` pointer for full inspection. Treat expired pending entries consistently
  with the approval authority; do not add command-side state mutation.
- Tests: missing integration, failed status read, many approvals, expiry, stable
  ordering, account isolation, and consistent mode wording across web and chat.

Deletion budget: extend existing status; ~40–80 net production lines, 0 config
keys/tools/record types/loops.

## P2 — Finish Telegram onboarding and web handoff (S–M)

The webhook already consumes `/start <pairing-token>` for an unlinked private
sender, and `/web` already issues a single-use account-bound sign-in link.
Build on those paths.

- Add a useful bare `/start` for linked users: brief introduction, `/help`, and
  `/web`. After successful pairing, send a clear success message and next step;
  currently the pairing branch returns HTTP 204 without a chat reply.
- Keep unlinked users outside the harness except for token redemption. Verify
  consumed/expired tokens, repeated updates, already-linked users opening a
  pairing link, and conflicting identity bindings have deterministic outcomes.
- Ensure delivery failure after successful pairing cannot repeat the mutation.
  Reuse the pairing authority and update-deduplication machinery.
- Keep `/web` explicit and sender-verified. Do not mint login links from generic
  selection callbacks or put them into model context, durable memory, or logs.
- Correct Google setup guidance for the required `expected_email`: reuse the
  existing config mutation authority or direct users to the panel's identity
  setting, rather than leave `/google set` looking like a complete setup path.

Deletion budget: extend existing start/pairing/help paths; ~40–90 net production
lines, 0 config keys/tools/new durable record types/loops.

## P2 — Improve Telegram feedback and delivery resilience (M)

Typing indicators, message splitting, HTML fallback, image input, quoted replies,
approval buttons, and transient choices already exist. Improve their edges.

- Give expired or already-used selection buttons an actionable response instead
  of only clearing the spinner. Remove obsolete keyboards where existing message
  editing supports it; preserve ownership checks before revealing details.
- Respond clearly to unsupported attachments. The current normalizer rejects
  non-image documents and does not represent voice input; avoid silent empty
  turns. Distinguish permanent unsupported input from retryable download failure
  so webhook redelivery does not create repeated notices or model calls.
- Extend the Telegram API error decoder to retain structured rate-limit details.
  Verify current official Bot API behavior at implementation time, then add
  bounded, cancellation-aware waits for explicit retryable rejections. Do not
  blindly replay sends after ambiguous transport failures that may have delivered.
- Test formatting/splitting around long help, Unicode, code blocks and buttons,
  plus cancellation and API rejection using the existing fake HTTP server.

Deletion budget: replace generic error decoding and silent callback handling;
~80–150 net production lines, 0 config keys/tools/durable record types/background
loops. Reuse the current client and turn lifecycle.

## P2 — Measure remaining prompt and provider costs (S–M)

The old cache implementation tasks are stale: `plugins/models/openaicompat/model.go`
already sends OpenRouter `session_id` and ephemeral `cache_control` for Anthropic
model IDs. `agent/prompt.go` orders stable sections first, `services/tools.go`
sorts tools, and `agent/loop.go` snapshots the catalog once per turn.

- Test unchanged prefix/schema byte stability with time held fixed; separately
  allow temporal context to change. Requiring the whole real prompt to remain
  identical between turns would be wrong.
- Pin reconnect behavior during and between turns. Preserve next-turn catalog
  refresh rather than freezing the tool set for an entire chat.
- Measure kernel and MCP schema bytes separately, skill-index bytes, cache-hit
  ratio, and latency by model. Use existing MCP filters before adding discovery
  machinery; replace the old arbitrary 10K-token trigger with measured evidence.
- Recheck official provider docs at implementation time and validate automatic
  Claude cache hints against real routing and usage. Session IDs do not by
  themselves prove a cache hit.
- Inspect rate-limit traces before tuning retries: the adapter currently makes
  three attempts with 100/200ms delays and no `Retry-After` handling. If failures
  justify it, add bounded, cancellation-aware backoff inside the adapter.

Done when a repeatable baseline identifies the largest cost and a before/after
comparison demonstrates improvement.

Deletion budget: tests and existing trace analysis first, ~0 production lines;
allow ~60 net lines for justified retry changes, 0 config keys/tools/record
types/loops initially.

## P2 — Enforce zero-cost optional initialization (S)

Repository tools are conditional in `bootstrap/app.go`, but the runner, GitHub
adapter, and workspace service are constructed even without repositories.
Audit initialization side effects and gate construction with registration.
Extend bootstrap tests to check absent resources as well as absent schemas;
apply the audit to disabled integrations without making mandatory services
artificially optional.

Deletion budget: move existing construction under existing conditions; neutral
or fewer production lines, 0 config keys/tools/record types/loops.
## Later — Capabilities requiring demonstrated demand

Choose these after correctness work, based on owner use rather than feature parity.

| Candidate | Evidence and next step | Deletion budget |
| --- | --- | --- |
| Telegram voice input (M) | Image ingestion exists but voice transcription does not. Prioritize for frequent dictation; use one optional transcriber adapter and narrow port, with download/time/size bounds and clear failure replies. | ~150–250 production lines, 1 port/package, ~2–3 config keys, 0 tools/records/loops. |
| Agent-authored skills (M) | Store write/delete already exist; the agent has only `skill_read`. First demonstrate reusable procedures and finish index bounds. Prefer extending the existing skill surface; writes need approval, secret filtering and locking. The memory-only `InternalTool` exception does not apply. | Estimate after schema design; target ~80–150 net lines, replace existing tool where practical, 0 config keys/loops, existing Markdown records only. |
| Anthropic Messages adapter (M) | Add for a real direct-provider or wire-feature need. Provider-neutrality alone is not owner value. Test port fit and wire through the existing selector. | 1 package/selector case, 0 tools/record types/loops; quantify lines and essential config in design. |
| Recall beyond keywords (M) | Capture a real FTS5 miss; try query reformulation and existing search before embeddings. An embedding index inside SQLite is not inherently a fourth durable form, but still needs a measured benefit. | Baseline first: 0 production/config/tool/record/loop additions; estimate implementation only after a failed case. |

## Documentation and operational follow-through (S)

- Reconcile comments mentioning writable workspaces, terminal tools, implementation
  sessions, and retired commands in `agent/loop.go`, `agent/compaction.go`, and
  `services/tools.go`. Update operator docs with each landed phase.
- Document the existing trusted-code execution boundary in the architecture guide.
  Repository mutation or sandboxing needs a separate owner use case and threat
  model; it is not an automatic next phase.
- Verify deployment state before carrying forward old Railway chores: proxy hops,
  MCP server status, and Google client setup require live evidence.
  `internal/config/config_init.go` already prunes retired fields. Do not reset
  `/data/config.yaml` merely to remove them; preserve owner configuration.

Deletion budget: replace stale prose, 0 runtime additions.

## Delivery order and verification

1. Improve bounded status, onboarding, and delivery feedback in separate changes.
   Add relevant cases to the harness regression set as each lands.
2. Continue the existing prompt/provider and optional-initialization backlog.
3. Choose voice input only after dictation demand justifies its adapter cost.
   Keep schedules, watch lists, traces, and larger configuration edits reachable
   through existing tools and `/web`; add direct chat commands only for a concrete
   repeated workflow, not to mirror every panel page.

For behavior changes, run the focused regression first, then
`make fmt vet test race build`. Run `make smoke` when Docker is available;
otherwise report the environment blocker. A roadmap-only review does not
establish a passing runtime or production baseline.
