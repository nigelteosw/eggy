---
title: Model providers
description: Configure OpenAI-compatible providers and expose their models through stable owner-facing aliases.
eyebrow: Configure
---

The shipped model adapter speaks the OpenAI-compatible chat-completions shape. Provider credentials and wire formats remain inside `plugins/models/openaicompat`.

## Add a provider

```yaml
providers:
  deepseek:
    adapter: openai_compatible
    base_url: https://api.deepseek.com
    api_key_env: DEEPSEEK_API_KEY
```

Then set the named environment variable:

```dotenv
DEEPSEEK_API_KEY=...
```

Provider names and base URLs are non-secret. The key itself must not appear in YAML.

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

A genuinely different provider implementation belongs in `plugins/models/<provider>/`. Select it through `ProviderConfig.Adapter` in bootstrap; do not import provider types into the kernel or widen the shared `Model` port to fit one provider.
