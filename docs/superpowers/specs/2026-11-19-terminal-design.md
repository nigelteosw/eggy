# Terminal extension

Status: first version built, 2026-11-19. See *As built* at the end for what differs from this design.

A terminal in the web panel, running in the agent runner's sandbox, so the owner
can install and run Claude Code (or any CLI agent) inside Eggy and check on it
from a phone. The terminal engine is herdr-core (`hc`), run as a child process;
the look and the information model come from herdr-app.

## Why

Eggy's coding today is the agent runner: a mandate, a run, one branch, one pull
request. That is the right shape for "ship this change", and the wrong one for
"let me sit with Claude Code for an hour". The owner wants an interactive
terminal that keeps running when the phone locks and says when it is waiting.

Herdr (herdr.dev) is the best version of this we have seen. What it gets right:

- **Terminals live in a server, not a window.** Closing the client, losing the
  connection or updating leaves programs running; attaching again paints the
  exact current screen.
- **Status is reported, never inferred.** A program prints OSC 7501
  (`\e]7501;state=blocked:app=my-agent\e\\`); states include `idle`,
  `working`, `done`, `blocked`, `error`. No screen scraping, no list of known
  agents.
- **One list of what needs you.** An Inbox sorted needs-you → done → working →
  idle is the phone's home screen.
- **Phone input that works.** A key bar (esc, ctrl, alt, tab, ⇧tab, arrows,
  ^C) and a compose button that sends a whole line written in a normal text
  field.

Sources read: herdr.dev, herdr.dev/core, `herdrdev/herdr-core` (README,
`docs/security.md`, `docs/machines.md`, `schema/README.md`) and
`herdrdev/herdr-app` (README, LICENSE, `Cargo.toml`, `src/hc.rs`,
`src/control.rs`, `defaults/themes/`, `defaults/layouts/main.json`,
`defaults/layouts/terminals.json`, `defaults/keys.json`, `docs/mods.md`), plus
the owner's screenshots. Both repositories are Apache-2.0; neither grants the
Herdr name or logo. Not read: `hc.openrpc.json`, `defaults/AGENTS.md`.

## Decision: `hc` as the engine, not our own PTY layer

Owning terminals in Go (`creack/pty` plus a scrollback buffer) is ~800–1200
lines and still loses two things `hc` has:

1. **Survival across `eggyd` restarts.** `hc`'s holder processes own the
   terminals, so `/restart`, a config change or safe-mode recovery — all of
   which rebuild the App in-process — leave a Claude Code session running.
   Ours would be children of `eggyd` and die with it unless we rebuilt holders.
2. **Exact screen on reattach.** `hc` keeps a screen model and sends the
   current screen plus history as a first paint. Replaying a byte buffer into
   xterm.js garbles full-screen programs.

It also already parses OSC 7501 and streams events with a 15 s heartbeat and a
`missed` snapshot for a lagging subscriber, and it lets an agent in a terminal
start, wait on and read helper agents.

Cost: a pinned third-party 0.x Rust binary whose protocol may move. The
version is pinned by SHA-256 in the Dockerfile and the bridge refuses a
protocol generation other than the one it was written for.

Neither choice survives a container redeploy, and the panel says so.

This is an extension, not a plugin. Nothing from Herdr loads into `eggyd`; no
Herdr JavaScript (mods) runs anywhere; Eggy gains no plugin mechanism. `hc` is
a program in the image, like `git` and `node`.

## Shape

```
browser (xterm.js, key bar, compose)
  ⇄ WebSocket over HTTPS, verified panel session
  ⇄ eggyd: internal/extensions/terminal (bridge, account checks, inbox)
  ⇄ pipes: `hc connect` (framed JSON-RPC), as coder, under Landlock
  ⇄ hc core + holders → bash / claude / codex
```

`eggyd` is `hc`'s only client. The browser never speaks `hc`'s protocol and
never reaches a socket.

### Process and sandbox

- Started only when `terminal.enabled` is true **and** the sandbox probe
  passed. Without a passing probe the extension is constructed as nil, exactly
  as bash is withheld from the agent runner.
- `eggyd` runs `hc connect` through the existing `sandbox-exec` entry point, as
  `coder`, Landlock-confined, with an environment built from nothing
  (`runEnv`-style: `PATH`, `HOME`, `TMPDIR`, `LANG`, `TERM=xterm-256color`,
  `HC_SESSION=eggy`). Never `os.Environ()`.
- Writable tree: `<data>/terminal/{ws,home,tmp}`, owned by `coder`, prepared
  the way `PrepareRunDir` prepares a run. `hc`'s state lives under `home`.
  Claude Code's login (`claude login`, subscription auth) lands in
  `home/.claude` on the `/data` volume; Eggy never reads it.
- Network: the sandbox's existing rule — connect to TCP 443 and 80, bind
  nothing. Claude Code needs only 443.
- `hc connect` starts the `hc` core on demand; holders outlive `eggyd`. On
  startup the bridge reconnects to the running core rather than starting a
  second one.

**Sandbox changes this needs** (the one real modification outside the
extension):

1. The sandbox lives in `internal/extensions/agentrunner`, and one extension
   must not import another. Move `Sandbox`, `sandbox-exec`, the probe and the
   Landlock rules to `internal/sandbox`, imported by both. This is a package
   split with two real callers, not an export to satisfy a boundary.
2. A long-lived mode: no `defaultCommandTimeout`, no output capture (the
   bridge reads the pipes), process-group kill on extension stop. A run's
   limits stay constants for the agent runner.
3. Landlock ABI 6 signal and abstract-socket scoping stays on; `hc`'s own
   socket is a pathname socket inside `home`, which `fsAll` already covers.

### `hc` protocol (as `src/hc.rs` uses it)

- Frames: 9-byte header — channel `u32 LE`, kind `u8`, length `u32 LE` — max
  16 MiB. Kinds: DATA 0, CREDIT 1, CLOSE 2, RESIZE 3, FOCUS 4, FACTS 5,
  AUTOMATIC_INPUT 6. Channel 0 is JSON-RPC 2.0.
- Startup: `hello {"framed": true}`, check the generation, `events.subscribe`.
- Methods used: `terminal.run`, `terminal.list`, `terminal.kill`,
  `terminal.label`, `terminal.attach`, `terminal.keep`, `events.subscribe`.
  (`doc.get`/`doc.set` are not needed in the first version.)
- Input is DATA on an attached channel; resize is RESIZE (`cols`, `rows`,
  `u16 LE`). No RPC for either.
- To confirm against `hc schema` before implementing: parameter names for
  `terminal.run` and `terminal.attach`, CREDIT flow control, and FACTS.

### Ownership

Every terminal is labelled at `terminal.run` with `eggy.account=<id>` and
`eggy.space=<name>`. The principal comes from the verified session, as
everywhere; a label is never taken from the request. Every list, attach, write,
resize and kill checks the label against the session's account and refuses
otherwise. One person cannot see or type into another's terminal.

### Panel API (mounted only when the extension is non-nil)

- `GET /api/terminal/spaces` — spaces (see below) with their terminals and
  status.
- `POST /api/terminal` — `{space, command}` where `command` is one of a fixed
  list (`shell`, `claude`, `codex`) plus the owner's own saved commands. Returns
  the address.
- `DELETE /api/terminal/{addr}` — kill.
- `GET /api/terminal/{addr}/attach` — WebSocket. Client → server: input bytes,
  `{"resize": [cols, rows]}`. Server → client: first paint, then output.
  `history: 2000` on attach.
- `/api/session` lists `terminal` in `features`; `/api/mode` does not.

No model tool. The terminal is for the owner, not for Eggy; a tool that typed
into it would be model output driving a shell outside the agent runner's
mandate.

### Spaces

A space is a folder under `ws/`: an empty one, or a clone of a configured
repository. The clone is made by `eggyd` from files, through the agent
runner's existing checkout path — never by running git with `ws/.git/config`
the agent controls. The space shows its branch, read from files.

### Status and "Needs you"

- `status` events update an in-memory map: terminal → state, title, last
  change. Nothing durable in the first version.
- The home page's "Needs you" and the panel's Inbox are one list: approvals,
  agent-runner runs in `waiting_for_user`, and terminals whose last state is
  `blocked`. That folds in the TODO item "Needs you includes waiting coding
  runs".
- Claude Code does not emit OSC 7501 itself. Ship a small hook script in the
  terminal's home that prints it from Claude Code's `Notification` and `Stop`
  hooks; the owner can delete it. Programs without a hook simply show no
  status.
- Optionally a Telegram/Discord notice, "a terminal needs you", as a pointer to
  the panel. Never a way to answer it.

## What the push rule means here

The terminal has no push or pull-request capability. Nothing changes the
safety invariant that only the agent runner publishes, under a mandate: no git
credential reaches `<data>/terminal`, and GitHub is not reachable with any
Eggy token from inside it. If the owner wants terminal work shipped, they use
`/code run` against the space or copy the change out. Claude Code's own
permission mode inside the terminal is the owner's business, as on their
laptop.

## UI

Code lives in `website/src/extensions/terminal/`. Copied: layout, behaviour,
density. Not copied: name, logo, the "Herdr 98" theme, mods.

**Desktop** (after `defaults/layouts/main.json`):

- Left sidebar 268 px: Spaces (letter avatar, name, branch, status dot or
  "Needs you" pill, "+" to add), then Agents grouped by space (name, age such
  as `<1m`, status), settings at the foot.
- Work area: 42 px tab bar (status mark per tab, "+"), panes with a 30 px
  header (program, folder, spinner) and 10 px radius, split right / down.
- Optional 368 px right panel: Files and Changes for the space, read-only.
- 28 px status bar.
- Clicking an agent with no pane opens a peek overlay over the current tab.

**Phone** (after the screenshots):

- Home is the Inbox: search ("Jump to a terminal…"), then Needs you, Done,
  Working, Idle. A bottom switch between Inbox and Spaces.
- Terminal view: title and space in the header; key bar above the keyboard —
  esc, ctrl, alt, tab, ⇧tab, arrows, ^C, keyboard toggle, compose. Compose
  opens a native text field (autocorrect, dictation, paste) and sends the
  text plus Enter.
- Tablet: sidebar beside the terminal.

**Keys.** Herdr's Ctrl-Shift set collides with the browser (T, W, N). Defaults
use Alt: Alt-1…9 space, Alt-h/j/k/l pane focus, Alt-↑/↓ agent, Alt-Shift-O /
Alt-Shift-E split, Alt-Shift-Z zoom, Alt-Shift-B sidebar. The palette is the
panel's existing one if any, otherwise Alt-Shift-P.

**Look.** Through the themes design: add a terminal palette to the theme
tokens — `background`, `foreground`, `selection`, `cursor`, 16 `ansi` — the
exact shape xterm.js takes and Herdr's theme files use. Built-in dark values
from `terminals.json`: background `#131619`, surface `#17191c`, border
`#25282c`, foreground `#e8e9eb`, accent `#3b82f6`; status amber (needs you),
blue (done), spinner (working). Monospace 15 px. Data only, checked by the
existing contrast rule.

Layouts are not owner-editable in the first version. If they become so, they
are data — a tree of `row`/`column`/`block`/`stack` with `px` or `weight`
sizes, as Herdr's are — validated on save, never code.

## Config

```yaml
terminal:
  enabled: false
```

`internal/config/ext_terminal.go`, with a settings card (enable, sandbox probe
result, `hc` version, "terminals stop when Eggy is redeployed"). Saved commands
can come later.

## Declined for now

- **The Herdr phone app connecting directly** over SSH into the container. It
  would be a second ingress that skips Eggy's session and account checks, and
  an SSH login lands outside Landlock unless forced into it. `hc` says of
  itself that it "is not a sandbox or a permission broker". Revisit as its own
  ingress design.
- **`hc machine add`** (remote machines). Eggy is one server.
- **A model tool** for terminals (see above).
- **Durable terminal records.** Status is in memory; `hc` holds the rest.

## Cost

- Go: ~300–500 lines in `internal/extensions/terminal` (frame codec, bridge,
  WebSocket route, inbox), ~150 moved into `internal/sandbox`, config and
  panel wiring in `ext_terminal.go` files.
- Web: xterm.js (pinned) plus the views above; most of the size is the phone
  key bar and the sidebar.
- Image: the `hc` binary, pinned by SHA-256.
- 1 config key, 0 tools, 0 SQLite tables, 1 child process and 1 event loop
  when enabled; nothing when disabled.

## Tests

- Frame codec round-trip and the 16 MiB limit.
- Bridge against a fake `hc` on pipes: hello and generation refusal,
  `terminal.run` labels taken from the principal, attach first paint, DATA and
  RESIZE, `missed` resync, core restart reconnect.
- Ownership: another account's list, attach, write and kill are refused.
- Disabled extension: no route, no `features` entry, no process.
- Probe failure: extension nil.
- Bootstrap: environment passed to `hc` contains no secret value
  (`Secrets.Values()`), and nothing from `os.Environ()`.
- `make smoke`: a real `hc` in the image runs `echo`, survives an in-process
  restart, reattaches with the same screen.

## Open before implementing

1. Read `hc schema` for exact parameters and CREDIT flow control.
2. Confirm `hc` runs with no systemd, no `XDG_RUNTIME_DIR` and no lingering:
   set `HC_SOCKET` under `home` explicitly.
3. Confirm holders survive the supervisor rebuilding the App (they should: the
   core is a separate process and the bridge only reconnects).
4. Confirm `hc` works under Landlock ABI 6 with the current read-only trees,
   and what extra paths it needs, if any.

## As built (2026-11-19)

Checked against the real `hc` 0.1.21 before writing code, in a Linux container
under the real sandbox: the protocol matches `schema/hc.openrpc.json`; `hc`
needs no systemd or `XDG_RUNTIME_DIR` once `HC_SOCKET` is set; holders outlive
both the client and the core, and a later `hc` reconnects to them; under
Landlock it needs `/dev/urandom` (it panics without it), `/dev/ptmx`,
`/dev/pts` and `/dev/tty`. End to end in a native container, a terminal
survives `POST /api/restart` with the same address and process.

Where the build departs from the design above:

- **One core and one uid per account, not one core with account labels.**
  Whoever reaches a core's socket has everything in it, so a label check in
  Eggy would not keep accounts apart. Each account gets its own directory
  (named by an HMAC of its id keyed with `EGGY_ENCRYPTION_KEY`), its own
  core, and its own uid from 200000, with passwd entries added at first use.
  The uid is what matters: Landlock grants all of `/dev/pts`, so with one
  shared uid any account could read another's ptys. Terminal mode has its
  own startup probe, run only when terminals are enabled. `terminal.enabled` therefore
  requires the key at config load.
- **Pipes, not a socket.** `eggyd` runs `hc connect` confined and speaks over
  its stdin and stdout, as herdr-app does, so `eggyd` never needs to reach
  `coder`'s socket.
- **Spaces are empty folders** made through the core (`machine.exec mkdir`).
  Cloning a configured repository into one is not built; the owner can
  `git clone` a public repository from the shell.
- **The command is a label** (`eggy.command`), not the terminal's `hc` name,
  because names must be unique among live terminals.
- **WebSocket through `gorilla/websocket`**, already in the module graph via
  discordgo; the standard library has no WebSocket server. The upgrade checks
  the panel's own origin rule and requires an Origin, since the session
  guard's CSRF check does not run on a GET.
- **The UI is the first slice**: desktop sidebar (Inbox and Spaces) with tabs
  and one pane; phone Inbox, full-screen terminal, key bar and compose. Not
  built yet: split panes, the Files and Changes panel, the status bar, peek,
  the Alt shortcuts, the palette as theme tokens (it is a fixed dark palette),
  "Needs you" on the home page (the route exists: `GET
  /api/terminal/needs-you`), and the Telegram/Discord pointer.
- **xterm.js loads lazily**, so an account that never opens a terminal does
  not download it.
- **Fixed on the way:** `sandbox.Run` let a backgrounded child outlive its
  command (see `AGENTS.md`), and the root-only sandbox tests had never
  actually run.
