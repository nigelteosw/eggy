---
title: Introduction to Eggy
description: A private, single-owner agent that runs where you deploy it and connects conversation, tools, memory, and trusted integrations.
eyebrow: Get started
---

Eggy is a self-hosted personal agent written in Go. One `eggyd` daemon serves an authenticated web chat and, when configured, Telegram. It routes each turn to a configured model, keeps durable context, and exposes a deliberately bounded set of tools.

> **Owner-first by design.** Eggy has one canonical owner. It is not a shared chatbot, a hosted multi-tenant service, or a general-purpose shell.

## Get started

### 1. Build Eggy

Eggy requires Go 1.26 and Bun. Clone the repository, then build the embedded web application and daemon.

```sh
git clone https://github.com/nigelteosw/eggy.git
cd eggy
make build
```

### 2. Configure your instance

Copy the example files, set a model provider key, and choose either a Telegram owner or a web-only owner.

```sh
cp config.example.yaml config.yaml
cp .env.example .env
```

Secrets stay in environment variables or `.env`; `config.yaml` contains only names and non-secret settings.

### 3. Start the daemon

```sh
EGGY_CONFIG="$PWD/config.yaml" ./bin/eggyd
```

For a persistent hosted instance, continue to [Deploy on Railway](/eggy/get-started/deploy-railway/).

## What Eggy can do

- Hold direct conversations in web chat and Telegram.
- Switch among configured model aliases.
- Remember successful conversation turns in an embedded SQLite database.
- Read owner-maintained identity and memory from Markdown.
- Inspect configured GitHub repositories without changing them.
- Connect trusted MCP servers over Streamable HTTP or stdio.
- Create, review, and cancel exact and recurring schedules for agent turns or deterministic reminders.
- Load reviewed procedural skills from local Markdown files.

## How Eggy works

```text
Web chat / Telegram
        │
        ▼
   HTTP + event queue
        │
        ▼
  conversation service ──► model provider
        │                       │
        └──── bounded tools ◄───┘
                  │
        MCP · repos
                  │
                  ▼
          durable /data home
```

The kernel knows only provider-neutral ports. Concrete model, channel, repository, scheduler, memory, and MCP implementations live in adapter packages and are composed at startup.

## Deliberate boundaries

Configured repositories are trusted inputs, but repository access is read-only. Eggy can clone, open, and inspect a checkout; it cannot edit files, run an agent shell, commit, push, or open a pull request.

Adding an MCP server to configuration is the trust decision, and its tools run without asking by default. Naming tools under a server's `require_approval` routes those calls through the payload-bound approval mechanism instead, and `/auto` switches every gate off when you want to work uninterrupted.

## Quick links

- [Run Eggy locally](/eggy/get-started/quickstart/)
- [Configure model providers](/eggy/configure/model-providers/)
- [Understand the security model](/eggy/operate/security/)
- [Read the architecture](/eggy/project/architecture/)
