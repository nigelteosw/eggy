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

Eggy accepts `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`. When an
effort is selected for an alias, the OpenAI-compatible adapter sends it as
`reasoning_effort` (OpenRouter's nested `reasoning.effort` on an OpenRouter
provider). With no selection, no effort parameter is sent and the model runs
at the provider's own default — for OpenRouter that is the catalog's
`default_effort`, usually `medium` or `high`. To keep a reason-by-default
model quiet, pick `none`; models that allow it list it among their efforts.

On OpenRouter an alias can also pin which upstream vendors serve it; see
[Model providers](/eggy/configure/model-providers/).

Effort is chosen in the web panel and, like the model, is durable runtime state.
The effort a turn ran on is recorded in its [trace](/eggy/use/traces/).

## Changing models by asking

Eggy can manage its own models on OpenRouter. Say "add Gemini Flash Image from
OpenRouter" and it searches OpenRouter's catalog for the exact model ID, writes
an alias with the catalog's reasoning efforts, and restarts to load it. Adding
an alias changes the shared `config.yaml`, so in `normal` mode it asks for your
approval first. "Switch to sonnet" changes only your own `/model` choice, so
it runs without asking. The new model answers from your next message on.

## Generated images

Choose an **image model** in **Settings → Models** (`agent.image_model`). It
must be an OpenRouter alias whose model OpenRouter lists as producing `image`.
After that, ask for a picture with any model selected: the model you are
chatting with calls `generate_image`, which draws with the image model, and the
picture is attached to its reply. With no image model chosen, the tool does not
exist.

If the alias you have selected can draw by itself, it also draws directly.
Eggy knows a model draws when OpenRouter's catalog lists `image` among its
output modalities, and only then asks for image output, using the model's own
list. A model that outputs only images is asked for images alone.

- **Only on turns you start.** Scheduled turns and check-ins never ask for an
  image, because each picture is billed and nobody asked for it.
- **Where it shows.** Telegram receives it as a photo and Discord as an
  attachment in your DM. The web chat draws it in the reply and keeps it there
  after a reload. Click it to open it full size.
- **How long it is kept.** An image is stored in `eggy.db` with the
  [trace](/eggy/use/traces/) of the turn that drew it, and is deleted when
  that trace is pruned. **Settings → Tracing** (`tracing.keep_turns` and
  `tracing.retention`) decides how long both last: by default the newest
  500 turns, for at most seven days. Save an image you want to keep.
- **With tracing off** an image is still sent to Telegram and Discord, but it
  is not stored. The web chat cannot show it and says so.
- **What is accepted.** PNG, JPEG, WebP, and GIF images up to 10 MB. A model
  that answers with a link instead gets the link in its reply; Eggy never
  fetches it.

Only the reference to an image enters the conversation, never the image
itself, so a later turn knows a picture was made but cannot see it. To edit
one, save it and send it back as an attachment.

## Provider isolation

Model request and response types stay inside the model adapter. The core
receives only provider-neutral messages, tool definitions, usage, and model
identifiers — which is why adding a provider that speaks the OpenAI wire format
is configuration rather than code. See
[Model providers](/eggy/configure/model-providers/).
