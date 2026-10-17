# Eggy TODO

Unfinished work only; delete items when they land. Durable rules and settled
decisions live in `AGENTS.md`; current behavior belongs in `README.md` and the
docs site.

Checked against the checkout on 2026-09-24, from source inspection rather than
production measurements. S/M/L are relative effort, not delivery promises; line
budgets are estimates to refine before implementation.

## P0 — Agent Runner extension: Eggy codes on allowed repositories (L)

Spec: `docs/superpowers/specs/2026-10-11-agent-runner-design.md` (approved
2026-10-12). Plan: `docs/superpowers/plans/2026-10-12-agent-runner.md`
(11 tasks). A coding turn on Eggy's loop, on the model `agent_runner.model`
names, edits a sandboxed checkout of an owner-allowed repository, and the
extension pushes `eggy/<run-id>` and opens a PR with the account's own PAT.

- `internal/extensions/agentrunner/`, one `agent_runner.enabled` switch, its
  own model and skill toggles, separate from Eggy's chat settings.
- Each account sets its own PAT in settings and enables repositories in the
  panel or with `/code repos enable <name>`.
- Start only from the panel form or `/code run <name> <task>`; the start is a
  24-hour mandate that authorizes the push and one PR in every mode. Eggy
  never approves, merges, force-pushes, tags, or touches `.github/**` or a
  protected branch.
- `bash` and `run_check` run as uid `coder` under Landlock ABI 6 (verified on
  Railway 2026-10-12), with HTTPS egress only; a failed startup probe drops
  `bash`. Only `run_check` exit codes count as PR evidence.
- Go and Node are baked into the image.
- Revise the `AGENTS.md` shipping and `/mode` rules in Task 8.

Deletion budget: ~2,300–3,800 production lines (estimate), 3 config keys plus
`repositories[].checks`, 1 owner tool plus 7 coding tools outside the
registry, 2 tables, 1 sealed record per account, 1 worker while enabled,
1 system user.

## Marketplace — open before merge (not ready to merge)

Whole-branch review of `marketplace` (2026-10-17) requested changes; the ticked
items are fixed on the branch, pending an Opus re-review. Source:
`docs/superpowers/specs/2026-10-16-marketplace-design.md`. Fixed so far:
`624b2d6`, `5e1dbeb`.

Blockers:
- [x] Update is not atomic: a mid-update failure leaves files changed while the
      record keeps the old hashes, with no rollback (`internal/extensions/marketplace/service.go` Update).
- [x] Owner maps are keyed by name across kinds: an MCP entry can count as owning
      a skill of the same name, so Update can overwrite a hand-placed skill (`service.go` owned/Replacing).
- [x] Owner MCP tuning is discarded: `enabled` and `require_approval` changes are
      not counted as edits, and a changed URL re-adds the server (`service.go` fileChanged, Update MCP step).
- [x] Remove and Update break when an installed skill was deleted by hand: the
      install is stuck until delete-edited is ticked (`service.go` fileChanged, releaseSkill).
- [x] Stdio servers can run from Preview before approval, and the approval route
      can enable a stdio server (`internal/mcp/preview.go`, `internal/config/config_mutate.go` SetMCPServerApproval).
- [x] Preview changes the running manager: it stores OAuth state on the live
      manager, and it builds no bearer handler for a disabled server (`internal/mcp/preview.go`).
- [x] Sources save reads `marketplace.enabled` outside the config lock, so a concurrent
      edit can be reverted (`internal/panel/ext_marketplace.go` sources PUT).
- [x] Skill replace is not crash-safe: a crash between the two renames leaves the
      owner's skill under a hidden `.old-*` name (`internal/skills/store.go` placeBundle).
- [x] No tests for external (github/git-subdir) items, nor for the UI's refusal,
      rename, and re-read paths (`InstallReview.tsx`).

Lower priority:
- [ ] `skill_read` schema grows for every owner, not only those with directory skills.
- [ ] The index-overflow warning logs on every turn (`internal/core/services/skills.go`).
- [ ] Raw error text from the config and GitHub reaches the browser in a few places.
- [ ] Connectors row lacks the "from plugin" label.
- [ ] Session/CSRF guard tests for the new routes.

Merge gate: the blockers above, then a re-review by a model stronger than Haiku.

## P0 — Customize: skills, connectors, plugins in one panel view (M–L)

Not started; scoped in conversation 2026-10-12. A fourth `View` in
`website/src/routing.ts` (`customize`, `/customize`) with a rail entry under
Scheduled, and a `CustomizePage` with `Skills | Connectors | Plugins` tabs,
search, sort and an Add menu. Yours only: no Discover tab and no fetch-from-URL
install from Discover, which is the marketplace in
`docs/superpowers/specs/2026-10-16-marketplace-design.md`.

Build in this order, test-first, each step landing on its own:

1. **Shell and Skills tab.** `internal/panel/skills.go` routes for list, read,
   write, delete and enable, through `SkillsService` (today read-only; it gains
   write, delete and enable over the existing `SkillsStore.Write`/`Delete`, no
   second path). A disabled skill drops out of the prompt index, so it costs
   nothing. Rows show name, description and modified time; ⋮ offers view, edit,
   disable, delete; Add writes a skill.
2. **Move Connectors in.** `McpCard` leaves Settings and Customize owns MCP,
   including the OAuth start and failure detail. No backend change: the existing
   `internal/panel/mcp.go` list and set routes already carry it. One home, not
   two.
3. **Skills as directories.** Done (`1257379`, fix `cf63933`).
4. **Plugins.** Superseded by `docs/superpowers/specs/2026-10-16-marketplace-design.md`.

Deletion budget: net-new, roughly 600–900 production lines (estimate) plus the
SPA, 0 config keys beyond the plugin toggle, 0 tools (`skill_read` stays the
only one), 1 durable flag per skill and plugin in SQLite, 0 loops. Removing
`McpCard` from Settings offsets part of it.

## P1 — Google login by callback instead of pasted URL (S–M)

`/google login` sends the owner to consent, which redirects to the dead
`http://localhost:1` (`google.LoopbackRedirect`); the owner copies the failed
URL back. That was chosen because a desktop-type client needs no registered
redirect. `server.public_base_url` now exists and the MCP flow already uses it
at `/auth/mcp/{server}/callback`, so Google can complete itself.

- A panel "Connect Google" button and `GET /auth/google/callback`, which calls
  the existing `Auth.CompleteLogin`. Unlike the MCP callback it requires a
  signed-in panel session as well as a matching `state`.
- The redirect becomes `public_base_url + /auth/google/callback`, one value
  shared by `BeginLogin` and the exchange so it still matches byte for byte.
  Reword the `LoopbackRedirect` comment and the AGENTS.md OAuth invariant.
- Needs a Web-application OAuth client with that redirect registered in Google
  Cloud (desktop clients cannot take an https redirect): a new client ID and
  secret, then one reconnect. Say so on the settings card.
- Keep `/google login <url>` as the fallback when no public base URL is set.
  Google's device flow is out: it excludes the Gmail, Calendar and Docs scopes.

Deletion budget: ~60–120 production lines, 0 config keys, 0 tools, 0 record
types, 0 loops.

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

## P3 — Finance follow-ups (S–M each)

The finance extension shipped tracking only. Take these on demonstrated use, one
at a time, not as a set.

- **A weekly spend digest from a schedule.** Grant the scheduled turn
  `finance:list` and `finance:summary`, scoped by action the way
  `schedule:list` already is in `turns.ReadOnlyTools()`. That makes the
  unprompted-turn allowlist name an extension's tool, a coupling worth arguing on
  its own; `log`, `update` and `delete` must stay off it.
- **A per-account default currency**, if accounts spend mostly in different
  currencies. It moves from `finance.currency` to per-account state beside
  `/mode`, and the config key becomes only a fresh account's start.
- **Budgets per category** with an over-budget notice at log time. Needs one
  record type; no loop, because the check runs when an entry is logged.
- **CSV export** from the Finance tab, through the existing list route.

Deletion budget: state it per item when taken up. None may add a tool, since
the `finance` tool's one schema is already paid on every call while enabled.

## Later — Capabilities requiring demonstrated demand

Choose these on owner use, not feature parity.

| Candidate | Evidence and next step | Deletion budget |
| --- | --- | --- |
| Agent-authored skills (M) | Hermes Agent's headline: after a complex task the agent writes the procedure as a skill and refines it on reuse, so Eggy would improve with use instead of staying as good as the files the owner placed. Store write/delete exist; the agent has only `skill_read`. First demonstrate reusable procedures and finish index bounds. Extend the existing skill surface; writes need approval, secret filtering, and locking. The memory-only `InternalTool` exception does not apply. | Target ~80–150 net lines, replace an existing tool where practical, 0 config keys/loops, existing Markdown records only. |
| Recall beyond keywords (M) | Capture a real FTS5 miss first. Try query reformulation, then an LLM digest of the hits (what Hermes does), before embeddings. An embedding index inside SQLite is not a fourth durable form but still needs a measured benefit. | 0 additions until a failed case exists. |
| Memory upkeep nudge (S) | Hermes periodically prompts its agent to update memory; Eggy writes memory only when a turn decides to. A scheduled consolidation pass conflicts with "unprompted turns cannot mutate anything", so what fits is a nudge at the end of an owner turn. Show a missed durable fact first; changing the invariant is a separate decision. | ~20–50 production lines, 0 config keys/tools/records/loops. |
| Skills as `/name` shortcuts (S) | Pi runs saved Markdown prompts as `/name`; a skill fired by `/brief` makes a recurring request one tap from a phone. Needs a concrete repeated workflow, not a mirror of the panel. Grants no tool and lifts no approval, like `skill_read`. | ~40–80 production lines, 0 config keys/tools/records/loops. |
| Feature extensions (L) | The owner wants extensions that can be added or removed — a travel manager, a D&D game manager — and `extensions/` is kept free for them. Design first: AGENTS.md declines runtime-loaded plugins and a marketplace, so the likely shapes are a compile-time package enabled by config, or a bundle of skills, Markdown documents, and an MCP server. Either must cost nothing when absent and keep approvals per action. | Estimate in design; state config keys, tools, and records per extension. |
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
