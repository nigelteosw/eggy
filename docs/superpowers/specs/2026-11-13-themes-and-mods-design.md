# Themes and mods, chosen by asking

Status: layer 1 (themes) built 2026-11-13; layer 2 (mods) deferred, tracked in
`TODO.md`. As built, layer 1 differs from the draft below: a custom look is
stored on the account's runtime state (one per account) rather than in a
themes table; built-ins are Eggshell and Cast iron; fonts are separate from
the look; radius and tracking were left out; contrast is checked in Go
against palettes a test pins to `index.css`.

## Why

Herdr (herdr.dev) lets people reshape their workspace by asking their agent:
themes, layouts and JavaScript "mods" that draw panels. That is the most
personal thing any comparable project does, and Eggy's panel has nothing like
it: one fixed palette per mode, and a per-account `light`/`dark`/`system`
choice (`ports.State.Agent.Theme`).

Herdr's mods are unsandboxed JavaScript. Herdr can afford that: it runs on the
owner's machine for one person, and its agents already have a shell there, so a
mod adds no authority. Eggy cannot copy it. The panel is where approvals are
pressed, so script running with the owner's session could approve its own
pending action, read every chat and rewrite config. A mod the agent wrote,
steered by an email it read, would be model output creating authority, which
`AGENTS.md` forbids. This design keeps Herdr's feel and keeps that line.

## Two layers

**1. Themes — data, core, always available.**
A theme is a validated set of design values:

- the colour tokens already defined in `website/src/app/index.css`
  (`--background` … `--yolk-ink`), as HSL triples, for light and dark;
- a text font and a code font, each picked from a fixed list served from
  Google Fonts (Geist, Inter, IBM Plex Sans, Fraunces; JetBrains Mono,
  IBM Plex Mono, SF Mono stack) — never a URL;
- radius and heading tracking from bounded ranges.

No CSS text, no selectors, no URLs, so a theme can only recolour and refont.
Eggshell and the Herdr palette (`463f7ac`) ship as built-in themes; the
current `light`/`dark`/`system` choice becomes "which palette of the chosen
theme".

Saving checks contrast with the rule `tests/contrast.test.ts` already holds
(the check moves into a shared function the Go side mirrors, or the panel
validates before save and the server re-validates ranges only — decide in the
plan). A theme that fails is refused with the pair that failed.

**2. Mods — sandboxed JavaScript, an extension, off by default.**
`mods.enabled` gates everything per the extension rule: disabled, there is no
tool, no route, no table read, no prompt bytes.

A mod is a small bundle (HTML, JS, CSS, images; ≤ 512 KB, ≤ 32 files) that
draws into a **slot**:

- a card on the home page, below "Needs you";
- a full page with its own sidebar entry.

No overlays, menus or dialogs on top of Eggy's own UI: a mod that could draw
over the panel could draw a fake sign-in or a fake approval card. Every slot
carries a visible frame label ("Mod: <name>") drawn by the panel, not the mod.

## The sandbox

Each slot is an `<iframe>` pointing at `/mods/{id}/` with
`sandbox="allow-scripts"` (no `allow-same-origin`, no forms, no popups, no top
navigation). The route also sends the same restriction as a header, so a mod
opened directly in a tab is sandboxed too:

```
Content-Security-Policy: sandbox allow-scripts; default-src 'none';
  script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com;
  font-src https://fonts.gstatic.com; img-src 'self' data: blob:; connect-src 'none';
  frame-ancestors 'self'
```

What that buys, layer by layer:

| A mod tries to | Stopped by |
| --- | --- |
| read the session cookie or `localStorage` | opaque origin (no `allow-same-origin`); cookie is `HttpOnly` |
| call `/api/approvals/.../approve` | opaque origin: Lax cookie not sent on a cross-site fetch; `X-Eggy-CSRF` unknown to it; `connect-src 'none'` |
| send data to the internet | `connect-src 'none'`, `default-src 'none'`; no forms, popups or navigation |
| navigate the panel to a phishing page | no `allow-top-navigation` |
| draw a fake approval | slots are inline and labelled; no overlays |

To verify in the plan, with a browser test rather than by reading specs:
whether `'self'` matches the mod's own files from an opaque-origin document in
Chrome, Safari and Firefox. If not, the route inlines the bundle into one
document and `'self'` is dropped.

## What a mod can see and do

Mods talk to the panel only through `postMessage`. The panel answers a fixed
set of messages and ignores the rest; it never forwards a mod's request to an
arbitrary route.

Reads (each answered from a route the panel already calls, scoped to the
signed-in account):

- `theme` — the current tokens, so a mod matches the panel;
- `now` — time and the account's timezone;
- `needs_you` — count and titles of pending approvals and waiting coding runs
  (no payloads);
- `watch` — the watch list;
- `runs` — agent runner runs, only when that extension is enabled;
- `finance.summary` — only when that extension is enabled.

Actions, each one finished by the person, never by the mod:

- `navigate` to a panel page from a fixed list;
- `compose` — open a chat with text prefilled; the person presses Send.

No approve, reject, config, restart, delete or send. Adding an action later
means arguing it here first; the test is "the person still makes the decision
on a surface the mod does not draw".

## Chosen by asking

- **Themes**: one more `settings` action, `set_theme`, taking a named built-in
  or a token set. Ungated by the same test as `select_model`: it changes only
  what the calling account sees, and the personal settings card shows it with
  a reset. Not on the unprompted allowlist.
- **Mods**: a `mods` tool, present only when `mods.enabled`, with `list`,
  `write` (create or replace a draft bundle) and `delete`. `write` never turns
  a mod on. The person turns it on in the panel's Mods page after seeing it
  run in a preview slot — an owner action at a verified session, like a
  marketplace install, not an `approvals.Action`. Replacing an enabled mod's
  bundle turns it back into a draft until it is previewed again, so the agent
  cannot swap code under a mod the person already approved.
  `write` and `delete` are `InternalTool()` by the existing test: they land
  only in records the calling account owns, sees and can delete in its own
  panel, and a draft does nothing until that account enables it.
- Mods are per account. Sharing one with another account is out of scope; it
  would make one person's agent output run in another person's panel.

## Storage

SQLite, additive (`IF NOT EXISTS`, no `MachineStateVersion` bump):

- `themes(account, name, tokens_json, updated_at)` plus the selected theme on
  the account's agent state beside `Theme`;
- `mods(account, id, name, slot, state[draft|enabled], digest, updated_at)`
  and `mod_files(mod_id, path, media_type, body)`.

The enabled digest is recorded at enable time; serving checks it, so a bundle
changed after enabling is served as a draft (not at all, in a slot).

## Files

- `internal/extensions/mods/` — service (the one write path), tool, bundle
  validation, digest.
- `internal/ports/ext_mods.go`, `internal/storage/sqlite/ext_mods.go`,
  `internal/panel/ext_mods.go` (routes: list, preview, enable, disable,
  delete, `GET /mods/{id}/…` with the headers above),
  `internal/config/ext_mods.go`, `internal/bootstrap/ext_mods.go`.
- `website/src/extensions/mods/` — Mods page, slot component, message host.
- Themes: `internal/commands/settings_tool.go` (`set_theme`), the agent state
  field, a theme route in `internal/panel/agent.go`, and
  `website/src/lib/theme.ts` applying tokens as inline CSS variables.

## Deletion budget

- Themes: ~150–250 production lines, 0 config keys, 0 tools (one action), 1
  table, 0 loops.
- Mods: ~600–900 production lines, 1 config key (`mods.enabled`), 1
  extension-gated tool, 2 tables, 0 loops.

## Out of scope

- Unsandboxed mods, mods reaching the network, mods writing anything.
- Layout changes to Eggy's own pages (Herdr's layouts). Slots only.
- A gallery or sharing between accounts or deployments; the marketplace is the
  place to argue that later.

## Order

1. Themes (useful on their own; mods read their tokens).
2. Mods: sandbox route and headers with the browser escape tests first, then
   storage and the panel, then the tool.
