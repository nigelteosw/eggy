# ADR 0005: Approval-gated `SKILL.md` files for procedural skills

## Context

`TODO.md` already commits Eggy to a lightweight, Markdown-based procedural-skill
format: no agent framework, no native plugin runtime, no arbitrary executable
extensions; only compact skill metadata stays in ordinary context, with full
instructions loaded when the current task matches; skills are agent-proposable
after a successful complex workflow, a recovered failure, or an owner
correction, but require explicit owner approval to create, edit, or delete;
installed skills must be inspectable and removable through deterministic
commands.

Three independent ecosystems already implement procedural skills this way —
Hermes Agent (Nous Research), pi (pi.dev), and Claude Code's own
`obra/superpowers` plugin and Anthropic's `skill-creator` — and they converge
on the same open format, published at `agentskills.io`: a folder per skill
containing `SKILL.md` with YAML frontmatter (`name`, `description` required;
optional fields vary by host) plus a Markdown body, optionally bundling
`scripts/`, `references/`, and `assets/`. At runtime only `name`+`description`
stay resident; the full body loads on demand once a task matches the
description (pi calls this on-demand fetch, Anthropic calls it progressive
disclosure).

Eggy already has an adjacent primitive: `internal/adapters/context/markdown`
edits `SOUL.md`/`USER.md`/`MEMORY.md` as single documents addressed by `##
section`, with atomic+locked writes, a byte cap, and `SecretGuard` validation,
exposed as agent tools (`soul_append`, `memory_replace_section`, etc.) that the
model calls directly with no approval step. That shape fits stable facts and
identity. It does not fit skills: each skill is its own file rather than a
section of a shared document, and — unlike a stored fact — a skill's body is
instructions that later steer the agent's own tool calls, which is why the
roadmap already asks for an owner-approval gate here that memory/user/soul
edits don't have.

## Decision

Skills are stored as flat files, `data_dir/skills/<name>.md`, one skill per
file — no bundled `scripts/`/`assets/` subfolders, since Eggy has no plugin
runtime to execute them. Frontmatter carries only `name` (slug, validated the
same way `sectionPattern` validates a section heading) and `description` (the
trigger condition); a skill may still reference other repository paths in its
body for the agent's existing `read_file` tool to open, but ships no
executable of its own.

A new `skills.Store` adapter (sibling to `markdown.Store`) exposes:

- `List` — name + description only, for the compact index injected into
  ordinary context alongside tool schemas.
- `Read(name)` — full body, returned only when fetched.
- `Write(name, content)` — create-or-replace, atomic+locked, size-capped,
  validated by `SecretGuard` and the name pattern.
- `Delete(name)`.

`skill_read` is a plain agent tool, callable like `memory_read`: the model
pulls one skill's full body into the turn when its description matches the
current task. `skill_write` and `skill_delete` are **not** directly callable
by the model. Creating, editing, or deleting a skill goes through
`ApprovalService.Request`/`Decide`, the same digest-bound approval flow already
used for Calendar mutations and `add_repository`: the agent proposes a name,
description, and body (after a successful workflow, a recovered failure, or an
owner correction), Eggy issues an expiring approval, and only an owner Telegram
tap or CLI confirmation executes the write.

Owner-direct editing goes through a `/skills` command family mirroring
`/memory`: `/skills` (list), `/skills show <name>` (full body), `/skills add
<name> <description> <content>` and `/skills edit <name> <content>`, `/skills
remove <name>`. Owner-initiated adds/edits/removals open the same approval
request the agent path uses rather than writing immediately — Telegram/CLI
text input has no undo, and skills share the stricter gate the roadmap already
specifies for this content type.

**Enable/disable** does not go through approval. A disabled skill's file is
untouched; only its name is added to a `DisabledSkills` set in `state.json`,
next to approvals and schedules. Because this only changes whether a
already-approved skill is surfaced, not its content, `skill_disable` and
`skill_enable` are freely agent-callable tools, the same trust level as
`memory_append` — reversible, capability-reducing-or-restoring, no new
content enters the system. Disabled skills are dropped from the compact
index and from the steering list below, but remain readable by exact name via
`/skills show` or `skill_read`.

**Cloning from an external repository** is not a bulk importer. `/skills
browse <repo-url>` is read-only: it lists `**/SKILL.md` paths found in that
repo without installing anything. `/skills clone <repo-url> <path>` fetches
exactly the one file at that path and opens the same approval request the
agent-authored path uses, with the fetched body attached — the owner reviews
actual text, never a bare URL. Fetches are size-capped and single-file, the
same trust boundary Eggy already applies to configured repositories; nothing
from the source repo is cloned, checked out, or executed. This is
deliberately narrower than Hermes's `hermes skills install provider/repo/...`
or pi's npm-package skills, both of which can pull in many skills per
command — that would turn "explicit owner approval before creating a skill"
into rubber-stamping a batch.

**Steering the agent to actually use installed skills** reuses the existing
capability-manifest mechanism in `internal/kernel/agent/prompt.go` rather than
adding a new one. `CapabilityManifest` gains the enabled skills' `name:
description` pairs; `BuildInstructions` renders them as their own system
message alongside the current capability manifest, and `hardRuntimePolicy`
gains one steering line: check that list before non-trivial work, call
`skill_read` on a match, and follow the loaded instructions unless they
conflict with hard policy or the current owner's instructions. This is Eggy's
equivalent of `obra/superpowers`'s `using-superpowers` bootstrap skill, which
forces Claude Code to check for a matching skill before acting — except Eggy
does not need a nested meta-skill for it, since it already owns and renders
its own system prompt every turn instead of relying on an opt-in plugin
convention.

Importing a skill from an external collection (`superpowers-skills`,
`hermes-skills`, `pi-skills`) works through either `/skills add` with a pasted
body or `/skills clone`; only the `name`/`description` pair has to match, so
cross-ecosystem skills carry over without a converter, but any
`scripts:`/`assets:` bundle they ship is dropped since Eggy has nothing to run
it with.

## Consequences

Skills become a fourth approval type (with Calendar and `add_repository`)
rather than reusing the freely agent-writable memory path, which costs one
extra owner tap on the "propose a skill after a successful workflow" flow the
roadmap wants — deliberate, since an unreviewed skill body is closer to
durable policy than a stored fact and can steer later tool calls if it
contains injected instructions.

Because bundled scripts/assets are out of scope, skills copied verbatim from
Hermes/pi/superpowers repositories will need any executable step rewritten as
instructions for Eggy's own `read_file`/`terminal`/`patch`/`write_file` tools;
only the Markdown-instruction portion ports directly.

Keeping only `name`+`description` resident in context means the compact index
scales with skill count the same way tool schemas already do; the cost of a
full body is paid only on the turn a skill actually fires.

Splitting trust this way — free toggling, gated content, single-file gated
cloning — means disabling a misbehaving skill is a same-turn agent action
with no owner round-trip, while anything that changes what a skill actually
says still waits on a tap, including content that originated outside Eggy.
The trade-off is that `/skills browse` still has to reach an external host to
list paths, which is new outbound network surface Eggy's other read paths
(configured repositories, calendar, provider APIs) don't otherwise need for a
feature this narrow.
