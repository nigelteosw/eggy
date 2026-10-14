---
title: Extensions
description: The optional features built into Eggy, how each one is switched on, and what it adds.
eyebrow: Configure
---

An extension is an optional feature built into Eggy. It can add a tool the model can call, a page under **Extensions** in the web panel's sidebar, and a Telegram command. Eggy ships two today:

| Extension | Section | What it does | Guide |
| --- | --- | --- | --- |
| Finance | `finance` | Logs spending by chat or receipt photo, per person | [Finance](/eggy/configure/finance/) |
| Agent Runner | `agent_runner` | Makes a reviewed change in a repository you allow, runs its checks, and opens a pull request | [Agent Runner](/eggy/configure/agent-runner/) |

Extensions are compiled into Eggy. Eggy does not load plugins from a directory at runtime and has no marketplace to install them from. A new extension is code written and built into the daemon, and then switched on by configuration.

## Turn one on

Each extension has one `enabled` switch in its own section of `config.yaml`:

```yaml
finance:
  enabled: true

agent_runner:
  enabled: true
  model: "coding"   # a models: alias from your config
```

You can also switch them on from the panel's Settings, where each extension has its own card. A change applies after a restart, and the card says **Restart to apply** until the saved setting and the running one match.

## What switching on adds

- **A tool** in the model's tool set, such as `finance` or `agent_runner`. A heartbeat or a schedule cannot reach either one.
- **An Extensions entry** in the web panel's sidebar, which opens the extension as a window over the view you are on. The entry appears only while the extension is running.
- **A command**, for Agent Runner only: `/code` in Telegram.

## What it costs when off

Nothing. With the section absent or `enabled: false`, the extension builds nothing: no tool schema reaches a model request, no route is served, no sidebar entry is drawn, and no goroutine runs. The only thing left is the extension's records in `eggy.db`. Turning it off and restarting keeps those records, and turning it on again brings them back.

## How extensions differ from skills and MCP

- A **skill** is a Markdown procedure you place in `skills/`. It grants no tool and lifts no approval. See [Skills](/eggy/use/skills/).
- An **MCP server** is a remote or local tool server you configure. Its tools are trusted as a whole unless you gate them. See [MCP servers](/eggy/configure/mcp-servers/).
- An **extension** is Eggy's own code for one feature. It has its own records, its own page, and its own approval rules, and it is off until you switch it on.

## Approvals

Each extension follows the same approval rules as the rest of Eggy. Finance writes only to the calling account's own entries, so `normal` mode does not ask before each one. Agent Runner's status and list actions run freely, while cancelling a run or answering its question asks first in `normal`. A coding run's push and pull request are authorized by a mandate you create, and `/mode` does not change that. See [Approvals](/eggy/use/approvals/).
