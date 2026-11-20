# One workspace per account: the agent runner and the terminal share it

Status: steps 1 (layout, runs as the account), 2 (mirrors) and 3 (repository spaces, space delete) built 2026-11-20.

## Why

Today the agent runner and the terminal are two worlds. A coding run clones
a configured repository into `/data/agent-runs/<run>/` and runs as `coder`;
a terminal runs as the account's own uid in `/data/terminal/<hash>/`, where
a space is an empty folder. The terminal cannot see the repositories or the
runs, and a run cannot see anything done in a terminal. The owner wants one
place: onboard a repository once, work in it from a terminal (with Claude
Code), start agent runs on it, open a shell inside a run to look or take
over, and see everything waiting on them in one list.

## Goals

- A repository listed under `repositories:` is a space in every account's
  terminal, already checked out. No manual `git clone`.
- Agent runs live in the same account workspace, as the same uid, so a
  terminal can open in a run's checkout.
- One "Needs you" list: blocked terminals and runs in `waiting_for_user`.
- Shipping still happens only through a run's mandate. The terminal gains no
  credential and no push.
- Running locally means running the image (`docker run` / `make smoke`), and
  then it behaves exactly as on Railway.

## Non-goals

- Native macOS. The sandbox is Landlock, which is Linux only; on a Mac
  `eggyd` keeps offering neither bash nor terminals. Local use is the image.
- Sharing a workspace between accounts. Accounts stay apart by uid.
- Letting a run and a person edit the same checkout at once.
- A push or pull request from the terminal.

## Layout

```
/data/workspace/                       root, 0711
  <hash>/                              one per account, root, 0711
    home/                              account uid, 0700: logins, tools, ~/.claude
    tmp/                               account uid, 0700
    ws/                                account uid, 0700
      <repo>/                          the owner's working copy of a repository
      <space>/                         a plain folder, as today
    runs/                              root, 0711
      <run-id>/                        root, 0711 (as a run directory is today)
        ws/                            account uid: the run's checkout
        tmp/                           account uid
        base/                          root, 0700: the publisher's own checkout
/data/mirrors/<repo>.git               root, 0755 dirs, 0644 files: read-only to everyone
```

`<hash>` is today's HMAC name. The account's uid is today's terminal uid
(from 200000, read back from `home/`'s owner).

**One home.** A run uses the account's `home/` rather than its own. That is
what lets a run use the Claude Code or Codex login the owner made in a
terminal, and it is the one real widening of a run's reach: a run can now
read and write the account's home. That is acceptable because the run acts
for that account, under that account's mandate, and the home holds nothing
of anyone else's; it is argued under *Safety* below.

## Repositories: mirrors, then working copies

Private repositories need `GITHUB_TOKEN`, which must never enter a sandbox.
So the token stays with one root-only operation, the mirror:

1. **Mirror.** For each configured repository Eggy keeps a bare mirror at
   `/data/mirrors/<name>.git`, fetched as root with the existing askpass
   path (`gitEnv`): at boot, when a repository is added, before every run's
   clone, and when the owner presses **Update** on the space. The mirror is
   readable by every terminal uid and writable by none. Its `config` is
   written by Eggy and nothing else touches it; Landlock adds
   `/data/mirrors` as a read-only tree in both modes.
2. **Working copy.** The first time an account's core starts, and whenever a
   repository is added, Eggy makes `ws/<repo>` for each repository that
   account does not have, by running `git clone /data/mirrors/<repo>.git`
   **inside the sandbox as the account's uid** (through the core, like
   `CreateSpace`'s mkdir). Git never runs as root on a tree the account
   controls. Its `origin` is the mirror path, so `git fetch` and `git pull`
   in the terminal work without a credential, and `git push` fails because
   the mirror is not writable.
3. A working copy is never touched again by Eggy: it is the owner's. A
   repository removed from config leaves the folder as an ordinary space.

## Agent runs in the workspace

What changes for a run:

- **Where.** `runs/<run-id>/` under the account that started it, not
  `/data/agent-runs/`.
- **Who.** The run's commands run as the account's uid, not `coder`, in the
  sandbox's command mode (no ptys), confined to `runs/<run-id>/` plus the
  account's `home/` and `tmp/`. Landlock still denies the rest of the
  account's `ws/`, so a run cannot edit the owner's working copies.
- **Clone.** As today, root clones into the run's `ws/` before handing it to
  the uid, but from the mirror, so the token is used once, by the mirror
  fetch, and never in a run directory. Hooks stay disabled.
- **Publish.** Unchanged in substance: the change set is computed from files
  against `base/`, root never runs git in `ws/`, and push and pull request
  are re-checked against the mandate. `base/` moves into the run directory as
  a root-only sibling, where Landlock does not reach.

What stays: one run, one checkout, one branch `eggy/<run-id>`; the mandate;
the checks; the budget; `waiting_for_user`.

## The terminal sees runs

- The Terminal page lists runs under their repository's space with a
  distinct mark: running, waiting for you, done, failed.
- **Open a shell here** on a run starts a terminal in the run's `ws/`, in
  terminal mode, as the same uid. While the run is active the shell is
  read-only by convention only, so the page says "the agent is working in
  this folder"; once the run is waiting or done it is the owner's to edit,
  and a later **Continue** hands the folder back to the agent.
- A terminal opened in a run is confined to that run's directory, home and
  tmp, exactly like the run, so it cannot reach the owner's working copies
  either.

## Shipping from a working copy

A repository space gets **Ship as a run**: Eggy creates a run whose checkout
is the owner's current changes (a file copy of `ws/<repo>`'s tracked and new
files onto a fresh clone of the base branch, made by root from files, never
by running git in `ws/<repo>`), and the run takes it from there: checks,
then push and pull request under a mandate created by that click, which is a
verified web session. The terminal never pushes.

## Needs you

`GET /api/terminal/needs-you` and the agent runner's waiting runs are listed
together on the home page and at the top of the Terminal page. This is the
`TODO.md` item "Needs you includes waiting coding runs", done once for both.

## Safety

- **Accounts stay apart by uid.** Everything an account owns, runs
  included, belongs to its uid; another account's programs cannot read it,
  open its ptys or signal its processes. This was already true of terminals
  and becomes true of runs, which today share `coder`.
- **The token stays in one place.** Only the root mirror fetch holds
  `GITHUB_TOKEN`. Run clones and working copies read the mirror.
- **No git as root on agent- or owner-controlled trees.** Root runs git only
  in mirrors and in `base/`. Working copies are cloned by the account's uid
  inside the sandbox.
- **No new way to ship.** Push and pull request remain the mandate's, re-
  checked before each call. Ship as a run creates a mandate at a verified
  session like any other web-started run.
- **The widening, named.** A run now shares the account's home, so a run
  can read the owner's Claude Code login and anything else they put there,
  and leave files there. A prompt-injected run could use that login to call
  the model on the owner's account, which it can already do through the
  run's own model; it cannot use it to push, because nothing in the home can
  write to GitHub. If that is not acceptable, the fallback is a per-run home
  with the owner's `~/.claude` login copied in read-only.
- **Probe.** The probe runs once in command mode and once in terminal mode,
  as a terminal uid, and adds: a run cannot write the account's `ws/`, and
  cannot write `/data/mirrors`.

## Migration

At boot, once, before the lock and probe:

- `/data/terminal/<hash>/` becomes `/data/workspace/<hash>/` (a rename; the
  uid and contents are unchanged, live terminals included, since the core's
  socket path is read from `HC_SOCKET` at connect time. Live cores are
  restarted, their programs keep running in their holders).
- An active or waiting run in `/data/agent-runs/<id>/` moves to its owning
  account's `runs/<id>/` and is chowned from `coder` to the account uid, its
  `home/` merged into the account's only if the account's is empty, else
  kept as `runs/<id>/home-legacy/`. Finished runs are left where they are
  and pruned as today.
- Mirrors are created on first fetch.

`/data/eggy.db` changes only by a column naming the run's directory if the
path cannot be derived; no `MachineStateVersion` bump if it can.

## Cost

- ~600-900 production lines: mirrors (~150), workspace layout and migration
  (~200), runs as account uid (~150), terminal listing and opening runs
  (~200 web, ~80 Go), Ship as a run (~150), Needs you (~60).
- 0 new config keys: repositories stay in `repositories:`, gated by the two
  existing `enabled` keys. With the agent runner off there are no runs and
  no mirrors' run path; with the terminal off nothing is listed.
- 0 new tools, 0 new background loops (mirror fetches happen on events).

## Order

1. Workspace layout, uid-per-account runs, migration. Agent runs work as
   before from their new home. Ship.
2. Mirrors; runs clone from them.
3. Repository spaces in the terminal (working copies from mirrors).
4. Runs listed in the terminal; Open a shell here.
5. Needs you, both kinds.
6. Ship as a run.

## Open questions

- Shared home vs per-run home with a copied login (see *Safety*). The design
  assumes shared; it is the simpler one and what makes "use my Claude Code
  login in a run" work.
- Whether **Continue** after the owner edits a waiting run needs its own
  approval, or is just the owner's next message to the run.
