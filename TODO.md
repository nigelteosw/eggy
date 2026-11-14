# Eggy TODO

Unfinished work only; delete items when they land. Durable rules and settled
decisions live in `AGENTS.md`; current behavior belongs in `README.md` and the
docs site.

Triaged against the checkout on 2026-11-09, from source inspection rather than
production measurements. S/M/L are relative effort, not delivery promises; line
budgets are estimates to refine before implementation.

## P2 — Voice and inbound attachments everywhere (M)

Telegram passes images and PDFs and refuses voice and other media with a
reply; Discord refuses attachments with a notice; web chat has no upload
control.

- Voice: one optional transcriber behind a narrow port, with download,
  duration and size bounds. Unconfigured, it costs nothing and voice gets the
  unsupported-input reply.
- Discord and web chat carry images and PDFs through the same `ContentPart`
  path Telegram uses, not a second pipeline.
- PDFs on text-only models: measure how often it happens before deciding
  whether to extract text.

Deletion budget: ~150–300 production lines, 1 port/package, ~2–3 config keys,
0 tools/record types/loops.

## P2 — Send documents back (S–M)

Images already go out (`DeliverImage`, Telegram `sendPhoto`). Documents do not:
"send me the itinerary as a PDF" ends as a link or a wall of text.

- Generalize the image delivery path to a document with a media type, not a
  new tool. A channel that cannot carry one says so. Bound size; attach only
  what the turn produced. Google exports are the obvious first producer.

Deletion budget: ~60–120 production lines, 0 config keys/tools/record
types/loops.

## P2 — Measure remaining prompt and provider costs (S–M)

Caching hints, stable prompt ordering, sorted tools, and a per-turn catalog
snapshot exist; what is missing is evidence.

- Test prefix/schema byte stability with time held fixed.
- Measure core and MCP schema bytes, skill-index bytes, cache-hit ratio, and
  latency by model; validate Claude cache hints against real routing.
- Record the process footprint too: binary size, idle memory, and cold start
  to a first reply. PicoClaw (Go, 10–20 MB RAM) and ZeroClaw (Rust, a single
  binary of a few MB) compete on this; know where Eggy stands before anyone
  claims it matters.
- The adapter retries three times at 100/200 ms and ignores `Retry-After`; add
  bounded backoff only if traces justify it.

Done when a repeatable baseline names the largest cost and a before/after
comparison shows improvement.

Deletion budget: ~0 production lines for measurement; ~60 for justified retry
changes.

## From the 2026-11-13 horizon scan

What Herdr, OpenClaw, Hermes Agent and the small "\*Claw" agents do that fits
Eggy's rules. Herdr's agent-to-agent orchestration, its desktop app and its
JavaScript mods are not here: they are declined in `AGENTS.md` (subagent
delegation, a desktop app, runtime-loaded plugins). A mod an agent writes
would run in the session the owner approves from, so it could press Approve.

### Later — Mods: sandboxed JavaScript panels (L)

Layer 2 of `docs/superpowers/specs/2026-11-13-themes-and-mods-design.md`;
themes (layer 1) have landed. Deferred until themes have demonstrated use. A
`mods.enabled` extension: agent-written bundles drawing home cards or pages in
a sandboxed frame with no session, no network and read-only data, turned on
by the person after a preview. First step is the browser escape tests.

Deletion budget: ~600–900 production lines, 1 config key, 1 extension-gated
tool, 2 tables, 0 loops.

### P2 — "Needs you" includes waiting coding runs (S)

Herdr's best idea is one place that says which agents are blocked on you. The
home page's "Needs you" section shows approvals only; a coding run in
`waiting_for_user` is visible only on the agent runner page.

- List waiting runs beside the approvals card, linking to the run. Only when
  the agent runner is enabled, so a disabled extension still costs nothing.

Deletion budget: ~40–80 web lines, 0 Go lines if the runs route already
answers it, 0 config keys/tools/records/loops.

### P3 — Package boundaries that read at a glance (S)

Prompted by Herdr's one-repository-per-program layout. Eggy is one program,
so the split stays folders, not repositories. Two places hide what they hold:

- Move the approval gate (`ApprovalToolCall`, `NewApprovalGatedTool[If]`,
  `RuleFor`, `ApprovalToolExecutor`) out of `internal/core/services` (~4k
  lines of mixed services) into `internal/core/approvals` (70 lines today),
  so the safety mechanism has its own folder. Check the `repo` one-way import
  rule still holds.
- Rename `internal/web` (Tavily search) to `internal/search/tavily`. Three
  unrelated "web" names (`internal/web`, `internal/panel/webui`, `website/`)
  is one too many.

Behavior unchanged. Deletion budget: ~0 net lines, 0 config keys/tools/records/loops.

## P3 — One typed contract per panel config section (M)

Settings cards still decode `webResult` rows by position (`HeartbeatCard`,
`TracingCard`, `GoogleCard`), and `config_routes.go` special-cases
`section == "appearance"`.

- A section descriptor in `internal/panel`: name, typed read, write,
  `appliesWithoutRestart`. Migrate one section per commit; stop when the shape
  stops paying. Keep `webResult` for real display lists.

Deletion budget: net smaller, ~−190 lines estimated; 0 config keys/tools/records/loops.

## P3 — Finance follow-ups (S–M each)

Take on demonstrated use, one at a time. None may add a tool.

- Weekly spend digest from a schedule (`finance:list`/`summary` on the
  unprompted allowlist — argue that coupling on its own).
- Per-account default currency.
- Budgets per category with an over-budget notice at log time.
- CSV export through the existing list route.

## Later — capabilities requiring demonstrated demand

| Candidate | Evidence and next step | Deletion budget |
| --- | --- | --- |
| Agent-authored skills (M) | Store write/delete exist; the agent has only `skill_read`. Demonstrate reusable procedures first; Hermes Agent's skill learning is the reference to read before designing. Writes need approval, secret filtering and locking; the `InternalTool` exception does not apply. | ~80–150 net lines, 0 config keys/loops. |
| Recall beyond keywords (M) | Capture a real FTS5 miss first; try reformulation, then an LLM digest, before embeddings. | 0 until a failed case exists. |
| Memory upkeep nudge (S) | A nudge at the end of an owner turn, not a scheduled pass. Show a missed durable fact first. | ~20–50 lines. |
| Skills as `/name` shortcuts (S) | Needs a concrete repeated workflow. Grants no tool, lifts no approval. | ~40–80 lines. |
| Anthropic Messages adapter (M) | For a real direct-provider or wire-feature need, through the adapter selector. | 1 package/selector case. |

## Documentation (S)

- Document the trusted-code execution boundary in the architecture guide.
- Tested MCP recipes for browser automation, sandboxed code execution, and
  image generation, each behind `require_approval` where it acts.
- Verify deployment state before carrying forward old Railway chores;
  `config_init.go` already prunes retired fields.

## Delivery order

1. Voice and inbound attachments, then outbound documents.
2. "Needs you" includes waiting coding runs.
3. Measurement, then the typed config contract.
4. Package boundaries, whenever the code nearby is being touched anyway.

For behavior changes, run the focused regression first, then
`make fmt vet race build`; `make smoke` when Docker is available.
