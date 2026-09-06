# Eggy TODO

Unfinished work only; delete items when they land. Durable rules and settled
decisions live in `AGENTS.md`; current behavior belongs in `README.md` and the
docs site.

Reviewed against the checkout on 2026-09-06. Findings below are from source
inspection, not production measurements. S/M/L indicate relative effort, not
delivery promises. Line budgets are estimates to refine before implementation.

## P1 — Establish a small harness regression set (S, alongside fixes)

Extend existing Go fakes and integration tests rather than add an evaluation
framework. Cover multi-step lookup, steering across compaction, approval,
rejection and expiry, MCP reconnect, provider failure, and unprompted turns
attempting forbidden tools. Assert outcomes and underlying call counts.

For an owner-authorized live sample, use existing traces to compare task success,
model calls, prompt/cached tokens, tool failures, and elapsed time. Keep private
trace bodies and credentials out of fixtures. Do not claim production improvement
without a comparable baseline.

Deletion budget: 0 production lines initially, 0 config keys/tools/record
types/loops; reuse existing tests and traces.

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

## P2 — Bound skill indexing before enabling authorship (S)

`plugins/skills/store.go:List` reads every Markdown file and returns all summaries.
It does not apply `readFile`'s size bound; one malformed file aborts the list.
The prompt includes every returned summary.

Bound individual reads and aggregate index size; surface malformed entries to the
owner without disabling usable skills. Test many, oversized, and malformed files.
Prefer explicit capacity reporting or owner selection over silently hiding skills.

Deletion budget: replace the unbounded listing path, up to ~60 net production
lines, 0 config keys/tools/record types/loops; prefer internal bounds initially.

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

1. Build the harness regression set around the landed context-preservation
   work. This is the biggest remaining correctness win.
2. Measure prompt/provider costs and bound optional initialization and skills.
   Let results determine performance work.
3. Select at most one demand-backed capability from the later list.

For behavior changes, run the focused regression first, then
`make fmt vet test race build`. Run `make smoke` when Docker is available;
otherwise report the environment blocker. A roadmap-only review does not
establish a passing runtime or production baseline.
