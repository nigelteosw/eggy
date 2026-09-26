# Eggy TODO

Unfinished work only; delete items when they land. Durable rules and settled
decisions live in `AGENTS.md`; current behavior belongs in `README.md` and the
docs site.

Checked against the checkout on 2026-09-24, from source inspection rather than
production measurements. S/M/L are relative effort, not delivery promises; line
budgets are estimates to refine before implementation.

## P2 — Make status actionable and bounded (S–M)

`CommandService.status` (`internal/commands/commands.go`) reports model, mode,
pending approval summaries, and MCP readiness. Extend it rather than create a
parallel diagnostics command.

- Add effective reasoning settings and a compact shared Google connection state
  using existing runtime reads. Show shared identity and a recovery command when
  authorization needs attention; omit unconfigured integration sections.
- Bound approval summaries and integration detail, with remaining counts and a
  `/web` pointer for full inspection. Treat expired pending entries consistently
  with the approval authority; do not add command-side state mutation.
- Tests: missing integration, failed status read, many approvals, expiry, stable
  ordering, account isolation, and consistent mode wording across web and chat.

Deletion budget: ~40–80 net production lines, 0 config keys/tools/record
types/loops.

## P2 — Finish Telegram onboarding and web handoff (S–M)

The webhook consumes `/start <pairing-token>` for an unlinked private sender,
and `/web` issues a single-use account-bound sign-in link. Build on those paths.

- Add a useful bare `/start` for linked users: brief introduction, `/help`, and
  `/web`. After successful pairing, send a success message and next step; the
  pairing branch currently returns HTTP 204 without a chat reply.
- Keep unlinked users outside the harness except for token redemption. Give
  consumed/expired tokens, repeated updates, already-linked users opening a
  pairing link, and conflicting identity bindings deterministic outcomes.
- A delivery failure after successful pairing must not repeat the mutation.
  Reuse the pairing authority and update-deduplication machinery.
- Keep `/web` explicit and sender-verified. Do not mint login links from generic
  selection callbacks or put them into model context, durable memory, or logs.
- Fix Google setup guidance for the required `expected_email`: reuse the config
  mutation authority or point to the panel's identity setting, so `/google set`
  no longer looks like a complete setup path.

Deletion budget: ~40–90 net production lines, 0 config keys/tools/record
types/loops.

## P2 — Improve Telegram feedback and delivery resilience (M)

Typing indicators, message splitting, HTML fallback, image and PDF input, quoted
replies, approval buttons, and transient choices exist. Improve their edges.

- Give expired or already-used selection buttons an actionable response instead
  of only clearing the spinner. Remove obsolete keyboards where message editing
  allows; check ownership before revealing details.
- Respond clearly to unsupported input. The normalizer rejects documents other
  than images and PDFs, and a voice message is not parsed at all, so it arrives
  as an empty turn. Distinguish permanent unsupported input from retryable
  download failure so webhook redelivery does not repeat notices or model calls.
- Keep structured rate-limit details in the Telegram API error decoder. Verify
  current Bot API behavior at implementation time, then add bounded,
  cancellation-aware waits for explicit retryable rejections. Never replay a
  send after an ambiguous transport failure that may have delivered.
- Test formatting/splitting around long help, Unicode, code blocks and buttons,
  plus cancellation and API rejection, using the existing fake HTTP server.

Deletion budget: ~80–150 net production lines, 0 config keys/tools/record
types/loops. Reuse the current client and turn lifecycle.

## P2 — Accept voice and reach attachments on every channel (M)

Most assistant requests arrive forwarded: a bill, a boarding pass, a screenshot
with a voice note saying "deal with this". Hermes Agent transcribes voice memos
on Telegram and Discord. Eggy's Telegram handler passes images and PDFs to the
model and refuses the turn when the model cannot read them; Discord and web
chat carry no attachments, and nothing handles voice.

- Voice: one optional transcriber adapter behind a narrow port, with download,
  duration, and size bounds and a clear failure reply. Unconfigured, it costs
  nothing and voice gets the unsupported-input reply above.
- Discord and web chat: carry images and PDFs through the same `ContentPart`
  event shape and media-type canonicalization Telegram uses, not a second
  attachment pipeline.
- PDFs on models that read only text (DeepSeek today): decide whether Eggy
  extracts text for them or the reply stays "this model cannot read files".
  Measure how often it happens first.
- Tests: oversized and unreadable input, transcription failure, and webhook
  redelivery not repeating a model call.

Deletion budget: ~150–300 production lines, 1 port/package for transcription,
~2–3 config keys, 0 tools/record types/loops.

## P2 — Send files back (S–M)

Every channel replies with text only: there is no send-document or send-photo
path. "Export that as a sheet", "send me the itinerary as a PDF", and "send the
chart" end as a link or a wall of text.

- Add outbound attachments to the existing reply path on `ports.Channel`, not
  as a new tool. A channel that cannot carry one says so rather than dropping
  it. Bound size; attach only what the turn itself produced.
- Settle the producer contract (a tool result carrying bytes and a media type)
  before adding any producer. Google exports are the obvious first.

Deletion budget: ~80–150 production lines, 0 config keys/tools/record
types/loops.

## P2 — Measure remaining prompt and provider costs (S–M)

`internal/llm/openaicompat/model.go` sends OpenRouter `session_id` and
ephemeral `cache_control` for Anthropic model IDs; `agent/prompt.go` orders
stable sections first, `services/tools.go` sorts tools, and `agent/loop.go`
snapshots the catalog once per turn. What is missing is evidence.

- Test prefix/schema byte stability with time held fixed; separately allow
  temporal context to change. The whole prompt need not match between turns.
- Pin reconnect behavior during and between turns. Keep next-turn catalog
  refresh rather than freezing the tool set for a whole chat.
- Measure core and MCP schema bytes separately, skill-index bytes, cache-hit
  ratio, and latency by model. Use existing MCP filters before adding discovery
  machinery.
- Recheck provider docs and validate automatic Claude cache hints against real
  routing and usage. A session ID does not prove a cache hit.
- Inspect rate-limit traces before tuning retries: the adapter makes three
  attempts with 100/200ms delays and ignores `Retry-After`. If failures justify
  it, add bounded, cancellation-aware backoff inside the adapter.

Done when a repeatable baseline identifies the largest cost and a before/after
comparison shows improvement.

Deletion budget: tests and trace analysis first, ~0 production lines; ~60 net
lines for justified retry changes, 0 config keys/tools/record types/loops.

## P2 — Enforce zero-cost optional initialization (S)

Repository tools are registered conditionally, but `bootstrap/app.go` builds the
runner (`localprocess.New`), GitHub adapter, and workspace sessions even with no
repositories. Gate construction with registration, extend bootstrap tests to
check absent resources as well as absent schemas, and apply the same audit to
disabled integrations without making mandatory services artificially optional.

Deletion budget: neutral or fewer production lines, 0 config keys/tools/record
types/loops.

## P3 — One typed contract per panel config section (M)

Left over from the retired cleanup RFC; its write-envelope, watch-predicate,
and staticcheck findings have landed. Config sections still travel as
`webResult` display rows that the settings cards decode by position
(`GoogleCard`, `HeartbeatCard`, `ModelsCard` read `table_rows[0][i]`), so
reordering headers in Go silently mis-wires a form. The section name is a bare
string switched on in `internal/panel/config_routes.go`, and `appearance` alone
skipping the restart is an `if section == "appearance"` there.

- Define a section descriptor in `internal/panel`: name, a typed read, a
  write, and `appliesWithoutRestart`. Route registration ranges over the
  table, and both switches go away as sections migrate.
- Migrate one section per commit — heartbeat, google, models, providers,
  appearance — each independently revertable; stop when the shape stops paying.
- Keep `webResult` for chat, traces, approvals, schedules, and tools, which
  really are lists for display.

Done when adding a config section touches `config.go`, one setter, one
descriptor, and one card.

Deletion budget: net smaller, ~−190 lines across Go and TypeScript estimated;
0 config keys/tools/records/loops.

## Later — Capabilities requiring demonstrated demand

Choose these on owner use, not feature parity.

| Candidate | Evidence and next step | Deletion budget |
| --- | --- | --- |
| Agent-authored skills (M) | Hermes Agent's headline: after a complex task the agent writes the procedure as a skill and refines it on reuse, so Eggy would improve with use instead of staying as good as the files the owner placed. Store write/delete exist; the agent has only `skill_read`. First demonstrate reusable procedures and finish index bounds. Extend the existing skill surface; writes need approval, secret filtering, and locking. The memory-only `InternalTool` exception does not apply. | Target ~80–150 net lines, replace an existing tool where practical, 0 config keys/loops, existing Markdown records only. |
| Recall beyond keywords (M) | Capture a real FTS5 miss first. Try query reformulation, then an LLM digest of the hits (what Hermes does), before embeddings. An embedding index inside SQLite is not a fourth durable form but still needs a measured benefit. | 0 additions until a failed case exists. |
| Memory upkeep nudge (S) | Hermes periodically prompts its agent to update memory; Eggy writes memory only when a turn decides to. A scheduled consolidation pass conflicts with "unprompted turns cannot mutate anything", so what fits is a nudge at the end of an owner turn. Show a missed durable fact first; changing the invariant is a separate decision. | ~20–50 production lines, 0 config keys/tools/records/loops. |
| Skills as `/name` shortcuts (S) | Pi runs saved Markdown prompts as `/name`; a skill fired by `/brief` makes a recurring request one tap from a phone. Needs a concrete repeated workflow, not a mirror of the panel. Grants no tool and lifts no approval, like `skill_read`. | ~40–80 production lines, 0 config keys/tools/records/loops. |
| Feature plugins (L) | The owner wants plugins that can be added or removed — a travel manager, a D&D game manager — and `plugins/` is kept free for them. Design first: AGENTS.md declines runtime-loaded plugins and a marketplace, so the likely shapes are a compile-time package enabled by config, or a bundle of skills, Markdown documents, and an MCP server. Either must cost nothing when absent and keep approvals per action. | Estimate in design; state config keys, tools, and records per plugin. |
| Anthropic Messages adapter (M) | Add for a real direct-provider or wire-feature need; provider neutrality alone is not owner value. Wire through the existing adapter selector. | 1 package/selector case, 0 tools/record types/loops; quantify lines and config in design. |

## Documentation and operational follow-through (S)

- Fix stale comments: `agent/loop.go:34` (writable workspace) and
  `services/tools.go:68` (`terminal` tool). Update operator docs with each
  landed change.
- `README.md` lags the product: its Telegram command list omits `/google`,
  `/soul`, `/heartbeat`, and `/web`, and it does not mention Discord or accounts.
- Document the trusted-code execution boundary in the architecture guide.
  Repository mutation or sandboxing needs its own use case and threat model.
- Write tested MCP recipes for what Hermes Agent and DeepSeek Harness build in:
  browser automation (e.g. a Playwright MCP server), sandboxed code execution,
  and image generation, each behind `require_approval` where it acts. These are
  configuration already; the gap is a verified setup.
- Verify deployment state before carrying forward old Railway chores (proxy
  hops, MCP server status, Google client setup). `config_init.go` already prunes
  retired fields; do not reset `/data/config.yaml` to remove them.

Deletion budget: prose only, 0 runtime additions.

## Delivery order and verification

1. Status, onboarding, and Telegram feedback, as separate changes, adding
   regression cases as each lands.
2. Voice and cross-channel attachments, then outbound files.
3. Prompt/provider measurement and optional initialization.
4. Agent-authored skills, first from the Later table.

Keep schedules, watch lists, traces, and larger configuration edits reachable
through existing tools and `/web`; add chat commands only for a concrete
repeated workflow.

For behavior changes, run the focused regression first, then
`make fmt vet test race build`. Run `make smoke` when Docker is available;
otherwise report the environment blocker. A roadmap-only review does not
establish a passing runtime or production baseline.
