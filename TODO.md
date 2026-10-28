# Eggy TODO

Unfinished work only; delete items when they land. Durable rules and settled
decisions live in `AGENTS.md`; current behavior belongs in `README.md` and the
docs site.

Checked against the checkout on 2026-09-24, from source inspection rather than
production measurements. S/M/L are relative effort, not delivery promises; line
budgets are estimates to refine before implementation.

## UI/UX overhaul — the Eggshell theme and one product (L)

Review and reasoning: `docs/superpowers/specs/2026-10-28-ui-ux-review.md`
(2026-10-28). One warm palette taken from the logo (eggshell, ink, one yolk
accent), one vocabulary across web and Telegram, and Eggy's own features
(watching, check-ins, approvals) brought out of Settings. Land in this order;
each step stands alone.

**1. Bugs a person hits on day one (S each)**

- [x] `/scheduled` and `/customize` serve a raw Go 404 on reload; the SPA path
      list in `internal/panel/web.go:442` is stale (`/finance`, `/extensions`).
- [x] Bare `/start` in Telegram answers "Unknown command". Reply with a
      first-person welcome and the `/menu` buttons.
- [x] The env-bound account displays its Telegram ID ("42") as its name.
      *Done:* an optional `name` on the account, set from the People card's
      Edit form. A blank name still shows the ID until someone sets one.
- [x] The web attach button accepts files it then refuses to send. Wire it to
      the media path Telegram already uses, or remove it. *Done:* removed for
      now; the upload control returns when the web channel takes attachments.
- [x] No Stop on the web while a turn runs; the send button should become Stop.
- [x] `/heartbeat on` points to "Settings → Automation" for the interval; it is
      in Advanced.
- [x] Raw error text reaches the screen (a JSON parse error under My settings
      with fake adapters); errors say what failed, raw text behind Details.

- [x] Phone: Settings has no close button and Log out sits where close should
      be (the dialog's close is under `ConfigPage`'s sticky mobile bar).
- [x] Phone: fields under 16 px make iOS zoom on focus (composer 15 px, settings
      13–14 px).
- [x] Phone: Return always sends, so the composer can't take a new line on a
      touch keyboard.
- [x] Phone: hover-only controls (chat row "…", group chevrons) are unreachable
      on touch.

**2. Trust: say what is being approved (M)**

- [x] Each native tool supplies a human sentence for its call, declared in its
      own file like `ToolEffect`; `approvalSummary` falls back to the JSON only
      for MCP. *Done:* the optional `ports.CallDescriber` method; Google's six
      tools, schedules and the agent runner's cancel and answer implement it.
      Reads have no sentence and keep the argument card. *Still open:* the
      outcome line (`Approved action completed: %v`), and a Details disclosure
      that shows the arguments behind the sentence.
- [ ] Approval card on web and Telegram: the sentence, a Details disclosure,
      **Approve / Decline** on both, and a one-line record in the transcript
      after the decision. *Done without the sentence:* Decline on both surfaces,
      and the web transcript records each decision. Still open: the sentence and
      Details.
- [x] One approval-mode control (it is in My settings *and* Pending approvals)
      and one shared description string for each mode, used by the panel and
      `/mode`. Take it out of the composer; show a warning pill only when the
      mode is not *Before changes*. *Done:* the duplicate picker is gone from
      Pending approvals and the composer shows a pill for non-default modes.
      *Still open:* the Telegram `/mode` text is a separate string and needs to
      be unified with the web wording.

**3. Theme (M)**

- [x] Replace the tokens in `website/src/index.css` with the Eggshell / Cast iron
      table from the review; delete the blue ramp, `pearl-*` and `login-pearl-bg`,
      the green-tinted shadows and `theme-color`.
- [ ] Geist everywhere and one serif for voice moments only; five text-size
      tokens, deleting the 15 arbitrary `text-[…px]` values; three radii.
- [ ] Merge the two red families and the raw `amber/emerald/red-950` literals
      into `--tomato`, `--chive`, `--yolk-soft`.
- [x] Replace the 5 `window.confirm` calls with one small confirm dialog (undo
      for chat delete). *Done:* one `useConfirm` dialog, a bottom sheet on a phone.
      Undo for chat delete is still open.
- [x] Light (Eggshell) and dark (Cast iron) as equal modes, plus Match system
      as the default (today `dark` is hard-coded). Per-account, set from a
      three-way switch in the account menu and in My settings.
- [x] No theme flash: cache the mode in `localStorage` and apply it from an
      inline script in `index.html` before first paint; per-mode
      `theme-color` metas; `color-scheme: light dark`.
- [x] Dark-specific rules from the review: lighter raised surfaces with a
      border instead of a shadow, brighter yolk with dark text on it, no pure
      black or white, re-picked status tints.
- [x] `contrast.test.ts` checks every token pair in both modes; a test bans raw
      colour literals in `src/`; screenshot checks run in both modes.
- [ ] Retheme the docs site with the same tokens and drop the mint accent, with
      the same Light / Dark / Match system switch (stored locally).
- [x] Single-card sign-in page with true copy (not "on your own machine").

**4. Structure: Eggy's features where people look (M–L)**

- [ ] **Home** as the landing view: waiting-on-you approvals, the watch list
      (editable inline), the next schedules, the composer, and a first-run
      checklist that ticks itself off from real state. Moves existing cards;
      adds no mechanism.
- [ ] Every destination a page in the work area, Settings included; Log out
      only in the account menu.
- [ ] One **Check-ins** section: personal toggle, watch list, shared cadence
      with time pickers (today spread over four places).
- [ ] One pending-restart bar fed by "config changed since boot", replacing the
      three Restart cards and the scattered "after restart" captions; `/status`
      reports the same.
- [ ] Group integrations by job: Channels (Telegram, Discord), Connectors
      (Google, MCP), Skills, Extensions. Settings → Connections goes away.
- [ ] Theme becomes a per-account preference under My settings.

**4b. Phone as a primary layout (M)**

Every screen is designed and checked at 390 px first; see *Mobile* in the
review.

- [ ] Data tables become stacked rows on phones (Providers, Models, People,
      Finance).
- [ ] One header row on phones: menu, page title, main action; new chat in the
      chat header.
- [ ] Traces and Settings drill down full-screen on phones instead of stacking
      panels or using a native `<select>`.
- [ ] Sticky bottom action bar for dirty forms; confirms as bottom sheets.
- [ ] Reply and Copy from a long-press menu on touch, instead of competing
      with the system text-selection menu.
- [ ] Web app manifest and `apple-touch-icon` with both theme colours;
      `viewport-fit=cover` so the safe-area padding actually applies.
- [ ] 44 px hit areas on touch; edge swipe to open the sidebar; keep the
      composer above the iOS keyboard through `visualViewport`.
- [ ] The overflow audit becomes a test across every screen at 390 px in both
      themes: no sideways page scroll, no hit area under 44 px, no field under
      16 px.

**5. Words (S–M)**

- [ ] Apply the vocabulary table in the review: *When Eggy asks*, *Check-ins*,
      *Approve / Decline*, Channels vs Connectors, Extensions vs Plugins.
- [ ] Eggy speaks in first person in chat and Telegram; panel labels in second.
      Delete "Eggy can make mistakes."
- [ ] Settings: short titles with one "Shared with everyone" label instead of
      the doubled headers; field labels in words with the YAML key as hint.
- [ ] Tools card: one human line per tool, model-facing text behind an
      expander; "kernel" reads "built in".
- [ ] Progress in plain verbs instead of tool names; Telegram deletes the
      progress message when the reply lands instead of leaving "Called x.".

**6. Telegram polish (S–M)**

- [ ] Advertise the everyday commands only (`menu stop clear web settings help`,
      `code` when running); add `/code` to `telegramCommands`.
- [ ] One grammar for model effort and thinking (`/model effort` vs
      `/model settings effort`); `/help` a short list, syntax under topics.
- [ ] Settings buttons toast through `answerCallbackQuery` and edit in place;
      drop the "Use /model, /mode…" footer.
- [ ] Italics, blockquotes, and expandable-blockquote thinking in `format.go`.
- [ ] `/web` without a public URL points to the panel, not `config.yaml`.

**7. Polish (S each)**

- [ ] Chat search; drop collapsible date groups; new-chat button in the phone
      chat header; copy and hover timestamp on replies; thinking folded into
      the activity disclosure.
- [ ] Schedules in plain language with relative next run; plural helper
      ("1 recent turns"); settings search indexes card and field labels.
- [ ] Setup page: provider presets instead of base-URL / env-var vocabulary.

Deletion budget: net production lines should fall. Removed: the pearl CSS
(~90 lines), the duplicate approval-mode control, two of three Restart cards,
two of three Log out buttons, the settings modal frame, 15 arbitrary type
sizes and ~19 status-colour classes. Added: Home (composition of existing
cards), a confirm dialog, the restart-pending flag, per-tool call sentences.
No new config keys except per-account theme (moved, not added); no new
tables, tools or background loops.

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

## Marketplace — follow-ups after merge

Source: `docs/superpowers/specs/2026-10-16-marketplace-design.md`. Two whole-branch
reviews (2026-10-17, Haiku then Opus) found merge blockers; all are fixed on the
`marketplace` branch, including transactional update and remove, kind-keyed
ownership, safe MCP preview, the reviewed-tools pin on enable, crash recovery for
skill replaces, and the sources lock. What remains is below.

- [ ] Remove's "delete my edits" ticks match by bare name, so an install owning a
      skill and an MCP server of the same name deletes both (`service.go` Remove).
- [ ] Restoring an MCP entry during a rollback brings it back disabled with default
      settings; its OAuth client, timeouts, and filters are not snapshotted.
- [ ] A disabled catalog MCP server's non-enable tuning (timeouts, OAuth client)
      is lost when an upstream URL change re-adds it.
- [ ] `skill_read`'s schema grows for every owner, not only those with directory skills.
- [ ] The index-overflow warning logs on every turn (`internal/core/services/skills.go`).
- [ ] Config write errors still reach the browser verbatim in a few routes (shared
      pattern with `internal/panel/mcp.go`).
- [ ] Connectors rows lack a "from plugin" label.
- [ ] Session/CSRF guard tests for the new routes, and component-level tests for the
      review screens (the decision logic is covered through pure helpers only).
- [ ] A test for OAuth login on a disabled server completing without a reconnect.
- [ ] An update review still lists a skill the owner deleted as planned content,
      though the update will not write it (the record keeps a tombstone).
- [ ] A skill left at `.old-*` after a failed restore comes back on the next List,
      even if the owner meant to discard it.
- [ ] Installs and updates are serialized in-process only; one `eggyd` process is
      the deployment model, so this holds, but it is not a cross-process lock.

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
