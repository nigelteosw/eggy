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
| `calendar` | Optional native Google Calendar default |
| `mcp` | Optional trusted remote or local servers |

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

The web settings panel writes supported sections directly to `config.yaml`. Restart `eggyd` after changing configuration so bootstrap can rebuild providers, tools, channels, and routes.

Schedules and procedural skills are not YAML configuration. They live as reviewed files under `cron/` and `skills/` in the Eggy home.
