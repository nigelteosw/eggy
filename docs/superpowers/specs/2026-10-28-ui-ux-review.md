# Eggy UI/UX review and theme

Reviewed 2026-10-28 against `aa2435e`. The web panel was run locally with
`EGGY_FAKE_ADAPTERS=1` and screenshotted at 1440 and 390 px wide; Telegram was
reviewed from `internal/commands` and `internal/channel/telegram`; the docs site
from `docs/src`. The prioritised checklist lives in `TODO.md` under
*UI/UX overhaul*; this file holds the reasoning and the theme.

## The short version

Eggy does things most assistants don't: it keeps a watch list and checks in
when something changes, it asks before it acts, it lives in Telegram as much as
the browser, and several people share one Eggy. Almost none of that shows. What
a person sees is a ChatGPT/Claude.ai layout ("New chat", Today / Yesterday /
Previous 7 days, "Eggy can make mistakes.", "Thought for 4s", a Customize page
with Skills | Connectors | Plugins), dressed in three unrelated visual
identities, with Eggy's own features filed inside a settings modal.

That is most of why it reads as generic. It isn't one bad screen; it's that
the borrowed parts are the polished ones and the parts that are Eggy's own are
the rough ones.

Five problems account for most of it:

1. **Three identities.** The docs site is mint green (`#b9f7a8`) on near-black
   with Inter. The sign-in page is an iridescent "pearl black" carried over
   from another project (`index.css` says so: "carried over from LexCatalyst"),
   with Geist. The app is pastel blue on grey with the system font. The one
   thing they share is the yolk dot in the logo, `#dba400`, and it's the one
   colour the app never uses.
2. **Eggy's features are buried.** The watch list is in Settings → Automation.
   Pending approvals are in Settings → Pending approvals. Check-in timing is in
   Settings → Advanced. Memory (USER.md, MEMORY.md) isn't visible at all. The
   home screen is an empty chat.
3. **Every concept has several names.** Approval mode is called "Approval
   mode", "Access", "Ask to write", and `/mode`, and the three options are
   described four different ways (table below).
4. **Developer-facing surfaces shown to everyone.** Approval prompts show raw
   tool-call JSON. Settings show YAML key names as field labels
   (`bearer_token_env`, `client_secret_env`, "API KEY ENV"). The Tools list
   shows the model-facing prompt text of each tool. Errors reach the screen
   verbatim ("Unexpected non-whitespace character after JSON at position 4").
5. **Restart is everywhere.** Models, MCP servers, extensions, agent-runner
   repositories and checks all say "after restart", and the Restart card is
   repeated in three settings sections. Nobody can tell what's pending.

## Theme: Eggshell

### The idea

Eggy is a household helper, not a cockpit: something that sits on the kitchen
table, keeps an eye on things, and taps you on the shoulder when it matters.
The theme should feel **warm, domestic and quiet**: eggshell paper, dark brown
ink, and one colour, yolk, used *only* where Eggy needs you or is doing
something for you. If yolk is on the screen, look at it.

The brand already has this. The mark is an egg outline tilted 5° with a yolk
(`components/ui/brand.tsx`). The theme takes its palette from the logo rather
than adding one next to it.

Why this and not a cooler tech palette: every AI product right now is a dark
neutral with a blue or violet accent and a gradient somewhere. Eggy's current
blue-on-charcoal and pearl gradients are exactly that. A warm paper palette
with a single food colour is uncommon in the category, fits the name, and is
hard to mistake for a template.

### Colour tokens

One accent. Status colours are kitchen colours, used sparingly, never as
decoration. Contrast ratios are measured against the token in brackets.

| Token | Light: "Eggshell" | Dark: "Cast iron" | Use |
|---|---|---|---|
| `--shell` | `#F6F1E7` | `#161412` | page background |
| `--paper` | `#FFFCF6` | `#1E1B18` | raised surfaces: composer, cards, menus, dialogs |
| `--crumb` | `#ECE5D7` | `#292521` | sidebar, inset wells, hover |
| `--line` | `#E0D7C6` | `#38322B` | the only divider colour |
| `--ink` | `#2B2620` (13.3:1 shell) | `#F0EADF` (15.4:1) | body text |
| `--ink-2` | `#6B6255` (5.3:1 shell, 4.8:1 crumb) | `#A59C8E` (6.8:1) | secondary text |
| `--yolk` | `#E3A21A` | `#F2B630` | primary fills: send, Approve, active indicator |
| `--on-yolk` | `#2B2620` (6.8:1) | `#1A1714` (9.8:1) | text on yolk. Never white |
| `--yolk-ink` | `#8A5A00` (5.3:1 shell) | `#F6C95A` (11.7:1) | yolk as text or icon |
| `--yolk-soft` | `#FBEBC4` | `#3A2E12` | "needs you" background: approval cards, unread badge |
| `--tomato` | `#B4432C` (4.9:1) | `#E07A62` (6.2:1) | destructive and failed |
| `--chive` | `#466E3B` (5.3:1) | `#8DB77F` (8.1:1) | done and healthy, only where "done" is news |

Rules:

- Yolk as a fill is for **one** primary action per view, plus approvals. Links
  are ink, underlined, not yolk.
- No blue anywhere, focus rings included: use `--ink` at 2 px with an offset.
- No gradients, glass, blur or glow. Drop `.login-pearl-bg`, `.pearl-black`
  and the `backdrop-blur` overlays. A plain `--ink` scrim at 30% is enough.
- One red. Merge the `eg-red*` and `destructive` families and the stray
  `amber-950` / `emerald-950` / `red-950` literals in the extensions (19
  distinct status-colour classes today) into `--tomato`, `--chive` and
  `--yolk-soft`.
- Fix the leftovers: `index.html`'s `theme-color` is `#f9fbf8` (greenish), the
  shadows are tinted `rgb(16 24 20)` (green), the "Paper" swatch in
  `AppearanceCard` is `hsl(105 24% 98%)` (green) while the real light
  background is grey, and a comment in `index.css` still talks about "the gold
  ramp repointed at pastel blue".

### Light and dark

Both modes are first-class: designed side by side, not one inverted from the
other. Light is **Eggshell** (morning, paper on a kitchen table); dark is
**Cast iron** (evening, warm charcoal, not black). The token table above is the
whole contract: components use tokens only, so a component that looks right in
one mode looks right in the other.

**Choosing a mode**

- Three options: **Light**, **Dark**, **Match system**. Default: Match system
  (today the default is `dark`, hard-coded in `App.tsx` and the server).
- A per-account preference stored with the person's other settings, so it
  follows them to any browser. Not a deployment-wide setting (see Settings
  below).
- The control sits in the account menu as a three-way segmented switch, one
  tap from anywhere, and again in My settings. It's the same stored value
  behind both, not two settings.
- Docs site: the same three options in its header, stored in `localStorage`
  (the docs have no accounts), defaulting to system.

**No flash on load**

Today the theme is applied only after the `/api/mode` request returns, so a
dark-mode person sees a light frame first, or the reverse. Fix:

- Cache the last applied mode in `localStorage`, and apply it with a few lines
  of inline script in `index.html` before the first paint. The server value
  still wins once the session loads, and updates the cache.
- `<meta name="theme-color">` per mode (`#F6F1E7` / `#161412`) via `media`
  attributes, so the phone status bar and installed-app title bar match.
- `color-scheme: light dark` on `:root`, so native controls, scrollbars and
  form autofill follow the mode.

**What changes between the modes, beyond swapping colours**

- **Elevation reverses its cue.** In light, raised surfaces are lighter than
  the page (paper on shell) with a hairline. In dark, raised surfaces are
  *lighter* than the page too (`#1E1B18` on `#161412`), and the one dialog
  shadow is replaced by a 1 px `--line` border, because shadows disappear on
  dark backgrounds.
- **Yolk gets brighter in dark** (`#F2B630` vs `#E3A21A`) so it keeps the same
  weight, and text on yolk is always dark ink in both modes. White on yolk
  fails contrast in either.
- **No pure black or pure white** anywhere. Cast iron's darkest value is
  `#161412` and its text is `#F0EADF`; pure white text on near-black halates
  for long reading, which chat is.
- **Status tints are re-picked, not inverted**: `--yolk-soft` is a deep amber
  `#3A2E12` in dark, not a pale yellow at low opacity, so approval cards still
  read as "needs you".
- **Code blocks** sit one step below the page in light (`--crumb`) and one step
  above it in dark (`--paper`), so they always read as inset.
- **Images and the egg mark**: the mark's outline uses `currentColor` already;
  the yolk dot uses `--yolk`, so it brightens in dark. User images get a 1 px
  `--line` ring in dark so pale screenshots don't bleed into the page.
- **Focus rings** are `--ink` in both modes, which inverts naturally.

**Keeping both modes honest**

- Extend `website/tests/contrast.test.ts` to check every text/background pair
  in the token table in *both* modes against 4.5:1 (3:1 for large text and
  icons), so a token change that breaks one mode fails the build.
- Every screenshot check (the existing mobile overflow audit included) runs in
  both modes.
- Ban raw colour literals in components (`bg-[hsl(...)]`, `#hex`,
  `amber-950`-style palette classes) with a test that scans `src/` for them;
  the extensions are where the dark-only literals live today.

### Type

- **One family for UI: Geist**, which is already loaded but used only on the
  sign-in page. Load it once and use it everywhere: panel, docs and sign-in.
  Drop Inter from the docs.
- **A small serif for Eggy's voice moments only**: the home greeting, empty
  states, the sign-in line, docs page titles. Fraunces at a soft optical size,
  or Newsreader. This is what makes the product feel written by someone, and
  it's the opposite of the all-sans look of generated UIs. Never use it in
  controls.
- **Five sizes, not 24.** Today the panel uses 24 distinct text sizes, 15 of
  them arbitrary (`text-[12.5px]` ×52, `[11.5px]` ×20, `[13px]` ×17,
  `[13.5px]`, `[14.5px]`, `[10.5px]`…). Define 12 / 13 / 15 / 18 / 24 (caption,
  small, body, title, display) as tokens and delete every arbitrary value.
- Drop the uppercase letter-spaced eyebrows (`section-eyebrow`, the data
  table's `text-[10.5px] uppercase tracking-[0.05em]` headers, "CONVERSATIONS"
  in Traces). Sentence case everywhere.

### Shape, depth and motion

- Radius: 10 px for controls, 16 px for cards and dialogs, full for pills and
  the send button. Today it's nine radius classes, with `rounded-2xl` and
  `rounded-xl` mixed arbitrarily on the same kinds of element.
- Depth comes from surface colour (shell → paper), not shadows. Keep one
  shadow, for menus and dialogs only.
- Motion: keep `fade-in-up` for new messages. Replace the spinner on boot
  ("Loading...") with the egg mark doing a slow 3° wobble. That's the one
  piece of character animation; nothing else should bounce.

### Iconography

Keep the 1.5-stroke line icons and the egg mark. Add no sparkles, wands or
robot heads. The egg can stand in as Eggy's avatar beside its messages in a
group of turns, and as the empty-state picture (a single cracked-shell line
drawing for "nothing here"). Don't reuse it everywhere.

### Voice

- **Eggy speaks in first person, the interface in second.** In chat and
  Telegram: "I'll check your inbox at 9 and only message you if something
  needs you." In panel labels: "Your watch list." Today these mix: Telegram
  says "Eggy does not message you unprompted", while `/soul` says "tell me how
  you'd like me to behave".
- Short, plain, no hedging boilerplate. Delete "Eggy can make mistakes."
  under the composer. It's ChatGPT's line, and Eggy's approval gate is the
  real answer to that worry.
- Name things after what people want, not the mechanism: "Check-ins", not
  "Heartbeat"; "When Eggy asks", not "Approval mode"; "Personality" as the
  subtitle under "Soul".

### What not to do (the generated look)

Icon-in-a-tile feature lists (the sign-in page has three); gradient hero
blobs; "Welcome back"; "Ask Eggy anything..."; a centred empty state with
four suggestion chips that have nothing to do with what this Eggy can
actually reach; uppercase eyebrows over every section; card-inside-card
settings; a blue primary.

## Vocabulary: one name per concept

| Concept | Names in use today | Use |
|---|---|---|
| How much Eggy asks | Approval mode (settings, Telegram menu), Access (composer tooltip name), "Ask to write" (composer chip), `/mode` | **When Eggy asks**: *Every time* / *Before changes* / *Never*. The command stays `/mode` |
| "Normal" described as | "Reading runs freely; anything that writes asks first." (ApprovalsCard) · "Only actions that write or reach outside ask; reads run." (PersonalSettingsCard) · "Native writes ask first, except your private memory. Reads run freely; MCP follows each server's approval policy." (Telegram) · "Normal — ask before anything that writes" | One sentence, one source string, shared by the panel and Telegram: "I'll ask before I change or send anything. Looking things up doesn't need asking." |
| Decline an approval | Reject (Telegram), Not now (web) | **Approve** / **Decline** on both |
| Periodic check | Heartbeat, check-ins, "Heartbeat check-ins", beat | **Check-ins** in all labels; `/heartbeat` keeps working |
| Chat bots vs MCP | Connections (Settings: Discord), Connectors (Customize: MCP, Google) | One word for one thing. **Channels** for Telegram and Discord; **Connectors** for MCP and Google |
| Optional features | Extensions (rail), Capabilities (settings), Plugins (Customize tab) | **Extensions** for Finance / Agent Runner; **Plugins** only for marketplace installs |
| Model | the alias (`deepseek-pro`) shown raw in the composer | Show the alias, with the provider model as secondary text in the menu |

## Findings by area

Severity: **Bug**, a defect; **Trust**, it affects whether someone understands
what they're approving or changing; **Friction**; **Polish**.

### Across the product

- **Trust: approval prompts are raw JSON.** `approvalSummary`
  (`internal/core/services/approval_gate.go`) renders `Call google with
  {"action":"gmail_send",...}`, truncated at 500 bytes, and the result comes
  back as `Approved action completed: %v` (`internal/core/turns/approval.go`).
  This is Eggy's most important decision surface, on both channels. Each
  native tool should provide a human sentence for its call ("Send an email to
  sam@… — subject 'Friday'"), with the arguments available behind "Details".
  It's the same idea as `ports.ToolEffect`: the tool declares it, in its own
  file.
- **Friction: restart is the product's most common instruction**, with no
  record of what's pending. Track "config changed since boot" (a hash of the
  loaded config against the current file) and show **one** bar across the
  panel, "3 changes apply after a restart. [Restart now]", in place of the
  Restart card in Models, Capabilities and Advanced and the scattered "after
  restart" captions. `/status` says the same in Telegram.
- **Trust: raw errors reach people.** A JSON parser message appears at the
  bottom of My settings under fake adapters (cause not traced, but whatever it
  is, the parser text reaches the screen). `TODO.md` already notes config-write
  errors reaching the browser verbatim. Errors should say what failed and what
  to do, and keep the raw text behind "Details".
- **Friction: native `window.confirm`** for deleting a chat, cancelling a
  schedule, removing a model, restarting, and finance (5 places). It's
  unstyled, can't explain consequences, and looks broken in an app window.
  Replace with one small confirm dialog, or with undo where it's cheap
  (deleting a chat).

### Sign-in and first run

- **Polish:** the sign-in page is the pearl-black split-screen with a
  feature-tile list. Replace it with a single centred paper card on shell:
  egg mark, one serif line ("Good to see you." or the deployment's name), the
  form. Drop the marketing column.
- **Trust:** the copy says "running on your own machine" and "this panel only
  talks to the machine it runs on". The documented deployment is Railway.
  Say what's true: "Your Eggy. Conversations stay in this deployment."
- **Bug: the env-bound account shows as "42"** with avatar "4" in the
  sidebar: the Telegram owner ID is used as the display name. Accounts need a
  display name separate from the immutable ID, defaulting to the username.
- **Friction:** `SetupPage` uses config vocabulary ("Provider base URL",
  "API key variable", "Model alias") and its first instruction is
  "Provision missing variables in the deployment environment". Pick a
  provider from a short list (DeepSeek, OpenAI, OpenRouter) that fills the
  base URL, and explain the env var in one line with the exact name to set.
- **Friction:** nothing happens after the first sign-in. Open on Home (below)
  with a short first-run checklist: talk to Eggy on Telegram (`/web`
  backwards), add something to watch, turn on check-ins. Each item ticks
  itself off from real state; the checklist disappears once all three are
  done.

### Home and chat

- **Friction: no home.** The landing view is an empty chat, or "Select a chat
  or start a new one." Add **Home** as the first rail entry: what's waiting
  for you (approvals), what Eggy is watching (the watch list, editable
  inline), what's coming up (next 3 schedules), and the composer. This is the
  one structural change that makes Eggy feel like Eggy, and it's all data
  the panel already loads; it moves cards, it doesn't add a mechanism.
- **Bug: the attach button can't attach.** The composer offers "Add files or
  photos", then refuses to send: "Eggy can't read attached files yet."
  Telegram already accepts images and PDFs (`channel/telegram/media.go`).
  Either wire the web channel to the same media path or remove the button
  until it works.
- **Friction: no stop on the web.** Telegram has `/stop`; the web composer
  has only Send. While a turn runs, the send button should become Stop.
- **Polish: progress shows tool identifiers.** "Calling web_search,
  current_time..." on both channels (`bootstrap/turn_presenter.go`), and
  Telegram keeps a permanent "Called current_time." message in the chat
  history. Show plain verbs ("Searching the web", "Checking your calendar"),
  and in Telegram delete the progress message when the reply arrives rather
  than editing it to past tense.
- **Polish:** the approval card is an amber box with "Needs your approval"
  and "Not now". Make it a yolk-soft card with the human sentence, a "Details"
  disclosure, and **Approve** / **Decline**. After a decision, leave a
  one-line record in the transcript ("Approved · sent email to Sam") instead
  of the card vanishing.
- **Polish:** the empty chat says "Start by asking a question or describing a
  task." Use a serif greeting with the person's name and two or three
  suggestions drawn from what's *configured* (only show "Summarise my inbox"
  if Gmail is connected).
- **Polish:** thinking shows up as a separate "Thinking" message *and* a
  "Thought for Ns" disclosure, two patterns for one idea. Fold reasoning into
  the activity disclosure above the reply.
- **Polish:** assistant replies have no copy action and no timestamp on
  hover. Add both, quietly.
- **Polish:** the composer's model chip and "Ask to write" chip sit at equal
  weight with the attach button. Move "When Eggy asks" out of the composer:
  it's a durable setting, not a per-message one, and having it one tap away
  from the send button makes switching to *Never* too casual for what it does.
  Show it only when it isn't *Before changes*, as a quiet warning pill.

### Navigation and structure

- **Bug: `/scheduled` and `/customize` return a raw Go `404 page not found`**
  on reload or a shared link. `internal/panel/web.go:442` lists the SPA paths
  and still names the old `/finance` and `/extensions` instead. Derive the
  list from `routing.ts`'s views, or serve the SPA for any non-`/api` GET.
- **Friction: four container types for pages.** Settings is a modal,
  extensions take over the work area with their own close button, Scheduled
  and Customize are pages, and Traces is a page reached from the account menu.
  Make everything a page in the work area, Settings included (a modal 1400 px
  wide with its own sidebar is a page already). One back behaviour, one
  heading style.
- **Friction: Log out appears three times**: the account menu, the settings
  rail footer, and the settings mobile header. Keep it in the account menu.
- Proposed rail: **Home · Chats · Watching · Scheduled · Customize ·
  Extensions**, with Settings and Traces in the account menu. Approvals get a
  yolk count badge on Home, the one place yolk appears in the rail.
- **Friction:** no chat search, and the date groups (Today / Yesterday /
  Previous 7 days) collapse on hover-chevron clicks that are hard to find.
  Add search; drop collapsible groups.

### Settings

- **Polish: every title is said twice.** The page header "My settings —
  applies to your Telegram and web sessions" is repeated word for word as the
  first card's header. Three sections use the title "Shared deployment —
  changes here affect everyone". Use short titles ("Models") with the
  scope shown once as a small label ("Shared with everyone").
- **Friction: check-ins live in four places.** The personal toggle is in My
  settings, the watch list in Automation, the cadence and active hours in
  Advanced (labelled `interval (3h, 45m — blank turns it off)`, `active from
  (08:00 — any hour)`), and schedules on their own page. Telegram's
  `/heartbeat on` reply sends people to "Settings → Automation" for the
  interval, which isn't there (`commands/personal.go`). One **Check-ins**
  section: your toggle, your watch list, then the shared cadence with
  proper time pickers.
- **Friction: approval mode has two controls** with different wording: the
  radio list in My settings and another in Pending approvals. Keep one.
- **Trust: Appearance contradicts itself.** It's filed under "Shared
  deployment — appearance, for everyone", and the card says "Saved to your
  config, so it follows you to any browser you log in from." Theme is a
  per-person preference; make it per account and move it to My settings.
- **Trust: field labels are YAML keys.** `bearer_token_env`,
  `oauth_client_id (optional)`, `client_secret_env`, the Providers table's
  "ADAPTER openai_compatible" / "API KEY ENV" columns. Label in words
  ("API key, read from the environment variable"), with the key name as
  hint text. Per the standing rule that a config section isn't done until it
  has a real settings card, these count as unfinished.
- **Friction: native controls mixed with custom ones.** The model picker is a
  native `<select>` with "Use the deployment default" floating below it as a
  caption, next to custom switches and radio cards. Use one segmented/list
  pattern.
- **Polish: the Tools card shows model-facing prompt text**: the
  `heartbeat_respond` description is a 15-line instruction to the model.
  Show a one-line human description per tool (a new field, or the first
  sentence), and keep the full text behind an expander for people debugging.
  "kernel" should read "built in".
- **Polish:** the settings search filters section labels only, so "telegram",
  "timezone" or "API key" find nothing. Index card titles and field labels.

### Customize and integrations

- **Friction: integration setup is split** between Customize (MCP, Google,
  Plugins), Settings → Connections (Discord), Settings → Capabilities
  (Finance, Marketplace sources, Agent Runner toggles) and Setup (Telegram).
  Group by what a person is doing: **Channels** (where you talk to Eggy:
  Telegram, Discord, web), **Connectors** (what Eggy can reach: Google, MCP),
  **Skills**, **Extensions** (Finance, Agent Runner, Marketplace).
- **Polish:** the Customize subtitle "Skills and connectors shared by
  everyone on this Eggy" is the only place sharing is explained on that page;
  Google's card has its own "Shared with all Eggy users". Use one consistent
  "Shared" label component wherever something is deployment-wide.

### Scheduled, watching and traces

- **Polish:** schedule rows show the cron expression as the secondary line
  and the kind as a pill. Lead with plain language ("Every weekday at 9:00"),
  next run as relative time ("in 2 hours"), and keep the expression in the
  tooltip.
- **Polish:** the Scheduled description explains that "the heartbeat is a
  separate mechanism and does not appear in this list". If check-ins had their
  own visible home, this sentence wouldn't need to exist.
- **Polish:** Traces says "1 recent turns". Fix plurals across the panel with
  one helper. The three-column browser is good; keep it, retheme it.

### Mobile (390 px)

A phone is where most people meet Eggy: Telegram first, then the panel through
a `/web` link. So the phone is a primary layout, not a squeezed desktop. Every
item in this review applies at 390 px, and every screen is designed and checked
at 390 px before 1440. What people do from a phone decides what the phone layout
puts first: approve or decline, glance at what Eggy is watching and what's
coming up, a quick chat, and occasionally a setting. Admin work (models, MCP,
people) has to *work* on a phone but doesn't need to be pleasant there.

**Bugs**

- **Settings on a phone has no close button, and Log out is where close
  should be.** The dialog's close button (`SettingsDialog.tsx`, `z-10`) sits
  under `ConfigPage`'s sticky mobile bar (`z-20`), whose top-right control is
  Log out. The one control in the corner people tap to dismiss signs them out.
  Making Settings a page fixes this for good; until then, close goes in that
  bar and Log out leaves it.
- **Inputs zoom the page on iPhone.** iOS Safari zooms in on focus for any
  field under 16 px: the composer is 15 px (`text-[0.9375rem]`), most settings
  fields are 13–14 px (`FIELD_COMPACT`, `Input`'s `text-sm`). Every editable
  field is 16 px on phones.
- **You can't type a new line in the composer on a phone.** Enter sends
  unless Shift is held (`Composer.tsx`), and phone keyboards have no Shift+Enter.
  On touch devices Return inserts a new line and the send button sends.
- **Hover-only controls are invisible on touch.** A chat row's "…" menu shows
  only on hover or for the active chat, so other chats can't be renamed or
  deleted from a phone without opening them first. The same goes for the
  date-group chevrons and the attach button's label. Anything hover reveals on
  desktop is always visible on touch (`@media (hover: none)`), or reachable by
  long-press.

**Friction**

- **Data tables scroll sideways inside cards.** Providers, Models, People and
  Finance use `DataTable`, which overflows at 390 px ("https://api.de…" cut off,
  "deepseek-pro" wrapping mid-word). On phones each row becomes a stacked
  block: name as the title, the rest as label/value lines, actions in a menu.
- **The work starts too far down.** On Traces and other pages the phone gets a
  56 px bar holding only a hamburger, then the page's own heading below it.
  Put the menu button, the page title and its main action in one header row.
- **Traces stacks three fixed-height panels**; the conversations panel is
  mostly empty space on a phone. Use a drill-down on phones: conversations →
  turns → step, each full screen, with a back button.
- A new chat on a phone is two taps (menu, then New chat). Put a new-chat
  button in the chat header beside the title.
- Settings navigation on a phone is a native `<select>`. With Settings as a
  page, use a list that pushes to each section, like iOS Settings.
- **Primary actions sit at the top.** Save buttons on settings cards and
  Add/Install actions sit at the end of long forms or in the page header, out
  of thumb reach. On phones, a form's primary action is a sticky bottom bar
  above the safe area once the form is dirty.
- **Select-to-reply fights the system.** Selecting text in a message shows
  Eggy's floating Reply button on top of iOS's and Android's own selection
  menu. On touch, offer Reply from a long-press menu on the message (with
  Copy) instead of on text selection.
- **Confirms are system dialogs.** `window.confirm` on a phone is a jarring
  OS alert with "OK"/"Cancel". The replacement confirm should be a bottom
  sheet on phones, with the destructive action labelled ("Delete chat").

**Polish**

- **No "Add to Home Screen" support.** There's no web app manifest or
  `apple-touch-icon`, so a saved Eggy gets a screenshot for an icon and opens
  inside browser chrome. Add a manifest (name, the egg mark, both theme
  colours, `display: standalone`) and the touch icon. It's a static file, not a
  new durable form.
- **Safe areas.** The composer already pads for `env(safe-area-inset-bottom)`,
  but the viewport meta lacks `viewport-fit=cover`, so the inset is zero and
  the padding does nothing. Add it, and pad the sidebar and bottom sheets the
  same way. In standalone mode the top inset matters too.
- **Touch targets.** The composer's attach button and model chip are 36 px
  (`h-9`), as are segmented tabs and several row actions. 44 px minimum on
  touch; the visual size can stay smaller with a larger hit area.
- The sidebar opens only from the menu button. An edge swipe from the left to
  open, and a swipe on the scrim to close, is what phone users try first.
- The sign-in page puts the brand panel *below* the form on a phone, so the
  first screen has no logo. With the single-card redesign this goes away.
- When the keyboard opens, keep the latest
  message and the composer in view (use `visualViewport` resize, since `dvh`
  alone doesn't track the iOS keyboard).

**Keeping it honest**

- The existing overflow audit (`.superpowers/audit/measure.ts`) becomes a test
  run on every screen at 390 px in both themes: no horizontal page scroll, no
  hit area under 44 px, no editable field under 16 px.
- Every screenshot in a review or PR includes the 390 px version.

### Telegram

- **Bug: bare `/start` answers "Unknown command. Send /help for commands."**
  Telegram sends `/start` the first time anyone opens the bot, so it's every
  new person's first message from Eggy. Only `/start <token>` from an
  unmapped sender is handled (`channel/telegram/handler.go`). Answer `/start`
  with a short first-person welcome and the `/menu` buttons.
- **Friction: 14 commands in the `/` menu**, ordered by group rather than by
  use, including `mcp`, `google` and `restart`. `/code` isn't in the list at
  all (it isn't in `telegramCommands`). Advertise the everyday set (`menu`,
  `stop`, `clear`, `web`, `settings`, `help`, plus `code` when the extension
  runs); the rest still work when typed and are listed in `/help`.
- **Friction: `/help` is a usage dump**: "`/model settings effort|thinking
  [value]`", plus a paragraph explaining that `/model effort` and `/model
  settings effort` both exist and when to use which. Two grammars for one
  setting is a defect by the repo's own rule. Keep one, and make `/help` a
  short list of what Eggy can do, with the syntax under `/help <topic>`.
- **Polish:** `/menu` and `/settings` append "Use /model, /mode, /heartbeat or
  /web. Telegram provides buttons for these controls." under a panel that
  already has the buttons. Delete it.
- **Polish:** button labels are `Model: deepseek-pro`, `Approval mode:
  normal`, `Thinking: on`, in lower case and config vocabulary. Use the shared
  vocabulary ("When Eggy asks · Before changes").
- **Polish:** setting confirmations prepend the command's reply to the
  re-rendered settings panel, so the message grows a status line on each tap.
  Use `answerCallbackQuery` with a toast ("Check-ins on") and edit the panel
  in place.
- **Polish:** the Markdown → HTML converter (`channel/telegram/format.go`)
  handles bold, code, links, headings and tables but not italics or
  blockquotes, so `_emphasis_` and `> quotes` arrive as literal characters.
  Telegram also supports `<blockquote expandable>`, the natural home for
  shown thinking, which today arrives as a separate "Thinking:" message.
- **Friction:** `/web` with no public URL tells people to "Set
  `server.public_base_url` in config.yaml (or `EGGY_PUBLIC_BASE_URL`) and
  restart." Point at the panel setting instead.

### Docs site

- Retheme to Eggshell: same tokens, Geist, the serif for page titles. Drop
  the mint accent. The docs and the app should look like one product.
- The docs default to dark and the app's stored theme default is also
  `dark`. With a warm paper palette, light is the stronger first impression;
  default to the system preference on both.

## Not proposed

- A component library or design-system package. Tokens in `index.css` and a
  dozen primitives in `components/ui` are the right size.
- A theme picker beyond light/dark/system.
- Mascot illustrations beyond the existing mark. The egg works because it's
  used sparingly.
- Unifying Telegram and web layouts. They're different media; the
  vocabulary, voice and approval wording are what must match.
