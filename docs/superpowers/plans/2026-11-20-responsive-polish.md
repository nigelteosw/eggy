# Responsive and polish pass: audit and plan

Date: 2026-11-20. Scope: the web panel (`website/`), phones first (iPhone),
then tablet and large desktop.

## How the audit was done

- Ran `eggyd` locally with `EGGY_FAKE_ADAPTERS=1`, signed in, and captured every
  routed screen in headless Chrome at 390×844 (iPhone 15/16, light and dark),
  820×1180 (iPad portrait), 1440×900 and 2000×1100, with touch emulation and an
  iOS user agent on the small sizes.
- Ran the existing `bun run audit:mobile` at 375 and 430 px, signed in. It
  passes: no sideways scroll, no hit area under 44 px, no field under 16 px.
  The basics are already right. What follows is what that audit cannot see.
- Read the shell and shared components: `App.tsx`, `index.css`,
  `lib/viewport.ts`, `ThreadSidebar`, `ChatPage`, `Composer`, `SettingsDialog`,
  `confirm`, page containers and every `<details>`/`<select>`.

Already good, and to keep: the keyboard-aware root (`useVisibleFrame`), the
home-indicator inset dropped while the keyboard is up, 44 px touch targets under
`pointer: coarse`, 16 px fields, bottom sheets for confirm and message actions,
edge swipe for the rail, `overscroll-behavior: none` and no tap flash in the
home-screen app, reduced-motion handling.

## Findings

### P0: broken

1. **The weather card breaks between 768 and ~1100 px of content.** At iPad
   portrait (820 px) the rail is pinned open and takes 244 px, but the card
   switches to two columns on the *viewport's* `md`. Result: the cloud overlaps
   the temperature, "H 34° · L 24°" wraps mid-value, "8.30 pm to 10.30 pm"
   spills out of the Next 2 hours tile, the Tomorrow bar clips its summary to
   "Af / th / sh", and the outlook range bars vanish. The same happens on a
   desktop window that is just narrow. Cause: layout keyed to the viewport, not
   to the space the card actually has.
2. **"New chat" is disabled on Home for a fresh account.** `ThreadSidebar`
   disables it while `draftOpen`, and the draft is "open" behind Home, so the
   row is greyed out and tapping it does nothing. From Home it should take you
   to the draft.
3. **Customize's tab bar hides tabs on a phone.** At 390 px "Extensions" is cut
   off and anything after it is off-screen, with only a faint fade to suggest
   it scrolls. The active tab is not scrolled into view when opened by URL.

### P1: iPhone shell and consistency

4. **Only the bottom safe area is handled.** `viewport-fit=cover` is set, but
   no `safe-area-inset-top/left/right` is applied anywhere. In landscape the
   notch or Dynamic Island covers the left edge of the rail and page. In the
   home-screen app the top inset depends on the status-bar style, which is
   not declared. Needs real-device checks; the emulator draws no notch.
5. **Seven page containers, four header heights.** Home `pt-6/8/10`,
   Customize and Scheduled `pt-10`, Finance `pt-8/10`, Settings page `pt-6`,
   Agent Runner `p-6`, Chat `max-w-4xl`, `.page` utility `max-w-4xl py-6`.
   On a phone, Chat and Settings put the title at 28 px from the top,
   Traces at 38 and Customize and Scheduled at 62. Switching tabs makes the
   header jump.
6. **Home on a phone is mostly weather.** The card is about 2.2 screens tall at
   390 px, so Waiting on you, Watching and Coming up are all below the fold.
   That is the part Home exists for.
7. **Chat is loose on a phone.** Every message on a touch screen shows its
   footer (copy, retry, time) at 44 px, so each message carries ~56 px of
   chrome. Bubbles use `leading-7` (28 px on 15 px). Three short exchanges fill
   the screen. Long-press already opens an action sheet, so the per-message row
   is duplicated.
8. **The composer's mode label is cryptic.** The footer reads
   "deepseek-pro   Read". "Read" is the `/mode` rung, but nothing says so.
9. **Two kinds of select.** `components/ui/select.tsx` styles a native select,
   but six raw `<select>`s bypass it (PersonalSettingsCard ×2, AppearanceCard ×2,
   AccountsCard ×2, SetupPage). The Model select in Settings shows the
   browser's chevron and a different height from the Area select in Customize.
10. **Fifteen native `<details>` with the default ▶ marker.** Configure Google,
    Configure Discord, Add MCP server and others look unstyled next to the rest
    of the panel, and do not animate.
11. **Raw config shown as status.** Google Workspace is a four-column table
    whose only value is lowercase "disabled", with empty Client ID, Secret env
    and Products cells. On a phone that becomes a card of labels with nothing
    beside them. Discord shows State "disabled", Application ID "—",
    Bot token "not set". Telegram, in the same tab, uses a sentence and a
    button instead. Three patterns for one kind of thing.
12. **Repeated and over-long copy.** Scheduled says the same thing twice (page
    description and section description). Home's Waiting on you explains
    expiry semantics in a paragraph before saying nothing is waiting.
    The settings caption "Use the deployment default" floats 40 px below the
    select it describes.
13. **Tablet portrait gets a pinned rail.** At `md` (768) the rail is pinned
    open at 244 px, leaving ~570 px for content on an iPad in portrait. That is
    what squeezes finding 1. A drawer up to `lg` (1024) would give the page the
    full width.

### P2: polish

14. Loading states are the word "Loading…", so Home's sections jump when data
    arrives. Use fixed-height skeletons for the Home sections and lists.
15. Large desktop: Customize, Scheduled and Settings stay at 860 px on a 2000 px
    screen. That is right for forms. Do not widen them; only Home and Chat
    should use the room. Confirm Chat's 4xl column still reads well at 2000.
16. Information only in `title=` tooltips (refresh button, rail actions) has no
    touch equivalent. Fine for redundant labels; check none carry the only
    explanation.
17. The account menu is a popover on a phone. A bottom sheet, like confirm and
    message actions, would match the rest of the phone UI.

## Plan

Each phase ships on its own and is checked with the screenshot run below.
Rough cost: no config keys, no tools, no server changes; mostly shared
components replacing per-page markup, so net lines should fall.

### Phase 0: tooling (do first)

- Commit the screenshot script used for this audit as
  `website/scripts/screens.ts` (signs in, captures every `auditPages()` screen
  at 390, 820, 1440 and 2000, both themes) so every phase can be compared
  before and after.
- Extend `mobile-audit.ts` with an **overflowing-text check**: any element whose
  `scrollWidth > clientWidth + 1` and whose computed `overflow` is `visible`.
  That catches finding 1. Run it at 820 as well as 375/430.

### Phase 1: P0 fixes (small)

- Weather card: lay out on the **card's** width with a CSS container query in
  `index.css` (`container-type: inline-size` on `.weather-sky`, two columns at
  `@container (min-width: 720px)`), not `md:`. Plain CSS, so no new Tailwind
  plugin. Let the reading tiles' `detail` truncate. Check at 820 with the rail
  pinned and at 1024.
- New chat from Home: when `draftOpen` and the view is not chat, the row is
  enabled and navigates to the draft. Add a test beside `canStartNewChat`.
- Customize tabs: scroll the active tab into view on mount. On a phone, render
  a full-width select (the shared `Select`) instead of a scrolling segmented
  bar when the tabs do not fit.

### Phase 2: one page shell

- Add `components/ui/page-shell.tsx`: one container (`px-4 sm:px-7`, one top
  offset that matches the chat header row) with two widths, `reading` (860)
  and `wide` (1240). Every routed page uses it with `PageHeader`; delete the
  ad-hoc containers and the `.page` utility.
- A test in `tests/` fails if a page file uses `mx-auto … max-w-` outside
  `page-shell.tsx`, in the style of `type-scale.test.ts`.
- Safe areas: the app root pads `env(safe-area-inset-left/right)`; the rail
  and page headers pad `env(safe-area-inset-top)`. Declare
  `apple-mobile-web-app-status-bar-style` explicitly. Verify on a real iPhone in
  Safari and as a home-screen app, portrait and landscape.

### Phase 3: shared controls

- Replace the six raw `<select>`s with `components/ui/select.tsx`.
- Add `components/ui/disclosure.tsx`: a `<details>` with a chevron that turns,
  a 44 px summary and a short height transition (off under reduced motion).
  Move the fifteen `<details>` onto it.
- Add a `StatusRow` (state badge, one sentence, one action) and use it for
  Telegram, Discord and Google in Customize, replacing the config tables.
  Show "Not set up" rather than raw `disabled` and empty cells.
- Put helper captions directly under their field (finding 12).

### Phase 4: phone density

- **Home:** under ~640 px the weather card becomes compact: greeting, status,
  temperature and glyph, then a horizontally scrolling strip for Today and the
  outlook, and the umbrella note. Readings fold into one chip row. Target: the
  card fits in about one screen and Waiting on you starts in the first
  scroll. Larger widths are unchanged.
- **Chat:** on touch, show the footer only under the latest reply; earlier
  messages use the long-press sheet that already exists (add Retry to it).
  Bubble line height `leading-6` on phones. Group timestamps rather than
  stamping every message.
- **Composer:** show the mode as a labelled chip ("Asks before changes" and so
  on, the same wording as the radio options) that opens the mode choice.
- Trim the copy in finding 12. Expiry semantics move to help text under the
  section, shown only when something is actually expired.

### Phase 5: tablet and large screens

- The rail becomes a drawer below `lg` (1024), not `md`. iPad portrait then
  gets the full width, and landscape keeps the pinned rail.
- Skeletons for Home's sections and the thread list.
- Account menu as a bottom sheet on a phone.
- Check Chat at 2000 px; widen only if the column reads as lost.

### Phase 6: caching

What happens today: nothing is cached on either side.

- **API data.** `lib/api.ts` `request()` is a bare `fetch` and every page
  refetches on mount. Going Home → Chat → Home re-requests threads, the
  watch list, schedules, the agent selection, approvals and weather, and
  shows "Loading…" each time. Opening a thread you just left reloads all its
  messages.
- **The app's own files.** `webUIHandler` serves the embedded bundle through
  `http.FileServer` with no `Cache-Control`. Files in `embed.FS` have no
  modification time, so there is no `Last-Modified` to revalidate against
  either. The browser guesses, and on a phone over mobile data each cold open
  can download the whole JS bundle again, even though Vite already
  content-hashes everything under `/assets/`.

Plan, cheapest first:

1. **Server headers (Go, about 10 lines in `internal/panel/web.go`).**
   `/assets/*` gets `Cache-Control: public, max-age=31536000, immutable`; it
   is safe because the filenames change with their content. `index.html` and
   app routes get `Cache-Control: no-cache` plus an `ETag` computed once from
   the embedded file at startup, so a deploy is picked up on the next open and
   an unchanged shell costs a 304. A test in `webui_test.go` or `web_test.go`
   pins both. Icons and the manifest get a day's `max-age`.
2. **An in-memory, stale-while-revalidate cache for GETs (about 60 lines,
   `lib/cache.ts`).** A `useCached(key, load)` hook: if there is a cached
   value, render it at once, refetch in the background and swap in the
   answer. Keys: thread list, per-thread messages, watch list, schedules,
   agent selection, approvals, session features, weather. Invalidation uses
   the `writes` event `request()` already dispatches after every non-GET, plus
   targeted invalidation where a write is known (cancelling a schedule clears
   `schedules`). The whole cache is cleared on sign-out and session expiry,
   because it holds one account's private data. No library: TanStack Query
   would do this, but it is a framework-sized dependency for one hook, and
   AGENTS.md asks for that argument first.
3. **Not persisted to browser storage.** Conversations, memory and approvals
   are private to an account, and a shared iPad or a browser someone else
   later signs into should not open with the last person's data on screen. The
   one exception is what is already cached: the theme and look. Weather could
   join it, since it is shared and not private, if a reload's first paint
   still feels slow after (1) and (2).
4. **No service worker for now.** It would allow an offline shell for the
   home-screen app, but it brings stale-deploy and auth-cache edge cases. With
   (1) a repeat open is already a 304 plus cached assets. Revisit if offline
   use becomes a goal.

Done when: Home → Chat → Home and leaving and reopening a thread show no
"Loading…" on the second visit; a write elsewhere (cancel a schedule, edit the
watch list, approve something) updates the views that show it; sign-out leaves
nothing behind; a second cold open of the panel downloads no JS (304 for the
shell, cache hits for `/assets/`).

## What was built (2026-11-20)

All six phases landed. Where the build departed from the plan:

- **Disclosures are one CSS rule, not a component.** The fifteen `<details>`
  already shared their markup, so a `summary` rule in `index.css` (chevron,
  44 px on touch, fade on open) changed all of them without touching fifteen
  files. A summary with `list-none` keeps its own layout.
- **The account menu stays a popover.** On a phone it already opens upward
  from the foot of the drawer, at the thumb, and the drawer is moved with a
  CSS transform, which breaks the fixed positioning a sheet needs.
- **Fields are 16 px on any touch screen, not only below `sm`.** The audit at
  820 px found every field at 13 px on an iPad, which iOS zooms into, so the
  rule follows `pointer: coarse` in `index.css`, beside the 44 px hit-area rule.
- **The audit opens every disclosure before measuring**, and checks for text
  spilling out of its box. Both found real problems, now fixed.
- **The cache is three functions in `lib/cache.ts`** (`recall`, `remember`,
  `forgetAll`) used at each loader, not a hook. Every write, chat included,
  and every session end forgets everything.

## Done when

- `bun run audit:mobile` passes at 375, 390, 430 and 820 with the new
  overflow check, in both themes.
- The screenshot set at all four widths shows the same header height on
  every page, no clipped text, and Home's Waiting on you within the first scroll
  on a 390 px phone.
- Checked by hand on a real iPhone, in Safari and the home-screen app, portrait
  and landscape, with the keyboard open in chat and in a settings field.
