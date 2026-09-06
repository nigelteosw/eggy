---
title: Tool catalog
description: Every tool Eggy can call, what registers it, and whether it asks before it runs.
eyebrow: Use Eggy
---

Eggy's tool surface is deliberately small, and most of it is conditional: **a
capability that is not configured costs nothing** — no schema on any model call,
no store, no goroutine. The panel's **Settings → Capabilities** page lists the
tools your instance actually has, in the order the model sees them, and reads
live from the registry the agent loop runs on.

Each tool declares its own effect, which is what
[`/mode normal`](/eggy/use/approvals/) filters on: `read` runs without asking,
`write` asks first, and an unclassified tool counts as a write.

## Always present

| Tool | Effect | What it does |
| --- | --- | --- |
| `status` | read | Bounded operational status: model, approvals, schedules |
| `current_time` | read | The current time in `agent.timezone` |
| `recall_conversation` | read | Search bounded historical conversation, up to 10 results, never injected automatically |
| `memory` | internal | Add, replace, or remove one entry in `MEMORY.md`, `USER.md`, or `WATCH.md` |
| `skill_read` | read | Load one [skill's](/eggy/use/skills/) full instructions by exact name |
| `heartbeat_respond` | internal | A beat's own answer: notify or stay silent, when to look again, and its watch-list annotation |
| `schedule` | mixed | `list` reads; `create` and `cancel` write |

`memory` and `heartbeat_respond` are the only tools classified *internal*: every
action writes, but only into documents you already read in the prompt and can
edit yourself, and nowhere else. `normal` lets them through so you are not
approving a tap per remembered fact; `strict` still asks.

`schedule` is classified per action, so listing what is due runs freely while
creating or cancelling asks.

## With `repositories` configured

| Tool | Effect | What it does |
| --- | --- | --- |
| `repository_list` | read | List repositories configured at runtime |
| `repository_github` | read | Read GitHub metadata for a configured repository |
| `workspace_open` | read | Attach a bounded read-only checkout to this conversation |
| `read_file` | read | Read one file inside the attached workspace |
| `workspace_close` | read | Detach and clean up the checkout |

That is the whole repository surface: no tree listing, no content search, no
shell, and no mutation. See
[Repository inspection](/eggy/configure/repositories/).

## With `telegram` configured

| Tool | Effect | What it does |
| --- | --- | --- |
| `telegram_select` | read | Ask the owner to tap one of 2–8 labelled options |

A tap returns the chosen value as an ordinary owner message. It can never
approve a protected action.

## With `google` authorized

| Tool | Effect | What it does |
| --- | --- | --- |
| `google_gmail` | mixed | Search and read mail; sending and modifying write |
| `google_calendar` | mixed | Read events; creating and editing write |
| `google_drive` | mixed | Search and read; uploading and moving write |
| `google_docs` | mixed | Read documents; editing writes |
| `google_sheets` | mixed | Read ranges; writing cells writes |
| `google_contacts` | mixed | Look people up; creating and updating write |

Which tools exist is decided by `google.products` at startup; authorizing later
changes only whether a call succeeds. Each action is classified as a read or a
mutation in the adapter, and `google.require_approval` narrows or widens that.
See [Google Workspace](/eggy/configure/google-workspace/).

## With `tavily` configured

| Tool | Effect | What it does |
| --- | --- | --- |
| `web_search` | read | Find pages, return ranked snippets |
| `web_extract` | read | Read up to five of those pages as markdown |

See [Web search](/eggy/configure/web-search/).

## From MCP servers

Remote tools arrive namespaced as `<server>__<tool>`, so a server can never
replace a native tool. They are the one exception to effect classification: a
remote catalog cannot be judged from here, so an MCP server is trusted wholesale
at configuration time and governed by its own `require_approval` list. `strict`
mode still gates every one of them. See [MCP servers](/eggy/configure/mcp-servers/).

The MCP catalog is read per turn rather than snapshotted at startup, so a server
that reconnects, reloads its catalog, or is logged out of changes the next turn's
tool set without a restart.

## What scheduled turns get

Unprompted turns — schedules and heartbeat beats — do not inherit the direct
turn's authority. They run against a read-only allowlist, cannot use MCP, and
cannot mutate anything. `schedule` is offered to them with the `list` action
alone, described that way in the schema: a beat may see what else is due, and may
not change it.
