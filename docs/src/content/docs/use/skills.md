---
title: Skills
description: Teach Eggy a procedure once by writing a Markdown file, and have it loaded only on the turns that need it.
eyebrow: Use Eggy
---

A skill is a procedure written down: how you want a recurring job done, in your
words, in a Markdown file. Eggy carries a one-line summary of each one in every
prompt and reads the full instructions only when the task matches, so a long
procedure costs a line of context until the moment it is useful.

Skills are files in `skills/` under the Eggy home. There is no registry, no
installer, and no download step — **you put the file there, and that placement
is the review**. Eggy reads a skill; it never executes one.

## Write a skill

One file per skill, named for the skill, with a small YAML frontmatter block
followed by the instructions:

```markdown
---
name: weekly-review
description: Compile the Monday review from the open PRs, the calendar, and last week's notes.
---

1. List open pull requests on the eggy repository with `repository_github`.
2. Read this week's calendar with `google_calendar`.
3. Draft the review as a short list of decisions needed, not a status dump.
4. Send it as a message. Do not create a document unless asked.
```

Save it as `skills/weekly-review.md`.

- **`name`** must match `^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]?$` — 1–64 lowercase
  letters, digits, and hyphens. The name is the filename, which is why it is
  restricted: a name that needs sanitizing before it can be a path is a name
  worth rejecting instead.
- **`description`** is the whole basis on which a skill is chosen. It is at most
  1024 bytes and it is the only part of the skill the model sees before deciding
  to load it, so write it as the trigger — *when* to use this — not as a title.
- The body is ordinary Markdown, up to 32 KB per file.

Changes take effect on the next turn. Skills are read from disk each time the
index is built, so adding, editing, or deleting one needs no restart.

## How a skill reaches a turn

The prompt carries an **Available skills** index: every readable skill's name and
description, one per line, sorted by name. When a description matches the task,
the model calls `skill_read` with the exact name and receives the full body.

That two-step is the entire mechanism, and it is what makes a large skills
directory affordable: bodies are never resident, only summaries are.

The index itself is bounded at 16 KB of summaries. Once it is full, the
remaining files are skipped and each one is named in a warning in the logs — an
index at capacity is reported, never silently trimmed. A file that is oversized,
unreadable, or has malformed frontmatter is skipped the same way, with a warning
naming it, so one bad file cannot take out the skills that still work.

## What a skill cannot do

A skill is instructions, not authority.

- It cannot grant a tool. If `google_gmail` is not configured, a skill that says
  to send mail has nothing to call.
- It cannot lift an approval. A step that writes still asks in `normal` mode,
  and every step asks in `strict`. See [Approvals](/eggy/use/approvals/).
- It cannot override hard runtime policy or `SOUL.md`.

This is why Eggy has no skill marketplace and no installer. The security model
*is* that a skill is a file you wrote or read before saving; fetching
agent-readable instructions from the internet would delete it.

## Where they live

`skills/` sits in the Eggy home beside `memories/` and `eggy.db`, with mode
`0700`. Back it up as ordinary text along with `config.yaml` and `memories/` —
see [Persistence and memory](/eggy/operate/persistence-memory/).
