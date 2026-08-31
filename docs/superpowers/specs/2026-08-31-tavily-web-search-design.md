# Tavily web search and page extraction

Date: 2026-08-31
Status: approved, ready for implementation plan

## Problem

Eggy cannot read the open web. Everything it knows arrives from the model's
weights, the owner, a configured Google product, or a configured MCP server.
For a personal agent that is a hole, not a stylistic gap.

The capability existed once and was deleted in `3c2873f` (2026-07-28), which
removed three providers (SearXNG, Tavily, Google CSE), the `web_search` kernel
tool, the `ports.WebSearcher` port, config, and docs. The stated reason was
that frontier models can invoke `curl` through the terminal tool.

That reason does not hold in general. The terminal primitives are only
registered when `config.Repositories` is non-empty (`internal/bootstrap/app.go`),
so an owner who runs Eggy without repositories has no `curl` and therefore no
web reach at all. `curl` also returns raw HTML, which costs far more context
than a ranked snippet and needs the model to parse markup it should never see.

`TODO.md` R2 answers the hole with "this is an MCP server, not a core tool",
budget 0 production lines and 0 tools. That path is not taken here: the owner
holds a Tavily API key, not an MCP endpoint, and no server was evaluated as
acceptable. R2's own escape hatch covers this -- "If it turns out no server is
acceptable, that is the argument for a core tool -- make it explicitly." This
document is that argument, made explicitly.

## What is built

Two tools, behind one config flag.

- `web_search` -- ranked results with snippets.
- `web_extract` -- cleaned page text for URLs the model names.

They are deliberately two calls, not one. Tavily's search `content` field is a
snippet (up to three ranked chunks per source), not the page. Reading an actual
website is the second call. The tool descriptions say so, so the model chains
them rather than assuming search returned the page.

## Cost accepted

Two tool schemas on every model call, for owners who set `tavily.enabled`.
Zero schema bytes, zero client, zero code path for everyone else -- the same
rule `google.products` already follows, and the reason the gate is a config
flag rather than an unconditional registration.

Footprint: ~450 production lines across 5 new files, 1 config section, 1
secret, 2 tools, 0 durable records, 0 background loops, 0 new ports.

## Architecture

New package `plugins/tools/tavily`, shaped like `plugins/tools/google`: the
adapter owns both the HTTP client and the `ports.Tool` implementations, and
bootstrap only classifies and registers.

| File | Responsibility |
|---|---|
| `client.go` | `Config`, bearer-auth POST helper, status to error mapping, output bounding |
| `search.go` | `Search(ctx, SearchRequest) (SearchResponse, error)` |
| `extract.go` | `Extract(ctx, ExtractRequest) (ExtractResponse, error)` |
| `tools.go` | `NewTools(*Client, Config) []ports.Tool` -- the two tool schemas |
| `tavily_test.go` | `httptest`-driven tests |

`internal/ports/ports.go` is not touched. The deleted design carried a
`ports.WebSearcher` port because it had three providers behind it; there is one
provider now. A port earns its place at the second adapter, not the first.

### Data flow

```
model -> web_search {query}
      -> tavily.Search -> POST api.tavily.com/search
      <- {results:[{title,url,content,score}]}          bounded
model -> web_extract {urls: [chosen from results]}
      -> tavily.Extract -> POST api.tavily.com/extract
      <- {results:[{url,content}], failed:[{url,error}]} bounded
```

## API surface (verified against docs.tavily.com, 2026-08-31)

### POST https://api.tavily.com/search

Auth: `Authorization: Bearer tvly-...`

Request fields used: `query`, `search_depth` (basic | advanced | fast |
ultra-fast; advanced costs 2 credits, others 1), `max_results` (0-20, default
5), `topic` (general | news | finance), `time_range` (day | week | month |
year). Every other documented field is left at its default; `include_answer`,
`include_images` and `include_raw_content` are explicitly not sent -- an answer
is the model's job, images cannot be rendered, and raw content on search is
what `web_extract` is for.

Response fields used, per result: `title`, `url`, `content`, `score`.

### POST https://api.tavily.com/extract

Auth: same.

Request fields used: `urls` (array, Tavily allows max 20), `query` (optional,
reranks chunks), `extract_depth` (basic | advanced), `format` (markdown).

Response: `results[]` with `url` and `raw_content`; `failed_results[]` with
`url` and `error`.

Credits: basic is 1 credit per 5 successful extractions, advanced 2 per 5.

## Tool definitions

Both are classified `ports.ReadOnlyTool()`. Searching and reading public pages
change nothing, so `ModeNormal` does not prompt; `ModeStrict` still gates them
like every other tool.

### web_search

Description states that results carry snippets, and that reading a page means
calling `web_extract` with its URL.

```json
{"type":"object",
 "properties":{
   "query":{"type":"string","minLength":1},
   "max_results":{"type":"integer","minimum":1,"maximum":20},
   "topic":{"type":"string","enum":["general","news","finance"]},
   "time_range":{"type":"string","enum":["day","week","month","year"]}},
 "required":["query"],
 "additionalProperties":false}
```

Output: `{"query":string,"results":[{"title","url","content","score"}],"truncated":bool}`

`max_results` omitted falls back to `tavily.max_results`. Out-of-range values
are rejected in `Execute` rather than clamped, so a model asking for 50 learns
it cannot instead of silently getting 20.

### web_extract

```json
{"type":"object",
 "properties":{
   "urls":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":5},
   "query":{"type":"string"}},
 "required":["urls"],
 "additionalProperties":false}
```

Output: `{"results":[{"url","content","truncated":bool}],"failed":[{"url","error"}]}`

`urls` is capped at 5, not Tavily's 20. Twenty pages of extracted text in one
turn is the actual failure mode this tool has, and 5 is exactly one credit at
basic depth. An owner who needs more calls the tool twice.

A call where every URL fails still returns successfully with a populated
`failed` array -- that is a result the model can act on, not a tool error.

## Output bounding

The one real risk. Extracted page text is unbounded, and a single long article
can consume a turn's context by itself.

Every response is bounded by `tavily.max_output_bytes` (default 65536):

1. Each result's text is truncated to `max_output_bytes / len(results)` first,
   so one enormous page cannot starve the other four.
2. Any remaining budget is not redistributed. Simplicity beats packing here;
   the model can ask again for a specific URL.
3. A truncated result carries `truncated: true`, so the model knows it is
   reading a fragment rather than a short page.

Truncation is on UTF-8 rune boundaries, never mid-rune. This mirrors the
`MaxOutputBytes` discipline `googleadapter.Config` already carries.

## Configuration

```yaml
tavily:
  enabled: true
  api_key_env: TAVILY_API_KEY
  search_depth: basic       # basic | advanced | fast | ultra-fast
  extract_depth: basic      # basic | advanced
  max_results: 5
  max_output_bytes: 65536
  timeout: 30s
```

`TavilyConfig` joins `Config` in `internal/config/config.go`. Defaults are
applied in `applyDefaults` alongside the other sections: `api_key_env` defaults
to `TAVILY_API_KEY`, `search_depth` and `extract_depth` to `basic`,
`max_results` to 5, `max_output_bytes` to 65536, `timeout` to 30s.

Validation, at load, only when `enabled`:

- resolved API key is non-empty, else a config error naming the env var
- `search_depth` and `extract_depth` are in their enums
- `max_results` is 1-20
- `max_output_bytes` is at least 4096

`Secrets.TavilyAPIKey` is added and included in `Secrets.Values()`, which puts
it under the existing generalized secret-redaction test in `config_test.go`
without writing a new one.

## Bootstrap wiring

`internal/bootstrap/tavily.go`:

```go
func newTavilyTools(cfg config.Config, secrets config.Secrets, options AppOptions) ([]ports.Tool, error)
```

Returns `nil, nil` when `!cfg.Tavily.Enabled` -- an absent capability builds no
client and costs nothing. Otherwise builds the client on `options.HTTPClient`
(falling back to `http.DefaultClient`) so tests inject a transport, and returns
the two tools.

Registered in `internal/bootstrap/app.go` through the existing
`registerGated(registry, asker, app.approvals, ...)` path, placed next to the
Google block. The registry rejects duplicate names, so a collision fails
bootstrap rather than silently shadowing.

## Error handling

Status codes map to errors with no retry loop -- one call is one credit, and a
retry the owner did not ask for is a charge they did not authorise:

| Status | Error |
|---|---|
| 401 | Tavily API key rejected (names `api_key_env`) |
| 429 | Tavily rate limit exceeded |
| 432 | Tavily plan limit reached |
| 433 | Tavily pay-as-you-go limit reached |
| 400 | invalid request, with the bounded response body |
| other | status code with a bounded body snippet |

Body snippets in errors are capped at 512 bytes so a provider error page cannot
become a context problem. Transport errors and context cancellation propagate
unwrapped in cause but wrapped in message. Errors surface to the model as tool
errors, which the loop already presents; the model can report or retry itself.

## Testing

`plugins/tools/tavily/tavily_test.go`, against an `httptest.Server`:

- search request carries the bearer header, the query, and configured depth
- `max_results` omitted uses the config default; out-of-range is rejected
- search response maps to the output shape
- extract sends `format: markdown` and the URL array
- extract with a mix of successes and failures populates both arrays
- extract where every URL fails returns a result, not an error
- truncation exactly at the boundary: one byte under, exactly at, one over
- truncation never splits a multi-byte rune
- each error status maps to its message, and body snippets are capped
- context cancellation propagates

`internal/config`: defaults applied, each validation rule rejects, and the key
is redacted (covered by the existing generalized test once it is in `Values()`).

`internal/bootstrap`: disabled config registers no tools; enabled config
registers exactly `web_search` and `web_extract`.

`config.example.yaml` carries the block, checked by the existing
`docs_consistency_test.go` pattern.

## Documentation

- `config.example.yaml` -- the block above, commented
- `.env.example` -- `TAVILY_API_KEY`
- `README.md` -- web reach in the capability list
- `docs/src/content/docs/` -- a page covering setup, the two-call pattern, and
  credit cost
- `TODO.md` R2 -- deleted. The design first called for rewriting it in place,
  but the file's own rule is "Unfinished work only. Delete an item once it
  lands", and completed work lives in git. The reversal argument is recorded
  here in this spec and in the package comment on plugins/tools/tavily, which
  is where someone asking "why is this a core tool?" will actually look.

## Out of scope

- Provider neutrality. One provider, no port, no registry. The second provider
  is when the abstraction is designed, and it is designed against two real
  ones.
- `include_answer`. Summarising is the model's job and it has the sources.
- Images, favicons, `auto_parameters`, `include_domains` / `exclude_domains`,
  `country` / `language`. All reachable later; none earn schema bytes now.
- Caching results. No durable record is created by this feature.
