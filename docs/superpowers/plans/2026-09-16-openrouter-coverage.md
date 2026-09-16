# OpenRouter coverage parity with pi-ai

Bring `plugins/models/openaicompat` up to what pi-mono's `openai-completions` API does
when it detects OpenRouter, and make opaque reasoning replay a port-level standard that
any future adapter (Bedrock, native Anthropic, OpenAI Responses) fills the same way.

Verified 2026-09-16 against pi-mono `packages/ai/src/api/openai-completions.ts` and
OpenRouter docs (usage-accounting, reasoning-tokens, provider-routing, prompt-caching).

## Current state

`plugins/models/openaicompat/model.go`:
- `isOpenRouterURL` flips `openRouter` on host `openrouter.ai` / `*.openrouter.ai`.
- Sends body `session_id`, top-level `cache_control: ephemeral` for `anthropic/*`, and
  top-level `reasoning_effort` for every provider.
- Non-streaming; decodes `reasoning_content` into `ModelResponse.ReasoningContent`
  which is documented as never replayed. Error bodies on non-2xx are discarded.

Kernel facts that shape the design:
- `agent/loop.go:199` builds `ports.ModelRequest{Model, Messages, Tools, ReasoningEffort}`
  and appends `response.Message` to `window.tail` between tool-call rounds. That in-turn
  history is the only place reasoning replay matters.
- `ports.StoredMessage` persists only `Role`/`Content` — tool calls are not stored across
  turns, so cross-turn reasoning replay is out of scope and needs no store change.
- `ModelTarget{Model, ModelID}` is built per alias in `bootstrap/app_wiring.go:215`; alias
  `reasoning_efforts` go to `AgentRuntime.efforts`. `ReasoningEffort(ctx)` returns `""`
  when nothing is selected.
- Alias config is set through `config.SetModelAlias(path, alias, provider, model, efforts)`
  from `web/config_routes.go:269` and rendered in `website/src/ModelsCard.tsx`.

## Design

### A. Opaque provider reasoning (the standard)

```go
// ports.Message
// ProviderReasoning is opaque reasoning state the producing adapter needs
// back on this message in the next request of the same turn: OpenRouter's
// reasoning_details, Anthropic's signed thinking blocks, OpenAI's encrypted
// reasoning items. The kernel carries it and never inspects it.
// ProviderReasoningOrigin names the adapter/provider that wrote it, so an
// adapter replays only what it produced; a mismatch is dropped, which is
// exactly today's behaviour.
ProviderReasoning       json.RawMessage `json:"provider_reasoning,omitempty"`
ProviderReasoningOrigin string          `json:"provider_reasoning_origin,omitempty"`
```

- `ModelResponse.ReasoningContent` stays: the visible text, still never replayed.
  Its comment changes to say so explicitly, contrasting with `ProviderReasoning`.
- `openaicompat` on OpenRouter: decode `message.reasoning_details` (raw) into
  `Message.ProviderReasoning`, origin `"openrouter"`; on send, for assistant messages
  whose origin is `"openrouter"`, emit `reasoning_details` verbatim.
- Non-OpenRouter providers: neither read nor write it (DeepSeek's in-round
  `reasoning_content` replay can adopt the same slot later).
- `TracedModel` (`services/trace.go`) omits the blob from traces — it is opaque and
  can be several KB for encrypted items.
- Loop: no change; `assistant := response.Message` already carries it into `window.tail`.
- Compaction (`contextWindow`) summarises folded steps into text, so blobs naturally
  drop with their messages; nothing to add.

### B. Reasoning parameter

- OpenRouter: `reasoning: {effort: X}` nested; drop top-level `reasoning_effort`.
- Off: when the alias declares `reasoning_efforts` and nothing is selected, send
  `reasoning: {effort: "none"}`. The adapter learns "this alias is reasoning-capable" via
  a new `ports.ModelRequest.ReasoningSupported bool`, set by the loop from a new
  `ModelTarget.Reasoning bool` (wired from `len(configured.ReasoningEfforts) > 0`).
- Behaviour change to flag: today an unselected effort sends nothing and reasoning-default-on
  models (Gemini, DeepSeek R1 via OpenRouter) reason anyway; after this they don't until a
  level is picked. Matches pi and matches what listing efforts on the alias implies.
- Other providers: `reasoning_effort` top-level unchanged; nothing sent when empty.

### C. Provider routing

Config (`ModelAliasConfig`):
```yaml
model_aliases:
  sonnet:
    provider: openrouter
    model: anthropic/claude-sonnet-4.6
    openrouter:
      order: [anthropic, amazon-bedrock]
      only: []
      ignore: [deepinfra]
      allow_fallbacks: true
      sort: price   # price | throughput | latency
```
- `OpenRouterRoutingConfig{Order, Only, Ignore []string; AllowFallbacks *bool; Sort string}`.
- Validation: `sort` ∈ {"", price, throughput, latency}; `only` and `ignore` disjoint;
  block only allowed when the alias's provider base URL is OpenRouter (error otherwise —
  a routing block on Groq is a mistake, not a no-op).
- Plumbing: `ModelTarget.ProviderRouting json.RawMessage` (marshalled once at wiring) →
  `ports.ModelRequest.ProviderRouting json.RawMessage` → adapter emits `provider` when
  `openRouter` and non-empty. Opaque past the config layer so the kernel stays vendor-blind.
- Mutation: `config.SetModelAlias` gains a `ModelAliasInput` struct (alias, provider,
  model, efforts, routing) replacing the five-string signature; empty routing clears it.
  `describe` line shows `openrouter=order:a,b sort:price`.
- Web: `config_routes.go` models section reads `openrouter_order`, `openrouter_only`,
  `openrouter_ignore`, `openrouter_allow_fallbacks`, `openrouter_sort` form fields; table
  gains a "Routing" column.
- Settings card: `ModelsCard.tsx` alias form shows a "Routing (OpenRouter)" disclosure —
  three comma-separated slug inputs, a sort select, an allow-fallbacks checkbox — only when
  the chosen provider's base URL is OpenRouter (the card already has provider rows).
- Chat command `/config models set` gets the same keys via `config.Values`.

### D. Usage

- Decode `prompt_tokens_details.cache_write_tokens` and top-level `cost`.
- `ports.ModelUsage` gains `CacheWriteTokens int64` and `CostUSD float64`; `Add` sums both.
- Trace usage line and `/status`-style summaries show them when non-zero.

### E. Errors

- `statusError` → `providerError(status int, body []byte)`: bounded read (8 KiB) of the
  response body, decode `{"error": {"message", "code", "metadata": {"raw", "provider_name"}}}`.
  Append `message`, then `provider_name: raw` when present and not already in `message`.
  Applies to every OpenAI-compatible provider, which all use the `error.message` shape.
- Retry path keeps its current transient classification; the body is read only on the
  final failing attempt. Authentication failures (401/403) are never quoted, keeping
  the existing rule that a provider echoing the key must not reach chat; the key is
  scrubbed from every other quoted body.

### F. Session affinity, cache control, developer role

- Add header `x-session-id` alongside body `session_id` (both documented; pi sends the
  header). Keep `~` model-prefix handling in `isAnthropicModel`.
- Cache control unchanged (top-level ephemeral is OpenRouter's recommendation for
  multi-turn). Add negative test.
- Developer role: pi *restricts* `developer` to `anthropic/*`/`openai/*`; Eggy only sends
  `system`, accepted everywhere. No code; one comment on `providerRequestMessage.Role`.

## Todos

- [x] 1. `ports`: add `Message.ProviderReasoning`/`ProviderReasoningOrigin`; add
      `ModelRequest.ReasoningSupported`, `ModelRequest.ProviderRouting`; add
      `ModelUsage.CacheWriteTokens`/`CostUSD` + `Add`; reword `ReasoningContent` comment.
- [x] 2. `agent`: `ModelTarget.Reasoning`, `ModelTarget.ProviderRouting`; loop passes both
      into `ModelRequest`. Loop test: assistant message with `ProviderReasoning` is
      replayed verbatim in the next round's request.
- [x] 3. `openaicompat` request: `x-session-id` header; nested `reasoning` (+ `"none"`);
      `provider` from routing; `reasoning_details` replay for origin `openrouter`.
      Non-OpenRouter shape unchanged. Tests for each, plus the existing
      "standard shape untouched" test extended.
- [x] 4. `openaicompat` response: decode `reasoning_details` → `ProviderReasoning`;
      `cache_write_tokens`, `cost` → usage. Test round-trip through two rounds.
- [x] 5. `openaicompat` errors: bounded body read + OpenRouter/OpenAI error decode.
      Test 400 with `metadata.raw`, test plain `error.message`, test empty body.
- [x] 6. `config`: `OpenRouterRoutingConfig` on `ModelAliasConfig`, validation
      (sort enum, only/ignore disjoint, OpenRouter-only), `ModelAliasInput` +
      `SetModelAlias` refactor, `describe` line. Tests in `config_validate_test` /
      `config_mutate_test`.
- [x] 7. `bootstrap`: wire `Reasoning` and marshalled `ProviderRouting` into targets.
- [x] 8. `web`: models section reads/writes routing fields; table column; `models_test`.
- [x] 9. `commands`: `/config models set` accepts the routing keys; `commands_test`.
- [x] 10. `website/src/ModelsCard.tsx`: routing fields under *Advanced options* on the
      alias form; edit round-trips from the Routing column; `website/tests` case.
      (Shown always with an "OpenRouter only" note rather than gated per provider:
      the card does not hold provider base URLs, and the server refuses the block
      on any other provider anyway.)
- [x] 11. Traces: `tracedMessage` already projects only known fields, so the blob is
      omitted with no change; `web/traces.go` + `TracesPage.tsx` show cache-write
      tokens and cost when reported.
- [x] 12. Docs: `configure/model-providers.md` OpenRouter section (routing, reasoning off,
      replay note, cost in traces); `use/models.md` mention of the routing control.
- [x] 13. `go vet ./... && go test ./...`; `website` tests; commit to main.

## Follow-up (same day): catalog-driven efforts and gated routing fields

OpenRouter's `/models` carries a per-model `reasoning` object
(`{mandatory, supported_efforts, default_effort}`), which changes two things above:

- `effort: "none"` is only valid where `mandatory` is false. The adapter now fetches
  the catalog once per process (retrying after a failed fetch) and sends `none` only
  for a model it says can be switched off. Unlisted or mandatory → nothing sent.
- `ports.CatalogModel.Reasoning{Mandatory, Efforts}` is filled from it; the browse
  table has an "Reasoning efforts" column, picking a row pre-fills an empty
  `reasoning_efforts`, and `/model available` prints the efforts. The accepted set
  widened to `minimal|low|medium|high|xhigh|max`; unknown levels are dropped before
  they reach config.
- The models section now carries `fields: [{label: openrouter_providers}]`, and the
  card shows the routing fields only when the typed provider is one of them. Routing
  is sent only in that case, so moving an alias off OpenRouter drops stale routing.
