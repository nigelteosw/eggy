---
title: Configuration overview
description: Understand Eggy's YAML, environment variables, first-boot generation, and restart behavior.
eyebrow: Configure
---

Eggy reads `config.yaml` from its home directory, from `EGGY_CONFIG`, or from an explicit `--config` flag. Unknown YAML fields are rejected so misspellings cannot silently disable a restriction.

## Top-level sections

| Section | Purpose |
| --- | --- |
| `server` | Listen address, public URL, Telegram webhook path, proxy hop count |
| `data_dir` | Durable artifact root |
| `owner` | Canonical single-owner identity |
| `telegram` | Optional numeric Telegram owner |
| `agent` | Default model alias and timezone |
| `providers` | Model adapter connections |
| `models` | Owner-facing model aliases |
| `repositories` | Trusted read-only Git repositories |
| `runner` | Checkout root, timeout, retention, output, and environment bounds |
| `mcp` | Optional trusted remote or local servers |
| `heartbeat` | Optional periodic check-in that speaks only when warranted |

## Heartbeat

Omitted, the heartbeat costs nothing: no ticker, no model call. Set an interval to turn it on:

```yaml
heartbeat:
  interval: 3h
  # instruction: "Check the deploy and open pull requests."
```

`3h` is the recommended starting interval. Each tick runs an isolated read-only turn with no conversation history, and delivers to Telegram only when there is something worth saying — so a heartbeat is not one message per tick. A tick is skipped while another turn is running. Without a `telegram` block there is nowhere to deliver unprompted output, so the heartbeat stays off and says so once at startup.

The settings panel edits this section too. A blank interval there means off, rather than leaving the previous interval in place; the instruction is preserved either way, so turning the heartbeat back on does not mean retyping it. Like every other section, the panel writes `config.yaml` and the change applies on the next restart, which `/restart` in chat performs.

## Secrets

YAML names secret environment variables but does not hold their values:

```yaml
providers:
  deepseek:
    adapter: openai_compatible
    base_url: https://api.deepseek.com
    api_key_env: DEEPSEEK_API_KEY
```

Eggy loads process variables and then `.env`. Secret values are collected for log redaction and never returned through the web config API.

## First boot

When the configured path does not exist, Eggy atomically generates a valid baseline config from deployment variables. Later boots load the persisted file; changing first-boot variables does not rewrite existing YAML.

## Applying changes

The web settings panel writes supported sections directly to `config.yaml`. Nothing in the running process re-reads that file, because bootstrap builds providers, tools, channels, and routes once at startup. A write takes effect on the next restart, and both surfaces say so on every write.

Schedules and procedural skills are not YAML configuration. They live as reviewed files under `cron/` and `skills/` in the Eggy home.

## Restarting to apply config

Two surfaces perform that restart: `/restart` in chat, and the **Restart** button on the settings panel's Advanced page. They are the same mechanism — one restart authority, two views onto it — so a restart the command would refuse the button refuses identically. Either way a config you edited from a phone can be applied from a phone.

It is not a process restart: `eggyd` supervises the daemon rather than being it, so the command tears the daemon down and builds a new one from the file on disk inside the same process. Nothing redeploys, the container is never replaced, and the platform never sees the service go away.

What happens, in order:

1. **The config is loaded first, and a bad one cancels the restart.** Both surfaces run the same load startup would, with the same environment including `.env`. If the file would not load, nothing restarts: the reply is the error, and the Eggy you already had keeps running. This matters because a config that fails to load puts the process into safe mode — a repair page served in place of Eggy, with Telegram gone and only the browser able to reach it. That is a recovery path worth having, but not one to fall into from a phone by accident.
2. **In-flight work finishes.** The restart is a signal to the event loop, not a cancellation. The loop stops accepting new work and waits for running turns — including the one that is still owed the `/restart` acknowledgement, which is why the confirmation always arrives. A long tool call delays the restart until it is done.
3. **The old daemon closes down.** MCP clients disconnect and the conversation database closes, then the HTTP listener shuts down gracefully so requests already in flight get their responses — including the panel's own restart request, which is why the button gets an answer rather than a dropped connection.
4. **A new daemon is built from the current `config.yaml`.** Providers, model aliases, MCP servers, repositories, the heartbeat, the scheduler, and the listen address are all rebuilt from what the file says now.

Durable state is on the volume, not in memory, so it survives: conversation history, memory, approvals, the auto-mode setting, schedules in `cron/`, and thread-attached checkouts are all reattached by the new daemon exactly as a redeploy would leave them.

The panel goes quiet while this happens: its chat stream is served by the daemon being replaced, so reload the page once the new one is listening.

Two things a restart does not fix. `.env` is read once when the process starts, so a changed secret still needs a real process restart. And a config that fails to load *later* — one that passed the pre-flight but broke during construction, such as an unreachable repository — lands in safe mode like any other failed startup, which is the recovery path rather than a dead container.
