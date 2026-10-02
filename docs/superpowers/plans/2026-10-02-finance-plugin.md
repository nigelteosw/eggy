# Finance plugin — implementation plan

Status: approved, 2026-10-02. Delete this file when the work lands; git keeps it.

## Goal

Each account can log spending by telling Eggy ("lunch 14.50") or by sending a
receipt photo, correct or delete entries afterwards, and see them in a
**Finance** tab in the web panel. The capability is switched on and off from
Settings and takes effect on restart. Switched off, it costs nothing at
runtime.

## Decisions

| # | Decision | Why |
|---|---|---|
| D1 | **The UI is a native view in the existing SPA.** `FinancePage.tsx` ships in the normal Vite bundle; the nav shows it only when the server reports the feature. | A plugin-served page is a second bundle, set of styles and session check ("one way to do a job"). Runtime-injected frontend code is a runtime plugin system, which AGENTS.md declines. |
| D2 | **This is the first feature plugin**, at `internal/plugins/finance/`, wired at compile time in bootstrap like Tavily. | AGENTS.md reserves `plugins/` for owner-addable feature plugins. There is no `Plugin` interface, because lifecycle interfaces whose only caller is bootstrap are forbidden. |
| D3 | **On and off by restart.** `finance.enabled` is written by a Settings card through `internal/config` and applied by the existing `POST /api/restart`. | Restart already checks the config first, so a bad file is refused rather than landing in safe mode. A hot toggle would be a second lifecycle. |
| D4 | **Entries live in SQLite** (`eggy.db`), owned per account. Disabling never drops data. | SQLite is the one durable form for machine-managed records. |
| D5 | **One tool, `finance`,** with an `action` enum. | Enabling it adds one schema to every model call, not five. |
| D6 | **`finance` claims `ports.InternalTool()`.** Logging runs without approval in `normal` mode; `strict` still gates it. | Gating every logged coffee trains the owner to tap approve without reading. Entries are private to the calling account and nothing outside Eggy can observe them. |
| D7 | **Ten supported currencies:** USD, EUR, JPY, GBP, CNY, AUD, CAD, CHF, HKD, SGD. There is one deployment-wide default, `finance.currency` (SGD), picked from a dropdown. Each entry carries one of the ten, and totals group by currency with no conversion. | Logging abroad in local currency keeps working, and there are no exchange rates to fetch or go stale. |
| D8 | **The currency comes from a field, never from a symbol.** | ¥ is both JPY and CNY, and $ is five of the ten. |
| D9 | **Receipt photos need no OCR code.** | Telegram already hands images to the vision model. If the selected model cannot read images, Eggy already tells the owner (commit 7295e6e). |

**Out of scope for v1:**
- budgets and alerts
- recurring entries
- refunds or income (negative amounts)
- exchange-rate conversion
- CSV import or export
- a per-account default currency
- read access from scheduled or heartbeat turns (see Follow-ups)
- photos in web chat, which waits on the TODO item "Accept voice and reach
  attachments on every channel"

## What it costs

| Item | Count |
|---|---|
| Production lines | ~750–1,000, of which ~450 Go and ~400 TS (see each task) |
| Config keys | 2: `finance.enabled`, `finance.currency` |
| Tools | 1 (`finance`), registered only when enabled |
| Durable record types | 1 (the `finance_entries` table) |
| Background loops | 0 |
| HTTP routes | 5 under `/api/finance/`, mounted only when enabled, plus `finance` in the existing `/api/config/{section}` list |
| Machine-state version bump | none (see Storage) |

When disabled there is no tool schema, no prompt bytes, no `/api/finance`
routes, no Finance tab and no goroutine. Two things remain:
- an empty table, created on open like every other table
- the Finance view's JavaScript in the bundle, which is never rendered

## Architecture

```
Telegram photo / chat text ─▶ model ─▶ finance tool ──┐
                                                       ├─▶ finance.Service ─▶ ports.FinanceStore ─▶ sqlite: finance_entries
Web panel FinancePage ─▶ panel /api/finance/* ─────────┘
```

**`finance.Service` is the one write path.** Validation, normalisation, id
generation and default filling all happen there:
- amounts and their minor units for each currency
- the currency against the supported list
- dates, defaulted to today in the owner's timezone
- categories, lower-cased

The tool and the panel are thin adapters over it. The panel never writes SQL,
and the tool never skips validation.

**The acting account always comes from `ports.PrincipalFromContext(ctx)`.**
- On a turn, the dispatcher puts it on the context.
- In the panel, `requireAccountSession` already puts it there
  (`internal/panel/session.go:181`).
- No tool argument, JSON body field or URL segment names an account.
- The store fails closed without a principal, like every other private store.

### File layout

| File | Contents |
|---|---|
| `internal/ports/finance.go` | `FinanceEntry`, `FinanceFilter`, `FinanceTotals`, `ErrFinanceEntryNotFound`, and the narrow `FinanceStore` interface: `CreateFinanceEntry`, `UpdateFinanceEntry`, `DeleteFinanceEntry`, `ListFinanceEntries`, `FinanceTotals`. Provider-neutral, with no SQL types. |
| `internal/storage/sqlite/finance.go` | `financeSchema` and the methods on `*Store`. |
| `internal/plugins/finance/money.go` | The currency table (code → exponent), `ParseAmount`, `FormatAmount`. |
| `internal/plugins/finance/service.go` | `Service`, `New(store, Options{Currencies, Default, Location, Now})`. |
| `internal/plugins/finance/tool.go` | The `finance` `ports.Tool`. |
| `internal/bootstrap/finance.go` | `newFinance(cfg, database, location, now) (*finance.Service, []ports.Tool)`; returns `nil, nil` when disabled. |
| `internal/panel/finance.go` | The `FinanceService` interface (declared in panel) and the five handlers. |
| `website/src/FinancePage.tsx`, `FinanceCard.tsx` | The view and the Settings card. |

### Storage

```sql
CREATE TABLE IF NOT EXISTS finance_entries (
  id           TEXT PRIMARY KEY,          -- 16 random bytes, hex
  account_id   TEXT NOT NULL,
  occurred_on  TEXT NOT NULL,             -- YYYY-MM-DD in the owner's timezone
  amount_minor INTEGER NOT NULL CHECK (amount_minor > 0),
  currency     TEXT NOT NULL,             -- ISO 4217, upper-case
  category     TEXT NOT NULL,             -- lower-case free text
  merchant     TEXT NOT NULL DEFAULT '',
  note         TEXT NOT NULL DEFAULT '',
  source       TEXT NOT NULL,             -- 'chat' | 'photo' | 'panel'
  created_at   TEXT NOT NULL,             -- RFC3339Nano, as machineSchema does
  updated_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS finance_entries_account_date
  ON finance_entries (account_id, occurred_on);
```

**No version bump.** `openDatabase` (`internal/storage/sqlite/store.go`)
already applies every schema string on every open, all `IF NOT EXISTS`, and
`financeSchema` joins that list.
- A new table is additive: an older binary opens the same database and
  ignores it.
- Raising `MachineStateVersion` would make `refuseNewerMachineState` lock out
  a rollback for no reason.
- This meets AGENTS.md's "preserve `/data/eggy.db` compatibility" through the
  compatibility branch, not the migration branch.
- If a later change alters this table's shape, that change bumps the version.

**Rules for the store:**
- Money is integer minor units, never floats. JPY's exponent is 0; the other
  nine use 2.
- Every statement carries `account_id = ?`. Update and delete match
  `id AND account_id`, so another account's id is `ErrFinanceEntryNotFound`,
  not "forbidden". Nobody can probe for ids that exist.
- `ListFinanceEntries` orders by `occurred_on DESC, created_at DESC` and takes
  a limit. It returns the total count as well, so callers can say how many
  more there are.
- `FinanceTotals(from, to)` returns sums by (currency, category) and by
  (currency, day), computed in SQL.

### The `finance` tool

```json
{
  "action":   "log | update | delete | list | summary",   // required
  "id":       "string",   // update, delete
  "amount":   "string",   // log (required), update; decimal text, e.g. "14.50"
  "currency": "USD | EUR | ... | SGD",   // enum from the configured list; default finance.currency
  "date":     "YYYY-MM-DD",              // default: today, owner's timezone
  "category": "string",   // log (required), update
  "merchant": "string",
  "note":     "string",
  "source":   "chat | photo",            // log; "photo" when read from an image
  "from":     "YYYY-MM-DD", "to": "YYYY-MM-DD"   // list, summary; default: current month
}
```

- `amount` is a string so that "14.50" never passes through a float. The
  parser accepts digits, at most one decimal point, and thousands commas. It
  rejects symbols, signs, zero, overflow, and more decimals than the currency
  allows.
- `Effect: ports.InternalTool()` covers every action (D6). Reads come along
  because one tool has one classification, and reads are ungated anyway.
- The description, kept short since it ships on every call when enabled, tells
  the model to:
  - log a receipt's total, not each line, unless asked
  - reply with what was logged and the entry's id, so "actually that was 15.40"
    becomes an `update`
  - ask rather than guess when a receipt's total, currency or date is unclear
  - use categories from: food, groceries, transport, shopping, bills,
    entertainment, health, travel, other — free text is still accepted
- Output is compact text, not JSON:
  - `log` and `update` echo the entry on one line with its id
  - `list` caps at 50 rows and appends "N more"
  - `summary` gives totals per currency, then per category
- `update` changes only the fields it is given. `delete` is a hard delete,
  and the reply carries the deleted entry so it can be logged again.

**Who can call it.** The tool is in the owner's full catalog only.
Scheduled and heartbeat turns run on the explicit `turns.ReadOnlyTools()`
allowlist (`internal/core/turns/policy.go`), which does not name `finance`, so
an unprompted turn can never log, edit or delete an entry. This holds the
invariant "Unprompted turns cannot use MCP or mutate anything", and no code is
needed for it.

### Panel API

All routes are mounted only when `WebUIConfig.Finance != nil` and all sit
behind `guard`. Writes pass the existing CSRF check.

```
GET    /api/finance/entries?from=&to=&category=&limit=    { entries: [...], total }
POST   /api/finance/entries                                create; source = "panel"
PATCH  /api/finance/entries/{id}                           partial update
DELETE /api/finance/entries/{id}
GET    /api/finance/summary?month=YYYY-MM                  { totals, by_category, by_day, currencies, default_currency }
```

- Amounts go over the wire as decimal strings in both directions. They are
  formatted by `money.go`, so the TypeScript never does arithmetic on minor
  units.
- Errors use the existing JSON shape: an invalid amount or date is 400, and an
  unknown or another account's id is 404.
- `currencies` and `default_currency` come from the running service, so the
  page's dropdown always matches what the service accepts.

### Telling the SPA finance is on

- `GET /api/session` gains `features: string[]`, which is `["finance"]` when
  `WebUIConfig.Finance` is set.
  - `handleAccountSession` is currently a plain function. It becomes
    `handleAccountSession(features []string)`.
  - `safemode.go` passes `nil`.
- It is deliberately not on `/api/mode`. That probe is unauthenticated, and
  whether a deployment tracks finances is nobody else's business.
- The **running** state is `features`. The **saved** state is the `finance`
  config section. The Settings card compares the two to show "Restart to
  apply", so no new endpoint is needed.

### Settings card

- `finance` joins the section list at `internal/panel/web.go:266`, which gives
  `GET` and `POST /api/config/finance` through the existing
  `webConfigGetRoute` and `webConfigSetRoute`.
- The set case calls `config.SetFinance(configPath, enabled, currency)`, which
  runs under the config lock with full validation like every other setter.
- The get case returns the enabled flag, the currency and the
  `config.FinanceCurrencies` list.
- `FinanceCard.tsx` sits in the shared group, beside Heartbeat:
  - an enable switch
  - a currency dropdown
  - Save
  - a "Restart to apply" hint and button whenever the saved and running
    states differ; the button reuses the existing restart call
- The card says that entries are private to each account and that turning the
  feature off keeps them.

### Finance page

- `routing.ts` adds the `finance` ↔ `/finance` view. `isApplicationRoute`
  (`internal/panel/web.go:372`) learns `/finance`, so a refresh serves the SPA.
- `AppNavigation` takes `features` and renders the Finance link only when it
  includes `"finance"`. If `/finance` is opened with the feature off, it falls
  back to Chat.
- The page, top to bottom:
  1. a month switcher (‹ Oct 2026 ›)
  2. one total per currency spent that month, with the default currency first
  3. **by category**: horizontal bars sized against the largest category, in
     plain Tailwind divs, one group per currency (there is no chart library
     and none is added)
  4. **by day**: a row of thin vertical bars for the month in the default
     currency, which is the "bar" view
  5. **entries**: a table of date, merchant, category, amount and source,
     with inline edit and delete
  6. **Add entry**: amount, currency (defaulted), date (defaulted to today),
     category, merchant and note
- It reuses `components/ui`, the theme tokens and the `onSessionExpired`
  handling, and it has an empty state ("No spending logged in October.
  Tell Eggy what you spent, or send a receipt on Telegram.").

## Tasks

Work test-first. Run the focused test, then `make fmt vet test race build`
before each commit. Commit each task to `main` with a scoped message, e.g.
`feat(finance): …`.

**Task 1 — Config (~60 lines).**
- In `internal/config`:
  - add `FinanceConfig{Enabled bool; Currency string}`
  - add the `Finance` field to `Config` with `yaml:"finance,omitempty"`
  - add `FinanceCurrencies`, the one ordered list of the ten codes
- `applyDefaults` upper-cases `Currency` and defaults it to `SGD`.
- Validation rejects any code not in the list.
- Add `SetFinance(path, enabled, currency string) error` through the locked
  writer.
- Tests:
  - the default is SGD
  - `sgd` becomes `SGD`
  - `XYZ` is rejected
  - `SetFinance` round-trips, and leaves the file untouched when the input is
    invalid
  - an unknown key under `finance:` is still rejected

**Task 2 — Port and SQLite store (~150 lines).**
- Add `internal/ports/finance.go` and `internal/storage/sqlite/finance.go`.
- Append `financeSchema` to the list in `openDatabase`.
- Tests in `internal/storage/sqlite`:
  - create, read back, update and delete
  - a partial update leaves the other fields alone
  - list ordering, limit and total
  - totals by category and by day, across two currencies
  - date bounds are inclusive
  - account B gets not found when it lists, updates or deletes A's entry
  - a missing principal fails closed
  - a database stamped v10 opens with the table added and is still stamped
    v10

**Task 3 — Money and service (~150 lines).**
- Write `internal/plugins/finance/money.go` and `service.go`.
- Tests:
  - `14.50`, `14.5`, `1,234.56` and `1200` (JPY) parse
  - `$14`, `-3`, `0`, `0.00`, `1.234` (SGD), `1200.5` (JPY), `abc` and an
    overflow value are rejected
  - formatting round-trips
  - an omitted currency uses the default, and an unsupported one is rejected
  - an omitted date is today in `Location`, not UTC: with a fixed `Now` at
    23:30 UTC, the entry lands on the next day in Asia/Singapore
  - categories are lower-cased and trimmed, and an empty one is rejected
  - `updated_at` advances on update and `created_at` does not

**Task 4 — Tool (~120 lines).**
- Write `internal/plugins/finance/tool.go`.
- Tests:
  - the schema's currency enum equals the configured list
  - `Effect` is `ports.InternalTool()`
  - `services.RuleFor` does not gate the tool in normal mode, and strict
    gates it
  - each action against a fake store
  - unknown fields and unknown actions return errors
  - log output contains the id
  - list caps at 50 with "N more"
  - delete output carries the deleted entry

**Task 5 — Bootstrap (~40 lines).**
- Write `internal/bootstrap/finance.go`, mirroring `tavily.go`.
- In `NewApp`, call `gate(financeTools...)` beside Tavily
  (`internal/bootstrap/app.go:337`).
- Pass the service to `WebUIConfig.Finance`.
- Tests:
  - disabled leaves `finance` out of `registry.Catalog()`, gives no
    `/api/finance/entries` route (404) and an empty `features`
  - enabled registers the tool, mounts the routes, sets `features` to
    `["finance"]`, and gives the tool the configured currency list
  - a scheduled turn's `ReadOnlyTools()` does not include `finance`

**Task 6 — Panel (~130 lines).**
- Write `internal/panel/finance.go`.
- Add the `finance` config section.
- Change `handleAccountSession` to take `features`.
- Add `/finance` to `isApplicationRoute`.
- Tests:
  - each route's happy path
  - a write without CSRF is refused
  - another account's id returns 404
  - a bad amount, date or month returns 400
  - the summary carries `currencies` and `default_currency`
  - a GET of `/finance` serves the SPA
  - `/api/session` returns `features`
  - the config section round-trips through `POST /api/config/finance`

**Task 7 — Web UI (~400 TS lines).**
- Change `routing.ts`, `api.ts` (types and calls), `App.tsx` (pass `features`
  through, conditional nav and view) and `ConfigPage.tsx` (mount the card).
- Add `FinancePage.tsx` and `FinanceCard.tsx`.
- Tests, with `bun test` and `renderToStaticMarkup` in the style of
  `website/tests/navigation.test.ts`, in a new
  `website/tests/finance.test.ts`:
  - the path ↔ view mapping
  - the nav hides Finance without the feature and shows it with it
  - the page renders the totals, the category bars and the empty state from
    fixtures
  - the card shows "Restart to apply" when saved and running states differ
- Then run `make build-web` and check it by hand in the browser at phone
  width.

**Task 8 — Rules and docs (~0 production lines).**
- AGENTS.md, InternalTool paragraph. This also corrects drift already present:
  `heartbeat_respond` claims `InternalTool()` today
  (`internal/core/services/heartbeat_tools.go:87`), though the paragraph says
  only `memory` does.
  - Name all three tools.
  - Restate the test: "a write that lands only in records the calling account
    owns and only it can observe".
  - Record why `finance` qualifies.
  - Keep "nothing else extends the claim".
- `internal/ports/tools.go`: update the `ToolEffect.Internal` doc comment,
  which says the claim exists for "exactly one thing", to match.
- AGENTS.md, Boundaries: record the feature-plugin convention:
  - code lives in `internal/plugins/<name>/`
  - compile-time wiring and a config `enabled` switch
  - records go through a narrow port into SQLite
  - the UI view is gated by `features` on `/api/session`
  - no shared plugin interface until a second plugin needs one
- `config.example.yaml`: a commented `finance:` block.
- Docs site: a short Finance page under configure.
- `TODO.md`: add the follow-ups below.

**Task 9 — End to end.**
- Run `make smoke`. An unavailable Docker daemon is reported as a blocker,
  not as a pass.
- Then, by hand:
  1. Turn finance on in Settings and restart. The tab appears and the tool
     shows on the Tools card.
  2. On Telegram, send "kopi 1.80". Then send a receipt photo. Both appear in
     the tab with the right source.
  3. Say "that receipt was actually 23.40". The edit shows in the tab.
  4. Add, edit and delete an entry in the panel.
  5. Sign in as a second account. Its Finance tab is empty.
  6. Turn finance off and restart. The tab, routes and tool are gone, and
     the rows are still in `eggy.db`. Turn it back on and the rows return.

## Risks

- **Vision misreads a total.** Every log echoes the amount and id, and a
  correction is one sentence. Read the traces from the first week before
  relying on photo logging. Traces are per account, so the amounts in them
  stay private.
- **Category drift**, such as "food" against "eating out". Categories are
  free text by design. If this gets annoying, add a bulk rename in the panel
  through the service rather than an enum.
- **The tool schema costs bytes on every call while enabled.** Keep the
  description under ~600 characters, and check its size against the Tavily
  tool's.
- **The first plugin sets the precedent.** Everything here uses seams that
  already exist: a config section, `gate(...)`, an optional `WebUIConfig`
  field, routes left unmounted when nil, an additive schema. If a second
  plugin needs something this one did not, that is when to discuss shared
  structure.

## Follow-ups (not v1)

- **A weekly spend digest from a schedule.** Grant `finance:list` and
  `finance:summary` to scheduled turns, using the action-scoped grant that
  `schedule:list` already uses. That means `turns.ReadOnlyTools()` names a
  plugin's tool, a coupling worth arguing on its own.
- **A per-account default currency**, if accounts spend mostly in different
  currencies. It would move from config to per-account state, beside `/mode`.
- **Budgets per category** with an over-budget notice.
- **CSV export** from the Finance page.
