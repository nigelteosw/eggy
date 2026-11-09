# Eggy TODO

Unfinished work only; delete items when they land. Durable rules and settled
decisions live in `AGENTS.md`; current behavior belongs in `README.md` and the
docs site.

Triaged against the checkout on 2026-11-09, from source inspection rather than
production measurements. S/M/L are relative effort, not delivery promises; line
budgets are estimates to refine before implementation.

## UI/UX — what is left of the Eggshell overhaul (M)

Review: `docs/superpowers/specs/2026-10-28-ui-ux-review.md`. Bugs, trust,
theme tokens, Home, phone layout and most polish have landed.

**Theme**

- [ ] Five text-size tokens and three radii. There are 145 arbitrary
      `text-[…px]` values in `website/src` (up from 15 at review time), so add a
      test banning them once they are gone. Geist everywhere, one serif for
      voice moments only.
- [ ] Merge the red families and the remaining raw `amber/emerald` literals
      (marketplace `InstallReview`/`InstalledView`, finance) into `--tomato`,
      `--chive`, `--yolk-soft`.
- [ ] Undo for chat delete.
- [ ] Docs site toggle offers Match system explicitly (today it flips between
      two themes).

**Structure**

- [ ] Settings becomes a page in the work area (today `/settings` opens a popup
      over chat); Log out only in the account menu (it is also in
      `ThreadSidebar` and `App`).
- [ ] Check-ins: move the personal heartbeat toggle off My settings, and use
      time pickers for the cadence.
- [ ] Pending restart: drop the Advanced fallback Restart card and the "after
      restart" captions now that the bar exists.
- [ ] Group integrations by job: Channels (Telegram, Discord), Connectors
      (Google, MCP), Skills, Extensions; Settings → Connections goes away.

**Phone**

- [ ] Menu and page title on one header row on non-chat pages.
- [ ] Sticky bottom action bar for dirty forms.
- [ ] Copy and a long-press menu on messages.
- [ ] Overflow audit as a test across every screen at 390 px in both themes:
      no sideways scroll, no hit area under 44 px, no field under 16 px.

**Words**

- [ ] Apply the review's vocabulary table: *When Eggy asks*, *Check-ins*,
      Channels vs Connectors, Extensions vs Plugins.
- [ ] Eggy speaks in first person in chat and Telegram; panel labels in second.
- [ ] Shared settings sections: short titles with one "Shared with everyone"
      label; field labels in words with the YAML key as hint.
- [ ] Settings search reaches card titles and field labels.
- [ ] Telegram's `/schedule` uses the panel's plain-language times.
- [ ] MCP approvals still show raw arguments as the sentence.

**Telegram**

- [ ] One grammar for model effort and thinking (`/model effort` vs
      `/model settings effort`); `/help` a short list, syntax under topics.
- [ ] Expandable blockquotes for shown thinking.

Deletion budget: net production lines should fall (arbitrary type sizes,
status-colour classes, the extra Restart card and Log out buttons, the settings
modal frame). No new config keys, tables, tools or loops.

## Marketplace — remaining follow-ups (S–M)

Source: `docs/superpowers/specs/2026-10-16-marketplace-design.md`.

- [ ] Rollback restores an MCP entry with URL only (`snapshotMCP`); its OAuth
      client, timeouts and filters are lost. Snapshot the whole entry.
- [ ] A disabled catalog MCP server's tuning is lost when an upstream URL
      change re-adds it.
- [ ] An update review lists a skill the owner deleted as planned content,
      though the update will not write it (the record keeps a tombstone).
- [ ] A skill left at `.old-*` after a failed restore reappears on the next List.
- [ ] Config write errors still reach the browser verbatim in a few routes
      (shared pattern with `internal/panel/mcp.go`).
- [ ] Tests: session/CSRF guards for the marketplace routes, component tests for
      the review screens, and OAuth login on a disabled server completing
      without a reconnect.

## P2 — Telegram feedback and delivery resilience (M)

- Expired or already-used selection buttons get an actionable response; remove
  obsolete keyboards where editing allows.
- Unsupported input gets a clear reply. A voice message is not parsed and
  arrives as an empty turn. Distinguish permanent unsupported input from a
  retryable download failure so redelivery does not repeat notices or calls.
- Bounded, cancellation-aware waits on explicit Telegram rate-limit rejections;
  never replay a send after an ambiguous transport failure.
- Tests for splitting around long help, Unicode, code blocks and buttons.

Deletion budget: ~80–150 net production lines, 0 config keys/tools/record
types/loops.

## P2 — Voice and inbound attachments everywhere (M)

Telegram passes images and PDFs; Discord refuses attachments with a notice; web
chat has no upload control; nothing handles voice.

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
- The adapter retries three times at 100/200 ms and ignores `Retry-After`; add
  bounded backoff only if traces justify it.

Done when a repeatable baseline names the largest cost and a before/after
comparison shows improvement.

Deletion budget: ~0 production lines for measurement; ~60 for justified retry
changes.

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
| Agent-authored skills (M) | Store write/delete exist; the agent has only `skill_read`. Demonstrate reusable procedures first. Writes need approval, secret filtering and locking; the `InternalTool` exception does not apply. | ~80–150 net lines, 0 config keys/loops. |
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

1. Telegram feedback and delivery resilience.
2. Voice and inbound attachments, then outbound documents.
3. UI theme and structure, as the review orders it.
4. Measurement, then the typed config contract.

For behavior changes, run the focused regression first, then
`make fmt vet race build`; `make smoke` when Docker is available.
