---
title: Models and reasoning effort
description: Route conversations through named model aliases, browse what a provider serves, and send provider-supported reasoning effort.
eyebrow: Use Eggy
---

Eggy separates a provider connection from the model aliases the owner selects. A
provider holds the adapter, base URL, and environment-variable name; an alias
points to a provider and a provider model ID. **Only aliases are selectable** —
what Eggy will run is exactly what `models` names.

## Select a model

On Telegram:

```text
/model
/model deepseek-pro
/model default
```

The selected alias is durable runtime state: it survives restarts and applies to
later turns until changed. `default` restores `agent.default_model`.

The panel's **Settings → Models** section selects, adds, edits, and removes
aliases.

## Browse what a provider serves

Writing an alias means knowing a provider's exact model ID, which is otherwise a
value copied out of a vendor's web page. Discovery removes that step:

```text
/model providers
/model available openrouter sonnet
/model add sonnet openrouter anthropic/claude-sonnet-4.5 low,medium,high
```

The panel does the same thing as a form: it lists browsable providers, fetches
the catalog on demand, filters it as you type, and fills the alias form from the
row you pick.

Discovery is **on unless a provider switches it off** with
`discover_models: false`. A provider that opted out, or whose adapter cannot
list, simply does not appear as browsable — that is a normal, working provider,
not an error.

**A catalog is a browse list, never an allowlist.** A discovered model becomes
runnable only once it is written down as an alias, and a new alias is selectable
only after a restart. A provider's catalog is provider-controlled and changes
without warning, so it may inform a choice but must not be one.

## Reasoning effort

An alias can declare the values its model supports:

```yaml
models:
  deepseek-pro:
    provider: deepseek
    model: deepseek-v4-pro
    reasoning_efforts: [low, medium, high, max]
```

Eggy accepts only `low`, `medium`, `high`, and `max`. When an effort is selected
for an alias, the OpenAI-compatible adapter sends it as `reasoning_effort`
(OpenRouter's nested `reasoning.effort` on an OpenRouter provider). For aliases
without declared values, no effort parameter is sent at all — an empty list is
the off switch, not a default. An alias *with* declared values and no current
selection tells OpenRouter `effort: none`, so a reason-by-default model stays
off until a level is picked.

On OpenRouter an alias can also pin which upstream vendors serve it; see
[Model providers](/eggy/configure/model-providers/).

Effort is chosen in the web panel and, like the model, is durable runtime state.
The effort a turn ran on is recorded in its [trace](/eggy/use/traces/).

## Provider isolation

Model request and response types stay inside the model adapter. The kernel
receives only provider-neutral messages, tool definitions, usage, and model
identifiers — which is why adding a provider that speaks the OpenAI wire format
is configuration rather than code. See
[Model providers](/eggy/configure/model-providers/).
