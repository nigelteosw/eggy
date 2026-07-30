---
title: Security model
description: Review Eggy's owner boundary, credential isolation, trusted inputs, approval rules, and process restrictions.
eyebrow: Operate
---

Eggy is designed for one owner and repositories the owner already trusts. It reduces accidental authority and credential exposure; it is not a hostile-code sandbox.

## Owner boundary

Telegram accepts one configured numeric owner. Web chat requires the configured email and password, issues a signed HTTP-only session, and throttles login failures. Every channel maps back to the same canonical `owner.id`.

## Secrets

Secret values come from environment variables or `.env`, not YAML. Provider credentials remain inside adapters. Logger setup receives the loaded secret set and redacts it from output.

Calendar and MCP OAuth records are sealed with AES-256-GCM under `EGGY_ENCRYPTION_KEY`.

## Repository boundary

Configured repositories are trusted. Their tools remain path-restricted, timeout-bounded, output-bounded, and environment-allowlisted. The current general agent surface is read-only.

The local runner is a restricted process boundary, not a container boundary. A stdio MCP server runs as Eggy's operating-system user and must be treated as trusted code.

## Side effects

Native Calendar writes require independent, payload-bound approvals. Reads do not. Telegram selections cannot authorize mutations.

MCP servers are trusted wholesale when configured because Eggy cannot place a reliable per-call approval boundary around an arbitrary server's behavior. Narrow their tools with exact filters.

## Scheduled turns

Scheduled agent turns are read-only and do not gain authority from instruction text. Deterministic message schedules do not invoke a model at all.
