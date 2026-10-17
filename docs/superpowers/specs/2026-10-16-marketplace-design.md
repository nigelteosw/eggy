# Marketplace: reviewed installs from existing catalogs

Status: proposed; not approved for implementation.
Date: 2026-10-16.

## Outcome

An owner can browse the skill catalogs other agents already publish — Claude
Code plugin marketplaces and plain `SKILL.md` repositories such as
`anthropics/skills`, `openai/skills`, and Hermes taps — install a plugin or a
skill from them in the panel, check for updates, and remove it again, without
copying files by hand.

The review that `AGENTS.md` calls the security model of skills is kept, not
deleted: nothing reaches `skills/` or `mcp.servers` until an owner has been
shown exactly what will be written, at an exact commit, and approved it. The
installer replaces *copying files over*, never *reading them*.

Eggy consumes catalogs. It does not publish one, host one, or define a catalog
format of its own.

## What the references do, and what Eggy takes

Researched from primary docs on 2026-10-16
([Claude Code marketplaces](https://code.claude.com/docs/en/plugin-marketplaces),
[plugin manifest](https://code.claude.com/docs/en/plugins-reference),
[hosting](https://code.claude.com/docs/en/plugins/host-marketplace),
[Hermes skills](https://hermes-agent.nousresearch.com/docs/user-guide/features/skills),
[Pi packages](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/packages.md)).

| Idea | Source | Eggy |
|---|---|---|
| `marketplace.json` catalog in a git repo, entries with a `source` | Claude Code | **Taken** as the catalog format to read. |
| `plugin.json` + default `skills/<name>/SKILL.md` layout | Claude Code | **Taken**, skills and remote MCP only. |
| `SKILL.md` folder standard (agentskills.io) | all three | **Taken**; requires directory skills (Delivery step 1). |
| Taps: a GitHub repo of skill folders as a source | Hermes | **Taken** as the `skills_repo` source kind. |
| Lockfile with source, content hash, scan result | Hermes | **Taken** as the SQLite install record. |
| Update skips locally edited files unless forced | Hermes | **Taken.** |
| Pin to a ref; a pinned ref never moves on update | Pi, Claude Code | **Taken**: every install is pinned to a commit SHA. |
| Background auto-update | Claude Code (opt-in) | **Declined.** Unattended turns would run instructions that changed after review. |
| Heuristic security scanner | Hermes | **Declined.** A scanner is weaker than the review kept here and would read as a guarantee it is not. |
| Hooks, commands, agents, `bin/`, LSP, monitors, extensions code | Claude Code, Pi | **Declined.** Eggy has no surface for them and runs no downloaded code. |
| stdio MCP servers from a plugin | Claude Code | **Declined** for now: a downloaded program running as Eggy's user with every secret Eggy holds. Listed as unsupported. |
| skills.sh, ClawHub, LobeHub, `/.well-known/skills` registries | Hermes | **Not now.** Each is another API adapter; revisit on demand. |
| Trust tiers | Hermes | **Not needed.** Every install is reviewed; there is no tier that skips review. |

## Scope

In:

- Directory skills: `skills/<name>/SKILL.md` plus text reference files, with
  flat `skills/<name>.md` still loading.
- A `marketplace` feature extension, gated by `marketplace.enabled`.
- Two source kinds, both on github.com, public repositories:
  - `claude_marketplace`: a repository with `.claude-plugin/marketplace.json`.
  - `skills_repo`: a repository (and optional path, default `skills/`) whose
    subdirectories each hold a `SKILL.md`.
- Discover, install, check-for-updates, update, disable-as-a-unit, and remove,
  all from the panel.
- Remote (streamable-http) MCP server entries carried by a Claude plugin,
  added to `mcp.servers` disabled.

Out:

- Publishing, hosting, or a catalog format of Eggy's own.
- Private repositories, non-GitHub hosts, and npm, `archive`, `url`, and
  `command` plugin sources. They are parsed and listed as unsupported.
- stdio MCP servers, hooks, commands, agents, executables, and any script file.
- Installing from Telegram or Discord, and any agent tool that installs. The
  model cannot install, update, or remove anything.
- Background update checks, notifications of new versions, and auto-update.
- Dependencies between plugins (`dependencies` in `plugin.json`).

## Design

### A plugin exists only at install time

After installation, a plugin is nothing at runtime. Its skills are ordinary
skills in `skills/`, read by the existing store, index, budget, and
`skill_read`. Its MCP servers are ordinary `mcp.servers` entries, governed by
the existing manager, `require_approval`, and `/mode`. The prompt never names a
plugin.

What groups them is the install record in SQLite, which exists so the owner can
update, disable, or remove what one install brought in as one unit. Deleting
every record would leave a working Eggy whose installed skills differ from
hand-placed ones only by a `source` frontmatter key (see *Index budget*), which
is the property that keeps this from being a second skill system.

### Directory skills (core, prerequisite)

`internal/skills/store.go` learns a second layout:

```
skills/
  weekly-review.md            # flat, unchanged
  pdf-forms/
    SKILL.md                  # frontmatter + body, same rules as a flat file
    reference.md              # text references, read on demand
    examples/fill.md
```

- A name present in both layouts is listed as invalid, with the reason, rather
  than one silently winning.
- `SKILL.md` keeps the existing 32 KiB bounded read. A reference file is bounded
  at 64 KiB, and a skill directory at 256 KiB and 64 files.
- Reference files must be UTF-8 text with an extension in a short allowlist
  (`.md`, `.txt`, `.json`, `.yaml`, `.yml`, `.csv`). Anything else in the
  directory is ignored by the store and never offered to the model.
- Frontmatter keys Eggy does not know (`license`, `allowed-tools`, `metadata`,
  `platforms`, ...) are preserved on disk and ignored. They grant nothing:
  `allowed-tools` in particular has no meaning in Eggy and lifts no approval.
- `skill_read` gains an optional `file` argument, a relative path inside the
  skill directory, validated against traversal and the allowlist. This is the
  only schema change, and it is small and serves hand-placed directory skills
  equally. The tool stays `ReadOnlyTool()`.
- Writes keep locking and atomic replacement. (The skills store applies no
  secret filtering today, and installed text comes from outside Eggy, so none
  is claimed here.)

This step is useful on its own and lands before anything marketplace-specific.

### The extension

`internal/extensions/marketplace/`, following the extension rules in
`AGENTS.md`. With `marketplace.enabled: false` — the default — bootstrap
passes nil: no route, no goroutine, no store, no prompt bytes, no outbound
request.

```yaml
marketplace:
  enabled: true
  sources:
    - name: anthropic-skills
      kind: skills_repo
      repo: anthropics/skills
      path: skills            # optional; default "skills"
      ref: main               # optional; default the repository's default branch
    - name: team-plugins
      kind: claude_marketplace
      repo: example-org/claude-plugins
```

Config lives in `internal/config/ext_marketplace.go`, validated there:
`name` is a slug and unique, `kind` is one of the two, `repo` is `owner/name`,
`path` is relative with no `..`. Per the settings rule, the section ships with a
settings card in the Customize page that edits sources through the one config
authority; nobody is told to edit YAML.

Components, all inside the extension package:

- `github.go` — the one outbound adapter. Resolves a ref to a commit SHA
  (`GET /repos/{o}/{r}/commits/{ref}`) and downloads a tarball at that SHA
  (`GET /repos/{o}/{r}/tarball/{sha}`). Fixed host `api.github.com`,
  unexported, for the same reason Google's endpoints are: a configurable host
  would be an exfiltration primitive that looks like configurability. It sends
  no credential in v1 — decided 2026-10-16: public repositories at 60
  requests an hour are enough for manual browse, install, and check, and no
  credential leaves with catalog traffic. `GITHUB_TOKEN` is the agent runner's
  push-capable token and is never sent here. No `git` process is ever run.
- `archive.go` — reads the tarball in a stream with hard limits: 20 MiB
  compressed, 64 MiB uncompressed, 5,000 entries, regular files only. A symlink,
  hard link, device, absolute path, or `..` component rejects the whole archive.
- `catalog.go` — turns one source at one SHA into a list of installable items.
  - `claude_marketplace`: parse `.claude-plugin/marketplace.json`. An entry
    whose `source` is a relative path, or a `github`/`git-subdir` source on
    github.com, is supported (the latter resolved and fetched as its own
    repository at its own SHA). Every other source type is listed with
    "unsupported source: npm" and cannot be installed.
  - Inside a plugin: `.claude-plugin/plugin.json` if present, else the
    marketplace entry, else the directory name; skills from the default
    `skills/` plus any manifest `skills` paths, or a root `SKILL.md` as a
    single-skill plugin; MCP entries from `.mcp.json` or `mcpServers`.
  - `skills_repo`: each subdirectory of `path` with a `SKILL.md` is one item.
- `plan.go` — computes an **install plan** for one item: every file that would
  be written, every file skipped and why, every MCP entry added or skipped and
  why, and a **plan digest** (SHA-256 over the canonical plan: source, SHA,
  item, every target path and content hash, every MCP entry). Pure function,
  no I/O; most of the tests live here.
- `service.go` — the one write path: Discover, Plan, Install, CheckUpdates,
  Update, SetDisabled, Remove. The panel is a thin caller.

### Mapping a plugin into Eggy

| Plugin content | Result |
|---|---|
| `skills/<n>/SKILL.md` and text references | Written to `skills/<n>/` through the skills store. |
| Scripts, binaries, images, other non-allowlisted files | Skipped: "Eggy never runs skill files". |
| `.mcp.json` entry with `url` (http) and no `headers` | Added to `mcp.servers` as `<plugin>-<server>`, `transport: streamable-http`, `auth: oauth`, `enabled: false`. |
| MCP entry with `headers`, `${CLAUDE_PLUGIN_ROOT}`, or `${user_config.*}` | Skipped: needs configuration Eggy cannot infer; the panel shows the entry so the owner can add it by hand. |
| MCP stdio entry (`command`) | Skipped: "runs a downloaded program". |
| `commands/`, `agents/`, `hooks/`, `bin/`, `.lsp.json`, monitors, themes, `userConfig` | Skipped: "not supported in Eggy". |

A skill whose body names a file the plan skips (a script, say `scripts/fill.py`)
is flagged in the review: "this skill refers to files Eggy will not install and
may not work". Much of `anthropics/skills` is instructions for running bundled
code, and installing it would add an index entry that cannot do what it says.
The flag is a warning, not a refusal.

Skill names keep their own name. A name that already exists — hand-placed or
from another install — makes the plan refuse with a conflict, and the owner may
choose a different name for that skill in the review (Hermes' `--name`). A plan
never overwrites a file it did not install.

### Install

1. The owner opens an item in Discover. The panel asks for its plan, passing
   any renames chosen for conflicting skills, so the digest covers the names
   that will actually be written; changing a rename asks for a fresh plan.
2. The service resolves the source ref to a SHA, fetches, and returns the plan
   with the full text of every file to be written, the skipped list with
   reasons, and the plan digest. A fetched archive is kept in a small in-memory
   cache (a few entries, 15 minutes), keyed by repository and SHA.
3. The owner approves. The request carries source, item, SHA, renames, and the
   plan digest the owner was shown.
4. The service rebuilds the plan at that SHA — from cache, or by fetching again,
   which gives the same bytes because the SHA addresses them — and refuses if
   the digest differs. The approval is bound to the exact content shown, the
   same principle as a payload-bound approval, without a staging directory.
5. Skills are written through the skills store; MCP entries through
   `internal/config` under its one file lock. Then the install record is
   committed. Skills take effect on the next turn. An added MCP entry is
   disabled and loads only once enabled, through the existing reload path; if
   that path needs a restart, the Installed view says "restart to load" rather
   than implying the server is live.

If a write fails midway, the service removes what it wrote for this install
and records nothing, so a failed install leaves no half-plugin. A failure
removing a written file is logged and listed so the owner can clean up.

An install is an owner action at a verified web session, like any other config
write. It is not an approval-gated tool call, because no tool exists: the review
screen is where the owner decides. It requires a session and passes CSRF checks.

### Install record

Additive tables in `internal/storage/sqlite/ext_marketplace.go`, created
`IF NOT EXISTS`; no `MachineStateVersion` bump.

```sql
CREATE TABLE IF NOT EXISTS marketplace_installs (
  id           TEXT PRIMARY KEY,
  source_name  TEXT NOT NULL,
  repo         TEXT NOT NULL,      -- resolved repository the item came from
  item         TEXT NOT NULL,      -- plugin or skill name in the catalog
  commit_sha   TEXT NOT NULL,
  plan_digest  TEXT NOT NULL,
  installed_by TEXT NOT NULL,      -- account
  installed_at TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  disabled     INTEGER NOT NULL DEFAULT 0,
  UNIQUE (source_name, item)
);
CREATE TABLE IF NOT EXISTS marketplace_files (
  install_id   TEXT NOT NULL REFERENCES marketplace_installs(id),
  kind         TEXT NOT NULL,      -- 'skill_file' | 'mcp_server'
  target       TEXT NOT NULL,      -- skills-relative path, or mcp server name
  content_hash TEXT NOT NULL,      -- hash as installed
  PRIMARY KEY (install_id, kind, target)
);
```

A skill's content hash is over its description, body, and reference files, not
its raw bytes, so toggling `disabled` or `source` in frontmatter does not read
as a local edit. Edits are tracked per skill, not per file: the store reads and
writes a skill as one unit, and so does the review.

Skills are deployment-wide, as they are today. The record notes who installed,
for the panel to show, and is not an ownership check: every account may update
or remove any install, as every account may edit any skill.

### Updates

There is no loop. **Check for updates** is a button, per source or for all:

- Resolve each source ref to its SHA. Same SHA: up to date. New SHA: for each
  install from that source, compute the new plan and diff it against the
  record.
- Show per install: files added, changed (with a text diff), removed, MCP
  entries added or removed, and **locally edited** skills — those whose current
  hash differs from the recorded one.
- **Update** is the same digest-bound approval as install. A locally edited
  skill is left alone unless the owner ticks "replace my edits" for that skill. An
  MCP entry the owner has edited in config is never replaced; a new MCP entry
  arrives disabled.
- A catalog entry that disappeared upstream marks the install "removed
  upstream". Nothing is deleted until the owner removes it.

### Enabling a plugin's MCP server

`require_approval` takes exact tool names, which a server reveals only once
connected, so an MCP entry cannot be gated at install time. Enabling is
where the trust decision is made, and it is made in **Customize → Connectors**,
the one home for MCP. A plugin's MCP server is an ordinary entry there, with a
"from <plugin>" label, not a second list in the Plugins tab.

1. A plugin-added entry shows as disabled with **Review and enable**.
2. That connects once without enabling and lists the tools the server
   advertises with their descriptions. The existing `Manager.Probe` cannot do
   this — it returns early for a disabled server and reports only a tool
   count — so the manager gains one method, `Preview(ctx, name)`, that opens a
   session from the entry's config, runs discovery, closes the session, and
   returns the tool names and descriptions without registering anything. An
   `auth: oauth` server needs its login first; the dialog runs the existing
   login flow before the preview when no grant is held.
3. The owner chooses which tools need approval. All are ticked by default:
   for a server from a catalog, the safe default is to ask.
4. Saving writes `enabled: true` and `require_approval` through
   `internal/config` in one locked write, then loads the server through the
   existing reload path. A server written to config after boot is unknown to
   the running manager, so `Preview` answers "restart to load" for it, and the
   dialog offers the restart instead of a tool list.

A server that advertises a tool later (`tools/list_changed`) gets the existing
warning path; a new tool is not in `require_approval` until the owner adds it,
exactly as for a hand-added server. Hand-added servers keep today's flow; the
picker is offered to them too, but nothing forces it.

### Installed skills on every turn

An installed skill is an ordinary skill after review. It is offered on every
turn a hand-placed skill is, unprompted turns included, where `skill_read` is
on the read-only allowlist and the turn still cannot mutate anything or reach
MCP. There is no runtime notion of where a skill came from, except for the index
budget's ordering below.

### Index budget: names stay, yours keep descriptions longest

Decided 2026-10-16, after comparing the references. Claude Code budgets its
skill listing at 1% of the context window, always lists every name, and drops
descriptions first from the least-invoked skills. Pi and Hermes document no
total budget; Pi caps a description at 1,024 characters, Hermes shrinks the
index by hiding skills per platform or when a tool already covers them. Eggy
today drops a skill that does not fit **whole**, name included, so the model
cannot know it exists. With installs able to add dozens of skills at once, that
is the wrong failure.

The 16 KiB `ports.SkillIndexBudget` stays. `SkillsService.Enabled` fills it in
two passes, in a fixed order, so the same skills on disk always render the same
bytes and the prompt cache is not disturbed:

1. **Names.** Every enabled, valid skill gets its name in the index. If the
   names alone exceed the budget, installed skills' names are dropped first, in
   reverse name order, then hand-placed ones, with the existing capacity
   warning. This is the only case in which a skill disappears from the index.
2. **Descriptions.** With what remains, descriptions are added: hand-placed
   skills in name order, then installed skills in name order. A description
   that does not fit is omitted, not truncated mid-sentence, and the next one
   is tried.

A name-only entry renders as `name: (description omitted; call skill_read)` so
the model knows it can still load it; `renderSkills` keeps its name-order output
and is otherwise unchanged. Claude Code's least-invoked ordering is declined: it
needs usage counters and reorders the prompt from turn to turn, which costs
cache hits.

Telling installed from hand-placed needs one fact at runtime, and it lives in
the file rather than the marketplace's tables, so the core never imports the
extension: the installer writes a `source: <source-name>/<item>` frontmatter
key into each `SKILL.md` it writes. `ports.SkillSummary` gains `Source string`
(empty for hand-placed). `SkillsStore.Write` preserves `source` as it already
preserves `disabled`, so editing an installed skill in the panel does not make
it look hand-placed; deleting the key by hand does, which is the owner's
choice. Like `disabled`, `source` is excluded from the content hash that
detects local edits. This is the one recorded exception to "a plugin exists
only at install time": a single frontmatter key, read only by the budget.

The install review shows the index bytes the install adds — names and
descriptions — and, when the index would overflow, which descriptions would be
omitted ("12 installed skills would be listed by name only").

This ordering helps hand-placed skills even with the marketplace disabled, so it
lands in Delivery step 1 with directory skills.

### Disable and remove

- **Disable** sets `disabled` on every skill the install wrote and
  `enabled: false` on every MCP entry it added, and records it, so the plugin
  turns off as one unit and turns back on to the state it had. Individual
  skills remain togglable in the Skills tab.
- **Remove** shows what will be deleted and asks once. It deletes the files and
  MCP entries the record lists, then the record. A locally edited skill is
  listed and kept unless the owner ticks it for deletion; a kept skill has its
  `source` key cleared, so it is hand-placed from then on.

### Panel

Customize gains a **Plugins** tab with **Discover** and **Installed** views.

- Discover: sources, their items, search across name and description, and an
  item page with the review (files with full text, skipped items with reasons,
  conflicts with a rename field) and **Install**.
- Installed: each install with source, SHA (short), installed-by and when,
  status (up to date, update available, removed upstream, locally edited),
  **Check for updates**, **Update**, **Disable**, **Remove**.
- The settings card for sources lives here, not in Settings.
- The review states, beside Install: "Skills are shared by every account.
  Their instructions reach the model on any turn where they match."

Routes in `internal/panel/ext_marketplace.go`, mounted only when the service is
non-nil, with `marketplace` added to `/api/session`'s `features` (never
`/api/mode`). SPA code under `website/src/extensions/marketplace/`.

| Route | Purpose |
|---|---|
| `GET /api/marketplace/sources` | sources and their resolved SHAs |
| `GET /api/marketplace/items?source=` | catalog items |
| `POST /api/marketplace/plan` | install or update plan with digest |
| `POST /api/marketplace/installs` | install, digest-bound |
| `GET /api/marketplace/installs` | installed list with status |
| `POST /api/marketplace/installs/{id}/check` | check one for updates |
| `PUT /api/marketplace/installs/{id}` | update, digest-bound |
| `POST /api/marketplace/installs/{id}/disabled` | disable or enable as a unit |
| `DELETE /api/marketplace/installs/{id}` | remove; returns a result body |

### Failure handling

| Failure | Behaviour |
|---|---|
| GitHub unreachable or rate-limited (60/h unauthenticated; listing a source costs a ref lookup and one tarball, and planning a plugin with its own `github` source costs two more) | The request fails with the HTTP status and reset time; installed skills are untouched. |
| Archive over a limit, or carrying a link or traversal | Whole archive rejected, with the rule it broke. |
| Malformed `marketplace.json` or `plugin.json` | The source or item is listed with the parse error; others still list. |
| Digest mismatch at approve | Refused: "the content changed since you reviewed it; review again". |
| Name conflict | Plan refuses until the owner renames. |
| Partial write failure | Rolled back as above; no record written. |
| Skill or reference over a size bound | That file is skipped with the bound named; the plan shows it. |
| MCP config write rejected by validation | The install is rolled back; the validation error is shown. |

## Changes to recorded decisions

- `AGENTS.md`, *Declined capabilities*: the "plugin marketplace or hub"
  paragraph is replaced by a *Decided* entry: Eggy reads existing catalogs
  through one reviewed, digest-bound, SHA-pinned install path; never automatic
  install or update; never downloaded code; the model cannot install. The
  runtime-loaded-plugins decline is unchanged.
- `2026-10-12-customize-design.md` listed plugins and directory skills as out of
  scope; this spec supersedes those two items. `TODO.md`'s Customize step 4
  (locally placed plugins with a hand-designed manifest) is replaced by this
  spec: the manifest is Claude Code's, and a plugin placed by hand is simply
  skills placed by hand.

## Delivery

Test-first, each step landing on its own, `make fmt vet test race build` at
each, `make smoke` where Docker is available.

1. **Directory skills.** Store layout, bounds, allowlist, invalid-on-collision,
   `skill_read` `file` argument. Tests: both layouts list; collision is
   invalid; a traversal in `file` is refused; a non-allowlisted file is never
   read; bounded reads at each limit; unknown frontmatter keys preserved.
   Index budget: every name listed before any description; hand-placed
   descriptions before installed ones; names dropped (installed first) only when
   names alone overflow; the rendered index is byte-identical across runs for
   the same files; `Write` preserves `source`; a name-only entry renders the
   `skill_read` hint.
2. **Archive and catalog parsing.** `archive.go`, `catalog.go`, `plan.go` with
   no network: fixture tarballs for `anthropics/skills`-shaped and Claude
   plugin-shaped repositories, and malicious ones (symlink, `..`, bomb,
   oversized). The plan digest is stable across runs and changes with any byte.
3. **GitHub adapter** against an `httptest` server: ref resolution, tarball
   fetch, rate-limit and error mapping, no credential sent, host not
   configurable.
4. **Service and SQLite.** Install, rollback on failure, digest refusal,
   conflict refusal, update with local edits kept, disable as a unit, remove,
   removed upstream. Bootstrap test: disabled extension registers no route and
   `features` lacks `marketplace`.
5. **Config and panel.** `ext_marketplace.go` config with validation and the
   sources settings card; routes with session and CSRF tests. The Connectors
   "Review and enable" flow: probe without enabling, tool list, one locked
   write of `enabled` and `require_approval`, all tools ticked by default.
6. **SPA.** Plugins tab, Discover, review, Installed. `bun test`,
   `bun run build`, and a browser check at phone and desktop widths against
   `anthropics/skills` and one real Claude plugin marketplace.
7. **Docs.** `AGENTS.md` decision entry, docs-site page, `TODO.md` cleanup.

## Budget

- Production Go: ~250–350 lines for directory skills and the index budget
  (step 1), ~800–1,100 for
  the extension (steps 2–5), ~100 for the Connectors enable flow. SPA: one tab and its views.
- Config keys: `marketplace.enabled`, `marketplace.sources` (with a settings
  card).
- Tools: 0 new. `skill_read` gains one optional argument.
- Durable records: 2 additive SQLite tables. Installed content lands in the
  existing Markdown and YAML forms; no fourth form.
- Background loops: 0.
- Cost when disabled: none — no route, goroutine, store, outbound request, or
  prompt bytes.
