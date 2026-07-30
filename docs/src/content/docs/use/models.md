---
title: Models and reasoning effort
description: Route conversations through named model aliases and optionally send provider-supported reasoning effort.
eyebrow: Use Eggy
---

Eggy separates a provider connection from the model aliases the owner selects. A provider holds the adapter, base URL, and environment-variable name; an alias points to a provider and provider model ID.

## Select a model

On Telegram:

```text
/model
/model deepseek-pro
/model default
```

The web configuration panel can add and update aliases. The selected alias is stored in runtime state and applies across later turns.

## Reasoning effort

An alias can declare supported values:

```yaml
models:
  deepseek-pro:
    provider: deepseek
    model: deepseek-v4-pro
    reasoning_efforts: [low, medium, high, max]
```

Eggy accepts only `low`, `medium`, `high`, and `max`. When an effort is selected for an alias, the OpenAI-compatible adapter sends it as `reasoning_effort`. For aliases without declared values, no effort parameter is sent.

The current direct Telegram command selects aliases; reasoning-effort values are administered through the web model settings.

## Provider isolation

Model request and response types stay inside the model adapter. The kernel receives only provider-neutral messages, tool definitions, usage, and model identifiers.
