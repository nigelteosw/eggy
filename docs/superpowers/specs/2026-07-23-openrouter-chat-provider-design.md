# OpenRouter chat provider readiness design

**Status:** Approved for implementation planning
**Date:** 2026-07-23

## Context

Eggy's provider control surface is already generic: `ProviderConfig` +
`ModelAliasConfig` (`internal/bootstrap/config.go`), the `/config set
provider` / `/config set model` commands, and the `openai_compatible` adapter
(`internal/adapters/models/openaicompat`) already let any OpenAI-wire-format
provider be added as configuration, with no new Go code. OpenRouter already
works today as a plain chat provider through this path — it's even used as
the worked example in `/config set provider` help text
(`internal/bootstrap/commands.go:441`) and exercised in tests
(`internal/bootstrap/app_test.go:118`, `internal/bootstrap/commands_test.go`).

The one real gap is reasoning-effort control. `ports.ModelRequest.ReasoningEffort`
(`internal/ports/ports.go:47`) is Eggy's single, provider-neutral field for
"how much reasoning budget to use" — set via `/model effort <level>` and
carried through `ModelAliasConfig.ReasoningEfforts`
(`internal/bootstrap/config.go:61`). Today `openaicompat.Generate` always
serializes it the way DeepSeek's API expects: a flat top-level
`reasoning_effort` string. OpenRouter's API does not accept that field at
all — per OpenRouter's docs, reasoning control is a nested object:
`"reasoning": {"effort": "high", "max_tokens": ..., "exclude": ...,
"enabled": ...}`. If an OpenRouter-routed model alias were given a
`reasoning_efforts` list today, `/model effort high` would report success
but silently have no effect on the actual request — a broken control
surface for exactly the case that motivated this design.

There is no way to reconcile the two wire formats into one shape; the fix is
to keep the existing kernel-level field standardized and push the
translation into the adapter, where provider wire differences belong.

## Goals

- Let a `ModelAliasConfig` backed by an OpenRouter (or any other
  wrapped-format) provider honor `reasoning_efforts` / `/model effort`
  correctly.
- Keep `internal/kernel` and `internal/ports` unchanged — the standardized,
  provider-neutral `ReasoningEffort` field already does its job; only the
  adapter's wire encoding needs to vary.
- Make OpenRouter a first-class documented example alongside DeepSeek in
  `config.example.yaml` and `.env.example`.
- Keep the mechanism generic (a declarative per-provider format setting),
  not an `if provider == "openrouter"` special case, so any future
  OpenAI-compatible aggregator with the same wrapped-reasoning convention
  can reuse it.

## Non-goals

- Embeddings / semantic search support for OpenRouter (covered separately by
  `2026-07-23-sqlite-memory-db-design.md`; explicitly deferred).
- Picking or hardcoding a specific OpenRouter model — the example alias in
  config stays a placeholder until a model is chosen.
- OpenRouter-specific attribution headers (`HTTP-Referer`, `X-Title`) —
  cosmetic/optional, not needed for correct function, out of scope per
  YAGNI.
- Support for `reasoning.max_tokens` / `reasoning.enabled` / `reasoning.exclude`
  — only `effort` is wired, mirroring the single level of control Eggy
  already exposes via `/model effort`.
- Streaming, multi-provider fan-out, or automatic format detection based on
  base URL — the format is an explicit config value, not inferred.

## Architecture

No new adapter package and no new port. `internal/adapters/models/openaicompat`
gains a second wire-encoding mode, selected by one new provider-level config
field.

### Config schema

`ProviderConfig` (`internal/bootstrap/config.go:53`) gets one new optional
field:

```go
type ProviderConfig struct {
	Adapter         string `yaml:"adapter"`
	BaseURL         string `yaml:"base_url"`
	APIKeyEnv       string `yaml:"api_key_env"`
	ReasoningFormat string `yaml:"reasoning_format,omitempty"` // "" (flat, default) | "wrapped"
}
```

Empty/absent means today's flat behavior — DeepSeek and any other existing
provider entry is unaffected without edits. Validation (alongside the
existing `validReasoningEfforts` check in `config.go`) rejects any value
other than `""`, `"flat"`, or `"wrapped"`.

The `/config set provider` command and CLI equivalent
(`internal/bootstrap/commands.go`, `command_catalog.go`) gain an optional
`reasoning_format` argument, following the same underscored-key pattern
already used for `base_url` / `api_key_env`.

### Adapter change

`openaicompat.Model` takes the format as a constructor option:

```go
func New(baseURL, apiKey string, client *http.Client, reasoningFormat string) *Model
```

`Generate` branches only when `input.ReasoningEffort != ""`:

- `reasoningFormat == "wrapped"`: encode a `Reasoning *struct{ Effort string
  \`json:"effort"\` } \`json:"reasoning,omitempty"\`` field instead of the
  existing flat `ReasoningEffort` field on `requestBody`.
- otherwise (default): unchanged flat `reasoning_effort` field, exactly as
  today.

Both branches still populate the same `ports.ModelRequest.ReasoningEffort`
input and produce the same `ports.ModelResponse` output — the translation is
purely on the outbound request encoding. Response decoding is untouched
(OpenRouter returns `reasoning_content` / `usage.completion_tokens_details`
compatibly with the existing decode path).

### Bootstrap wiring

`internal/bootstrap/app.go:212` passes `provider.ReasoningFormat` through to
`openaicompat.New(...)`. No other call site changes.

## Config examples

`config.example.yaml` gains a second provider block:

```yaml
providers:
  deepseek:
    adapter: "openai_compatible"
    base_url: "https://api.deepseek.com"
    api_key_env: "DEEPSEEK_API_KEY"
  openrouter:
    adapter: "openai_compatible"
    base_url: "https://openrouter.ai/api/v1"
    api_key_env: "OPENROUTER_API_KEY"
    # OpenRouter expects reasoning effort as a nested object, not a flat
    # field. Leave unset ("flat") for providers that use a plain
    # reasoning_effort string, like DeepSeek above.
    reasoning_format: "wrapped"
models:
  deepseek-pro:
    provider: "deepseek"
    model: "deepseek-v4-pro"
    reasoning_efforts: ["low", "medium", "high", "max"]
  # openrouter-pro:
  #   provider: "openrouter"
  #   model: "<pick a model id from openrouter.ai/models>"
  #   reasoning_efforts: ["low", "medium", "high"]  # omit if the chosen model doesn't support effort control
```

`.env.example` uncomments and documents `OPENROUTER_API_KEY` the same way
`DEEPSEEK_API_KEY` is documented (env var name only, never a real key).

README's existing OpenRouter example (`README.md:39-45`) is updated to
mention `reasoning_format` when the chosen model supports reasoning effort.

## Testing

- `openaicompat` unit test: given `reasoningFormat = "wrapped"` and a
  non-empty `ReasoningEffort`, assert the outbound JSON body contains
  `reasoning.effort` and does not contain a top-level `reasoning_effort`
  field. A companion test asserts the existing flat behavior is unchanged
  when `reasoningFormat` is `""`.
- `internal/bootstrap` config tests: round-trip `reasoning_format` through
  `SetProvider` / config load-and-reload (mirroring the existing
  `config_mutate_test.go` coverage for `base_url` / `api_key_env`), and
  reject an invalid `reasoning_format` value.
- No changes needed to `agent_runtime_test.go` — `ReasoningEffort` selection
  logic at the kernel level is untouched; this is purely an adapter
  encoding concern.
