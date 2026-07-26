# Native provider-neutral web search

## Goal

Give direct owner turns an optional `web_search` tool backed initially by the
owner's SearXNG deployment. The search capability must be absent when it is not
configured, must not change startup or ordinary conversation for existing
deployments, and must admit future providers such as Exa or Tavily without
changing the kernel tool contract.

`WEB_SEARCH_API` is the opt-in switch and contains the selected provider's base
URL. An unset or blank value means web search is disabled. SearXNG requires no
Eggy-side API key; `SEARXNG_SECRET` remains private to the SearXNG service and
must not be copied into Eggy.

## Approaches considered

### Project SearXNG through the existing MCP adapter

This would avoid native provider code, but the deployed SearXNG HTTP API is not
an MCP server. It would require another service to translate SearXNG into MCP
and would expose a server-namespaced tool rather than one stable Eggy
`web_search` capability.

### Make every search adapter implement `ports.Tool`

This is small for the first provider, but it duplicates the model-facing schema,
normalization, and output bounds in every future adapter. Provider changes could
then alter the agent contract.

### Add a narrow search port and one kernel-owned tool

This is the selected approach. The kernel owns the stable `web_search` schema,
while a provider-neutral port carries search requests and normalized results.
SearXNG implements the port. Future providers add an adapter package and
bootstrap selection without changing the port or tool.

## Architecture

The dependency flow is:

```text
direct owner agent loop
  -> kernel web_search tool
  -> ports.WebSearcher
  -> internal/adapters/search/searxng
  -> configured SearXNG /search JSON API
```

`internal/ports/ports.go` gains a narrow `WebSearcher` interface and neutral
request/result types. They contain no SearXNG, Exa, Tavily, HTTP, credential, or
wire-format concepts.

The provider-neutral contract is:

```go
type WebSearchRequest struct {
	Query string
	Limit int
}

type WebSearchResult struct {
	Title       string
	URL         string
	Snippet     string
	PublishedAt string
	Sources     []string
}

type WebSearcher interface {
	Search(context.Context, WebSearchRequest) ([]WebSearchResult, error)
}
```

`PublishedAt` remains an optional string because providers do not expose one
consistent timestamp representation. Adapters normalize a timestamp when they
can and omit it otherwise. `Sources` identifies contributing search engines or
indexes without exposing provider wire objects.

`internal/kernel/services/web_search_tool.go` owns the model-facing tool. Its
input schema accepts:

- `query`: required, non-blank string.
- `max_results`: optional integer from 1 through 20.

The configured default and ceiling are applied in the kernel tool before the
port call. The tool returns the effective query and a bounded list of normalized
results. Search is read-only and requires no approval.

The tool is part of the full direct-owner tool surface. It is not added to the
explicit scheduled or heartbeat allowlists in this version.

## Configuration and optional behavior

Bootstrap gains provider-neutral web-search configuration:

```yaml
web_search:
  adapter: "searxng"
  base_url_env: "WEB_SEARCH_API"
  api_key_env: ""
  timeout: "15s"
  max_results: 8
  safe_search: 1
```

These defaults are applied when the block is omitted, so old persisted
configuration remains valid. `base_url_env` names an environment variable; it
does not contain the URL itself. `api_key_env` is optional for SearXNG and is
reserved for adapters that genuinely require a credential.

Runtime behavior is:

1. If `WEB_SEARCH_API` is unset or blank, bootstrap does not construct a search
   adapter and does not register `web_search`.
2. If it is present, bootstrap validates the resolved URL and selects the
   configured adapter.
3. An unsupported adapter or malformed configured URL is a startup
   configuration error.
4. Bootstrap does not probe the endpoint. A temporary provider outage therefore
   does not prevent Eggy from starting or remove the tool from the capability
   manifest.
5. A failed call returns a bounded tool error to the loop. It does not crash
   Eggy or affect later ordinary conversation.

The initial selector accepts only `searxng`. Adding Exa or Tavily later adds its
adapter package and a bootstrap selector branch. It does not change
`internal/kernel`, `internal/ports`, or the SearXNG adapter.

`.env.example`, `config.example.yaml`, the generated first-boot configuration,
and the README document `WEB_SEARCH_API`. Existing deployments need no new
environment variables.

## SearXNG adapter

`internal/adapters/search/searxng` implements `ports.WebSearcher` using the
standard library HTTP client.

For each call it:

1. Resolves the configured base URL to `/search`.
2. sends a `GET` request with URL-encoded `q`, `format=json`, and the configured
   `safesearch`;
3. applies the configured timeout through the request context;
4. rejects non-2xx responses with a short status-based error;
5. caps the response body before decoding JSON;
6. maps at most the requested number of results into neutral result values; and
7. bounds individual title, URL, snippet, timestamp, and source values before
   returning them.

The adapter accepts configured `http` or `https` endpoints because a trusted
SearXNG instance may be reached through private service networking. It rejects
URLs with embedded credentials, query strings, or fragments. The URL is
configuration-only and can never be supplied through a tool call.

SearXNG's `content` becomes `Snippet`. Its engine/engines fields become
`Sources`. Unknown response fields are ignored so compatible SearXNG upgrades do
not break the adapter.

## Fake adapters and registration

Fake-adapter mode supplies an in-memory `WebSearcher` only when
`WEB_SEARCH_API` is configured. This lets bootstrap and smoke tests verify
conditional registration without making network calls.

The existing `ToolRegistry` remains the collision boundary. Registration fails
if any other native or MCP tool attempts to claim `web_search`.

The capability manifest is derived from the final registry as it is today:
configured search includes `web_search`; absent search does not.

## Errors and safety

- Blank queries and invalid result limits fail before the adapter call.
- Provider errors contain no response body, credentials, or environment values.
- Search output is untrusted external text. It is returned only as a tool result
  and grants no capabilities or authorization.
- Request time, response bytes, result count, and individual string sizes are
  bounded.
- Cancellation propagates through the tool, port, and HTTP request.
- No search configuration or result is written to `/data/state.json`, so its
  schema remains unchanged.
- `SEARXNG_SECRET` is not an Eggy credential and never enters Eggy's environment,
  prompts, logs, or tool results.

## Testing

Implementation is test-first and includes:

- Kernel tool tests for its definition, strict input decoding, defaults, limit
  validation, normalized output, and provider error propagation.
- SearXNG adapter tests using a fake `http.RoundTripper` for query encoding,
  safe-search propagation, JSON mapping, result limits, cancellation,
  non-success statuses, malformed JSON, and oversized bodies.
- Bootstrap configuration tests proving omitted or blank `WEB_SEARCH_API` is
  optional, configured values resolve through the named environment variable,
  malformed URLs fail, and existing configs still load.
- Bootstrap registration tests proving `web_search` appears only when configured
  and that fake-adapter mode performs no network access.
- Documentation consistency coverage for the example YAML and environment
  variable.

Focused tests run before the complete required verification:

```text
make fmt vet test race build
```

`make smoke` is run when Docker is available; an unavailable Docker daemon is
reported separately rather than treated as an application failure.
