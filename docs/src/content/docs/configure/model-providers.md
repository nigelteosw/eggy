---
title: Model providers
description: Configure OpenAI-compatible providers and expose their models through stable owner-facing aliases.
eyebrow: Configure
---

`adapter` names a **wire format, not a vendor.** The shipped adapter,
`openai_compatible`, speaks OpenAI's chat-completions shape — `/chat/completions`,
`tools` and `tool_calls`, `reasoning_effort`, and cached-token usage reporting.

That is OpenAI's own API, so OpenAI, DeepSeek, OpenRouter, Groq, and most hosted
model services are all reachable by adding a provider entry. Adding one of them
needs no Go code. Provider credentials and wire types stay inside
`plugins/models/openaicompat`.

## Add a provider

```yaml
providers:
  deepseek:
    adapter: openai_compatible
    base_url: https://api.deepseek.com
    api_key_env: DEEPSEEK_API_KEY
```

OpenAI itself is the same shape, pointed at a different base URL:

```yaml
providers:
  openai:
    adapter: openai_compatible
    base_url: https://api.openai.com/v1
    api_key_env: OPENAI_API_KEY
```

Then set the named environment variable:

```dotenv
DEEPSEEK_API_KEY=...
```

Provider names and base URLs are non-secret. The key itself must not appear in YAML.

OpenRouter is the same shape again, and is the provider discovery was built for:

```yaml
providers:
  openrouter:
    adapter: openai_compatible
    base_url: https://openrouter.ai/api/v1
    api_key_env: OPENROUTER_API_KEY
```

### What OpenRouter gets that others do not

The adapter recognises an `openrouter.ai` base URL and adds what OpenRouter
alone understands. Nothing here leaks into a request to any other provider.

- **Sticky routing.** The conversation ID is sent as the session key (body
  `session_id` and header `x-session-id`), so consecutive turns land on the
  same upstream and its prompt cache.
- **Prompt caching.** `anthropic/*` models get a top-level
  `cache_control: ephemeral`, OpenRouter's recommended form for multi-turn
  chats.
- **Reasoning.** The effort is sent as OpenRouter's nested `reasoning.effort`
  rather than `reasoning_effort`. When an alias declares `reasoning_efforts`
  and none is selected, `effort: none` is sent explicitly, so a model that
  reasons by default does not keep doing so unasked — but only for a model
  OpenRouter's catalog says can be switched off. One that always reasons
  (Gemini, DeepSeek R1) is left alone. The catalog is fetched once per
  process, on first need.
- **Effort levels from the catalog.** OpenRouter's `/models` says which
  efforts each model accepts. Browsing shows them, picking a row pre-fills
  `reasoning_efforts` from them, and `/model available` lists them beside
  each ID.
- **Reasoning replay.** OpenRouter's `reasoning_details` are carried back on
  the assistant message across tool-call rounds within a turn, unmodified, so
  Anthropic and OpenAI reasoning models keep their own thinking. The visible
  reasoning text is still never replayed.
- **Provider routing.** An alias can say which upstream vendors may serve it:

  ```yaml
  models:
    sonnet:
      provider: openrouter
      model: anthropic/claude-sonnet-4.6
      openrouter:
        order: [anthropic, amazon-bedrock]   # try these first
        ignore: [deepinfra]                  # never these
        only: []                             # or: allow just these
        allow_fallbacks: false               # default true
        sort: price                          # price | throughput | latency
  ```

  Slugs are OpenRouter's own. The block is refused on any alias whose
  provider is not OpenRouter. **Settings → Models** shows the same fields
  under *Advanced options* once the alias's provider is an OpenRouter one, and `/model add` takes them as `openrouter_order=a,b`,
  `openrouter_ignore=`, `openrouter_only=`, `openrouter_sort=`, and
  `openrouter_allow_fallbacks=false` after the positional words.
- **Cost and cache writes.** OpenRouter reports `cost` and
  `cache_write_tokens` on every response; both show on the turn's
  [trace](/eggy/use/traces/).
- **Upstream errors.** A rejected request quotes OpenRouter's error message
  and the upstream vendor's own words from `metadata.raw`. Authentication
  failures are never quoted, since a provider may echo the key.

## Discover what a provider serves

`discover_models` is **on unless switched off**. It lets a surface ask the
provider for its own `/models` listing, so an alias can be filled in from what
the provider actually serves instead of from an ID copied out of a vendor's web
page.

```yaml
providers:
  openrouter:
    adapter: openai_compatible
    base_url: https://openrouter.ai/api/v1
    api_key_env: OPENROUTER_API_KEY
    discover_models: false   # never query this provider's catalog
```

Two surfaces read it:

- **Settings → Models** lists the browsable providers, fetches the catalog on
  demand, filters it as you type, and fills the alias form from the row you
  pick.
- **`/model providers`** names every provider and says which can be browsed.
  **`/model available <provider> [filter]`** lists its catalog — the filter is
  worth using, since OpenRouter answers with several hundred entries.
  **`/model add <alias> <provider> <model> [efforts] [openrouter_*=...]`**
  writes the alias, and `/restart` makes it selectable.

**Discovery is a browse list, never an allowlist.** What Eggy will run stays
exactly what `models` names, and a discovered model becomes selectable only
once it is written down as an alias. That separation is the point: a provider's
catalog is provider-controlled and changes without warning, so it may inform a
choice but must not be one.

A provider whose adapter cannot list, or that opted out, simply does not appear
as browsable. That is a normal, working provider — not an error.

## Add aliases

```yaml
agent:
  default_model: deepseek-pro
  timezone: Asia/Singapore

models:
  deepseek-pro:
    provider: deepseek
    model: deepseek-v4-pro
    reasoning_efforts: [low, medium, high, max]
```

`agent.default_model` must name a configured alias. Every alias must reference an existing provider. Only aliases in this catalog can be selected.

## Add a different model adapter

A new adapter is earned by a **different wire format**, not a different vendor.
Anthropic's Messages API is the real example: a top-level system prompt, content
blocks instead of string content, `tool_use` and `tool_result` blocks instead of
`tool_calls`, and a required `max_tokens`. None of that fits `openai_compatible`.

Writing an `openai` adapter that duplicates `openaicompat` is the wrong turn, and
a test in `internal/config` pins that OpenAI stays a provider entry.

A genuinely new format costs three things:

1. the package, in `plugins/models/<provider>/`;
2. its name in `config.supportedModelAdapters`, so configuration validates;
3. one case in `bootstrap.newModelAdapter`, which is the only place an adapter
   name becomes a running implementation.

Nothing else changes. Do not import provider types into the kernel or widen the
shared `Model` port to fit one provider.
