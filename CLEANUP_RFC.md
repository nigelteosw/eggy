# Cleanup RFC

What to clean up in Eggy, and what to leave alone.

Status: proposal only, nothing implemented. Measured against `main` @ `8fdaed2`
on 2026-09-05.

---

## 0. The read

The premise this started from was that Eggy has grown bloated. The
measurements do not support it:

| | |
| --- | --- |
| Production Go | 22,846 lines — 4,151 comment, 1,592 blank, **17,103 code** |
| Test Go | 20,642 lines |
| Panel TS/TSX | 4,919 lines |
| `go vet ./...` | clean |
| Unreferenced exported functions | 0 |
| Functions over 200 lines | 1 (`bootstrap.NewApp`, already argued for) |
| Duplicated test fakes | `roundTripFunc` ×4, four lines each |

17,103 lines of code is unremarkable for Telegram plus a web panel plus MCP
plus Google plus two OAuth flows plus SQLite plus cron plus repository tools
plus tracing plus approvals. The 18% comment density is house style and
load-bearing — several comments in this tree are the only record of why
something is the way it is. Deleting an argument along with the code it
explains is a net loss.

**What has grown is the marginal cost, not the total.** `appearance` is the
simplest section Eggy has — one field, two legal values, no runtime effect —
and adding it required edits in `config.go`, `config_mutate.go`, the 98-line
GET switch, the 80-line POST switch, `ConfigPage.tsx`, `api.ts`, and a new
card. Six files, two languages, for an enum.

Eggy has three surfaces onto one config — YAML, Telegram, the web panel — and
the seam between them is held together by repetition rather than by structure.
Every instance below is *correct today*. Each is one careless edit from not
being.

So this is not "delete code to hit a number." `AGENTS.md` retired that target
and was right to. It is: **turn four conventions into four mechanisms**, and
let the line count fall out as a side effect.

---

## 1. Findings

Ranked by defect risk, not by line count.

### S1 — The panel reads config by column index

**Risk: silent breakage across the language boundary.**

`webResult` is one envelope — `state`, `title`, `detail`, `fields`,
`table_headers`, `table_rows`, `lines`, `next` — serving every route: chat
history, traces, schedules, model catalogs, and all six config sections.
Because config values ship as `[][]string` display rows, the React cards
recover them positionally.

```tsx
// website/src/TracingCard.tsx
const row = result?.table_rows?.[0];
setEnabled(row[0] !== "off");   // state
setKeepTurns(row[1] ?? "");
setRetention(row[2] ?? "");
setMaxBodyBytes(row[3] ?? "");
```

```go
// internal/web/web.go — the producer, 400 lines away, in another language
result.TableHeaders = []string{"Tracing", "Turns kept", "Kept for", "Max body"}
```

Reorder those headers in Go and the tracing form silently starts writing the
retention into the turn cap. Nothing catches it: not the compiler, not
`go test`, not `tsc`.

The same shape appears in `api.ts` (`{ id: row[0], title: row[1], updatedAt:
row[2] }`), `ChatPage.tsx` (`row[0] === "user"`), and `ModelsCard.tsx`.

This is also why `webConfigGetRoute` is 98 lines: the HTTP layer is doing
presentation — deciding that a zero interval renders as `"off"` — because the
transport can only carry strings in a grid. The envelope is a holdover from
when the panel rendered Telegram command results. It no longer does.

Where: `internal/web/web.go`, `website/src/{TracingCard,ChatPage,ModelsCard}.tsx`,
`website/src/api.ts`

### S2 — Nine hand-copied config write envelopes

**Risk: a safety invariant held by copy-paste.**

`AGENTS.md` states the rule plainly: *"Every config write goes through
`internal/config` under one file lock with the same validation."* That is true
today. It is true because nine functions each independently spell it out.

```go
// SetProvider, SetModelAlias, SetMCPServer, SetMCPServerEnabled,
// RemoveMCPServer, SetGoogle, SetAppearance, SetHeartbeat, SetTracing
return filelock.With(path, func() error {
    cfg, err := LoadDocument(path)
    if err != nil { return err }
    /* ... the four to twenty lines that differ ... */
    if err := cfg.Validate(); err != nil { return err }
    return writeConfigUnlocked(path, cfg)
})
```

All nine hold the invariant. The tenth is where it breaks, and it breaks
silently: a setter that forgets `cfg.Validate()` writes an invalid
`config.yaml`, and the owner discovers it at the next restart, in safe mode.

Where: `internal/config/config_mutate.go:126–509`

### S3 — The watch-list emptiness rule exists twice

**Risk: one job, two ways — the rule `AGENTS.md` names first.**

`bootstrap.App.watchListIsEmpty` decides whether a heartbeat tick is skipped.
`web.watchIsEmpty` decides whether the panel warns the owner that their
heartbeat is dormant. They implement the same predicate — blank lines and
Markdown headings do not count — in two packages, and the code says so out
loud:

```go
// internal/web/watch.go:81
// watchIsEmpty mirrors the daemon's own emptiness rule (App.watchListIsEmpty):
// ... The panel must agree with the runtime about what counts, or it would
// tell the owner their heartbeat is armed while the daemon skips every beat.
```

The comment identifies the hazard precisely and then leaves it in place. This
is the clearest violation of "One way to do a job" in the tree, and the
cheapest to close.

Where: `internal/bootstrap/app_events.go:278`, `internal/web/watch.go:87`

### S4 — Wiring ceremony obscures the wiring

**Risk: low. Noise, not danger.**

Three repetitions, none dangerous, all making the interesting parts harder to
see:

- **32 ×** `requireWebSession(webConfig, now, …)` in `NewWebHandler`. The two
  constant arguments are repeated on every route, so the eye has to filter them
  out to read the route table — and a route registered *without* the wrapper
  would not stand out.
- **9 ×** `registerGated(registry, asker, app.approvals, …)` in `NewApp`, each
  with the same three collaborators and the same four-line error check.
- **34 fields** copied one at a time from `Options` into a private struct with
  identical lowercase names — 19 in `turns.New`, 15 in `commands.New`. Adding a
  collaborator means editing three places in one file.

To be explicit: this is **not** the `buildToolRegistry` extraction `AGENTS.md`
rejected. That one needed eleven parameters and hid the coupling. These are
closures over variables already in scope, and struct embedding — no new
parameters, no new indirection, nothing hidden.

Where: `internal/web/web.go`, `internal/bootstrap/app.go`,
`internal/kernel/turns/turns.go:115`, `internal/commands/commands.go:79`

### S5 — A config section is a bare string in five places

**Risk: shotgun surgery on every new section.**

The section name travels as a raw string — a URL path segment, a switch key in
two long functions, a TypeScript union — while the four things that actually
define a section (how to read it, how to write it, what to call the
confirmation, whether a restart applies it) are scattered across those switch
arms.

The special case is revealing: `appearance` is the one section that needs no
restart, and that fact is encoded as `if section == "appearance"` at the bottom
of the POST handler, 60 lines from anything else about appearance.

Where: `internal/web/web.go` (`NewWebHandler`, `webConfigGetRoute`,
`webConfigSetRoute`), `internal/config/config_mutate.go`,
`website/src/{ConfigPage,api}.ts(x)`

### S6 — CI lints with `go vet` and nothing else

**Risk: low. Prevention.**

The pipeline runs `gofmt -l`, `go vet`, tests, and tests under `-race` — good,
and all green. But `go vet` catches almost none of what is above.
`staticcheck` is the cheap addition: unused unexported symbols, redundant
control flow, standard-library misuse. It is the tool that keeps S2 and S4 from
silently growing back.

Where: `.github/workflows/ci.yml`, `Makefile`

---

## 2. Deliberately not findings

Several things look like smells and are not, or are already settled in
`AGENTS.md`. Listed so this proposal does not sell work that has already been
argued, and so a future pass does not re-derive them.

- **The two OAuth flows.** Settled and correct. Google has fixed endpoints; MCP
  does discovery, dynamic registration and per-server keying. Unifying them adds
  branches to delete ~50 lines.
- **Config field count.** Settled. ~20 are MCP knobs, which are what let
  capability arrive as configuration rather than code. Cutting the count means
  cutting a capability.
- **`NewApp` at 307 lines.** Leave it. The extraction was tried and needs eleven
  collaborators. Only the repeated `registerGated` prefix in S4 is worth
  touching, and that is a closure, not an extraction.
- **Test volume and test fakes.** 20,642 test lines against 17,103 code lines is
  a healthy ratio, not bloat. Fake duplication is negligible, and the
  `services`/`repo` split is deliberate. No churn here.
- **Comment density.** 18% of production lines, and this is where the reasoning
  lives. Do not strip it.
- **The 414 KB panel bundle.** React, `react-markdown`, `remark-gfm`. Nine
  runtime dependencies total. Normal, and not on the request path for anything
  Eggy is slow at.

---

## 3. Plan

### Phase 1 — Mechanisms, not conventions

One PR. Purely mechanical. **Every existing test should pass unmodified**; if
one needs editing, that is the signal that the change was not mechanical and
the step should stop.

1. **S2.** Add `mutate(path string, apply func(*Config) error) error` to
   `config_mutate.go`, holding the lock, the load, the validate and the write.
   Rewrite the nine setters as their `apply` bodies. Add one test asserting a
   setter whose `apply` produces an invalid config is refused and leaves the
   file untouched — the property the nine copies assert nine times by
   construction and zero times by test.
2. **S3.** Move the predicate to `ports.WatchListIsEmpty(content string) bool`.
   Both callers may import `ports`; neither may import the other. Delete both
   copies.
3. **S4.** In `NewWebHandler`, a local
   `guard := func(h http.HandlerFunc) http.Handler { … }`; in `NewApp`, a local
   `gate := func(tools ...ports.Tool) error { … }`. In `turns` and `commands`,
   embed `Options` in the service struct and keep only the defaulting in the
   constructor.
4. **S6.** Add `staticcheck` to `make vet` and to the Go CI job. Fix or
   explicitly ignore what it reports in the same PR, so the baseline is clean
   from the first run.

### Phase 2 — One typed contract per config section

This is the phase that actually moves the six. It also touches every settings
card, so it is done incrementally and is abandonable at any point.

1. Define a section descriptor in `internal/web`: name, a
   `read(config.Config) any` returning a typed struct, a
   `write(path string, body json.RawMessage) (title string, err error)`, and
   `appliesWithoutRestart bool`. The route loop becomes a range over a table of
   these; both switch statements go away as their arms migrate.
2. Migrate **tracing first** — the worst offender (four positionally-decoded
   values) and the smallest blast radius. The GET route returns
   `{"enabled":true,"keep_turns":50,"retention":"72h","max_body_bytes":65536}`;
   `TracingCard.tsx` reads fields by name; the `"off"`-versus-zero rendering
   decision moves out of the HTTP layer and into the card, where presentation
   belongs.
3. Then heartbeat, google, models, providers, appearance — one per commit, each
   independently revertable. Stop early if the shape stops paying.
4. **Leave `webResult` in place** for chat, traces, approvals, schedules and
   tools. Those routes genuinely are rendering a list for display, and the
   envelope fits them. The claim is not that it is a bad type; it is that config
   sections are not lists.

Exit criterion: adding a config section touches `config.go`, one setter, one
descriptor and one card. Three files, down from six, and the two long switches
are gone rather than lengthened.

### Phase 3 — What not to do

Recommendation: decline all three.

- **Do not unify the Telegram and web surfaces.** They share
  `internal/config`, which is the part that matters. A shared presentation layer
  would put `commands` and `web` in each other's import path to save formatting
  code — already declined once for `ModelDiscoverer`.
- **Do not extract handler boilerplate in `internal/web`.** The nil-check /
  decode / call / write-error sequence is idiomatic Go, and each handler's error
  strings are the part worth reading.
- **Do not target a line count.** Already retired, for good reasons. If Phase 2
  nets larger on some section, that section keeps the old shape.

---

## 4. Deletion budget

Per the `AGENTS.md` rule that every proposal states one. Line figures are
estimates from the counted call sites; everything else is exact.

| Item | Prod lines | Config keys | Tools | Durable records | Background loops | New ports |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| S2 · config write envelope | −55 | 0 | 0 | 0 | 0 | 0 |
| S3 · watch-list predicate | −12 | 0 | 0 | 0 | 0 | 0 |
| S4 · wiring ceremony | −75 | 0 | 0 | 0 | 0 | 0 |
| S6 · staticcheck in CI | +2 (CI) | 0 | 0 | 0 | 0 | 0 |
| S1 + S5 · typed sections (Go) | −120 | 0 | 0 | 0 | 0 | 0 |
| S1 + S5 · typed sections (TS) | −70 | 0 | 0 | 0 | 0 | 0 |
| **Net** | **≈ −330** | **0** | **0** | **0** | **0** | **0** |

No capability is removed, no config key changes, no tool schema changes, so no
prompt bytes move. A refactor that nets larger is a refactor that failed — this
one nets smaller on every line, which is necessary but not the point. The point
is the Phase 2 exit criterion.

---

## 5. How we know it worked

- **Six becomes three.** Adding a config section touches `config.go`, one
  setter, one descriptor, one card — and no switch statement grows.
- **Column order stops mattering.** No `row[n]` against a config route in
  `website/src`. Renaming a Go field breaks `tsc`, loudly.
- **Validate becomes unskippable.** One test pins that an invalid mutation is
  refused and the file is untouched, instead of nine copies of the same three
  lines.
- **The suite is untouched by Phase 1.** If a test needs editing, the step was
  not mechanical — stop and reconsider it.

---

## 6. Open questions

1. **Does the section descriptor live in `internal/web` or `internal/config`?**
   Web is the only consumer today and the boundary rule allows `web` → `config`,
   so web is the default answer. Config would be wrong: it would put HTTP
   concerns in the file-format package.
2. **Should the typed section payloads be hand-written structs or generated from
   the config structs?** Hand-written, almost certainly — a section's wire shape
   is deliberately narrower than its config shape (`include_recent_history` is
   file-only on purpose), and generation would leak the excluded fields.
3. **Is Phase 2 worth it if it stops after tracing?** Arguably yes: tracing is
   the only section with four positionally-decoded values, so migrating it alone
   removes most of the S1 risk. The rest is consistency. Decide after commit one
   rather than committing to all six up front.
4. **Does `staticcheck` earn its noise?** Unknown until it runs. If the first
   pass reports more than ~20 findings that are all style rather than substance,
   drop it rather than bulk-ignoring — a linter with a long ignore list is worse
   than none.
