# Native Web Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an optional provider-neutral `web_search` tool, initially backed by SearXNG whenever `WEB_SEARCH_API` is set.

**Architecture:** A kernel-owned tool exposes one stable model schema and calls a narrow `ports.WebSearcher`. The SearXNG adapter owns HTTP and wire-format details; bootstrap resolves the opt-in environment variable, selects the adapter, and conditionally registers the tool.

**Tech Stack:** Go 1.26, standard-library `net/http` and `encoding/json`, existing YAML bootstrap configuration, fake `http.RoundTripper` tests.

## Global Constraints

- `internal/kernel` and `internal/ports` remain provider-neutral.
- SearXNG request/response types remain inside `internal/adapters/search/searxng`.
- `WEB_SEARCH_API` is the sole opt-in; unset or blank means no registered tool and no startup change.
- `SEARXNG_SECRET` never enters Eggy's configuration, environment requirements, prompts, logs, or tool results.
- A configured but temporarily unavailable endpoint does not block startup; the individual tool call returns a bounded error.
- Search is read-only and is not added to scheduled or heartbeat allowlists.
- Use only the standard library and existing project dependencies.
- Preserve `/data/state.json` schema unchanged.
- Run `make fmt vet test race build`; run `make smoke` only when Docker is available.

---

### Task 1: Provider-neutral search port and kernel tool

**Files:**
- Modify: `internal/ports/ports.go`
- Create: `internal/kernel/services/web_search_tool.go`
- Create: `internal/kernel/services/web_search_tool_test.go`

**Interfaces:**
- Consumes: existing `ports.Tool`, `ports.ToolDefinition`, and strict JSON tool-input behavior.
- Produces:

```go
type WebSearchRequest struct {
	Query string
	Limit int
}

type WebSearchResult struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Snippet     string   `json:"snippet,omitempty"`
	PublishedAt string   `json:"published_at,omitempty"`
	Sources     []string `json:"sources,omitempty"`
}

type WebSearcher interface {
	Search(context.Context, WebSearchRequest) ([]WebSearchResult, error)
}

func NewWebSearchTool(searcher ports.WebSearcher, defaultResults int) ports.Tool
```

- [ ] **Step 1: Write failing kernel-tool tests**

Create `web_search_tool_test.go` with a recording provider:

```go
type recordingWebSearcher struct {
	request ports.WebSearchRequest
	results []ports.WebSearchResult
	err     error
}

func (s *recordingWebSearcher) Search(_ context.Context, request ports.WebSearchRequest) ([]ports.WebSearchResult, error) {
	s.request = request
	return s.results, s.err
}
```

Cover these exact cases:

```go
func TestWebSearchToolDefinition(t *testing.T)
func TestWebSearchToolUsesConfiguredDefault(t *testing.T)
func TestWebSearchToolAcceptsBoundedOverride(t *testing.T)
func TestWebSearchToolRejectsInvalidInput(t *testing.T)
func TestWebSearchToolPropagatesProviderError(t *testing.T)
```

Assert the definition name is `web_search`, `query` is required, unknown JSON
fields fail, blank queries fail, and `max_results` outside `1..20` fails. For a
provider result, assert the emitted JSON contains `query` and the normalized
`results`.

- [ ] **Step 2: Run the focused test and confirm it fails**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod \
  go test ./internal/kernel/services -run WebSearch -count=1
```

Expected: compile failure because `ports.WebSearcher`,
`ports.WebSearchRequest`, and `NewWebSearchTool` do not exist.

- [ ] **Step 3: Add the neutral port**

Add the exact types from this task's Interfaces section beside `ports.Tool` in
`internal/ports/ports.go`. Do not add provider names, HTTP fields, API keys, or
SearXNG response structures.

- [ ] **Step 4: Implement the minimal kernel tool**

Create `web_search_tool.go` with:

```go
type webSearchTool struct {
	searcher       ports.WebSearcher
	defaultResults int
}

type webSearchInput struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}

type webSearchOutput struct {
	Query   string                  `json:"query"`
	Results []ports.WebSearchResult `json:"results"`
}
```

`Definition()` returns this schema:

```json
{
  "type": "object",
  "properties": {
    "query": {"type": "string", "minLength": 1},
    "max_results": {"type": "integer", "minimum": 1, "maximum": 20}
  },
  "required": ["query"],
  "additionalProperties": false
}
```

`Execute()` must:

1. call the existing package-level `decodeStrict`;
2. trim and reject an empty query;
3. use `defaultResults` when `max_results` is zero;
4. reject effective limits outside `1..20`;
5. call `searcher.Search(ctx, ports.WebSearchRequest{Query: query, Limit: limit})`;
6. preserve a non-nil empty result slice in JSON; and
7. return provider errors unchanged so the agent loop uses its established
   bounded tool-error path.

`NewWebSearchTool` must normalize an invalid configured default to `8`, never
panic, and return a `ports.Tool`.

- [ ] **Step 5: Run the focused test and confirm it passes**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod \
  go test ./internal/kernel/services -run WebSearch -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the neutral contract and tool**

```bash
git add internal/ports/ports.go \
  internal/kernel/services/web_search_tool.go \
  internal/kernel/services/web_search_tool_test.go
git commit -m "feat: add provider-neutral web search tool"
```

---

### Task 2: SearXNG HTTP adapter

**Files:**
- Create: `internal/adapters/search/searxng/adapter.go`
- Create: `internal/adapters/search/searxng/adapter_test.go`

**Interfaces:**
- Consumes:

```go
ports.WebSearcher
ports.WebSearchRequest
ports.WebSearchResult
```

- Produces:

```go
type Config struct {
	BaseURL     string
	Timeout     time.Duration
	SafeSearch int
	MaxBytes   int64
}

func New(config Config, client *http.Client) (*Adapter, error)
func (a *Adapter) Search(context.Context, ports.WebSearchRequest) ([]ports.WebSearchResult, error)
```

- [ ] **Step 1: Write failing adapter tests with a fake transport**

Define:

```go
type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
```

Create tests:

```go
func TestNewValidatesSearXNGConfig(t *testing.T)
func TestSearchMapsSearXNGJSON(t *testing.T)
func TestSearchHonorsLimitAndEncodesQuery(t *testing.T)
func TestSearchRejectsNonSuccessStatus(t *testing.T)
func TestSearchRejectsMalformedAndOversizedResponses(t *testing.T)
func TestSearchPropagatesCancellation(t *testing.T)
```

The successful fake response is:

```json
{
  "results": [
    {
      "title": "Eggy",
      "url": "https://example.com/eggy",
      "content": "A personal agent",
      "publishedDate": "2026-07-26T10:00:00Z",
      "engine": "duckduckgo",
      "engines": ["duckduckgo", "brave"]
    }
  ]
}
```

Assert the outbound URL path is `/search`, `q` is unchanged after decoding,
`format=json`, and `safesearch=1`. Assert sources are de-duplicated and sorted,
with `engine` folded into the same list.

Validation cases must reject:

- missing scheme or host;
- schemes other than `http` and `https`;
- embedded URL credentials;
- a base URL containing a query or fragment;
- non-positive timeout; and
- non-positive response cap.

- [ ] **Step 2: Run adapter tests and confirm they fail**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod \
  go test ./internal/adapters/search/searxng -count=1
```

Expected: package or symbol-not-found failure.

- [ ] **Step 3: Implement constructor and URL validation**

`New` must parse and copy the configured URL, strip only trailing slashes from
its path, retain any legitimate path prefix, and store the supplied client (or
`http.DefaultClient` when nil). It must not make a network request.

Use these defaults only when bootstrap supplies them; the adapter constructor
itself rejects incomplete configuration instead of silently changing it.

- [ ] **Step 4: Implement the bounded SearXNG request**

Use a child context:

```go
requestContext, cancel := context.WithTimeout(ctx, a.timeout)
defer cancel()
```

Append `/search` to the configured path and set:

```go
values.Set("q", request.Query)
values.Set("format", "json")
values.Set("safesearch", strconv.Itoa(a.safeSearch))
```

Read at most `MaxBytes + 1` bytes. If more than `MaxBytes` are returned, fail
with `SearXNG response exceeds configured limit`. For non-2xx responses, return
`SearXNG search returned HTTP <status>` without reading or returning the body.

Decode only adapter-local wire types:

```go
type response struct {
	Results []result `json:"results"`
}

type result struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Content       string   `json:"content"`
	PublishedDate string   `json:"publishedDate"`
	Engine        string   `json:"engine"`
	Engines       []string `json:"engines"`
}
```

Bound fields before returning:

- title: 512 bytes;
- URL: 2048 bytes;
- snippet: 4096 bytes;
- published timestamp: 128 bytes;
- each source: 128 bytes;
- sources: at most 16.

Use a UTF-8-safe helper that truncates by bytes without returning an invalid
string. Skip results whose URL is blank. Stop at `request.Limit`.

- [ ] **Step 5: Run adapter tests and confirm they pass**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod \
  go test ./internal/adapters/search/searxng -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the SearXNG adapter**

```bash
git add internal/adapters/search/searxng
git commit -m "feat: add SearXNG search adapter"
```

---

### Task 3: Optional configuration, fake adapter, and bootstrap registration

**Files:**
- Modify: `internal/bootstrap/config.go`
- Modify: `internal/bootstrap/config_init.go`
- Modify: `internal/bootstrap/config_test.go`
- Modify: `internal/bootstrap/app.go`
- Create: `internal/bootstrap/web_search.go`
- Create: `internal/bootstrap/web_search_test.go`
- Modify: `internal/bootstrap/primitive_tools_test.go`

**Interfaces:**
- Consumes:

```go
searxng.New(searxng.Config, *http.Client)
services.NewWebSearchTool(ports.WebSearcher, int)
```

- Produces:

```go
type WebSearchConfig struct {
	Adapter    string   `yaml:"adapter"`
	BaseURLEnv string   `yaml:"base_url_env"`
	APIKeyEnv  string   `yaml:"api_key_env,omitempty"`
	Timeout    Duration `yaml:"timeout"`
	MaxResults int      `yaml:"max_results"`
	SafeSearch int      `yaml:"safe_search"`
}

type Secrets struct {
	// existing fields...
	WebSearchBaseURL string
	WebSearchAPIKey  string
}

func newWebSearcher(config Config, secrets Secrets, options AppOptions) (ports.WebSearcher, error)
```

- [ ] **Step 1: Write failing optional-config tests**

Add tests proving:

```go
func TestWebSearchDefaultsAreOptional(t *testing.T)
func TestLoadConfigResolvesWebSearchEnvironment(t *testing.T)
func TestWebSearchConfigValidation(t *testing.T)
```

`TestWebSearchDefaultsAreOptional` loads the existing `validConfig()` with no
`WEB_SEARCH_API`, expects no error, and asserts:

```go
cfg.WebSearch.Adapter == "searxng"
cfg.WebSearch.BaseURLEnv == "WEB_SEARCH_API"
cfg.WebSearch.Timeout.Value() == 15*time.Second
cfg.WebSearch.MaxResults == 8
cfg.WebSearch.SafeSearch == 1
secrets.WebSearchBaseURL == ""
```

`TestLoadConfigResolvesWebSearchEnvironment` sets
`WEB_SEARCH_API=https://search.example.com`, expects the value only in
`Secrets.WebSearchBaseURL`, and asserts marshaled configuration contains the
environment-variable name but never the resolved URL.

Validation cases reject invalid adapter names, invalid environment-variable
names, non-positive timeout/result limits, result limits over 20, and safe
search outside `0..2`. A malformed resolved URL, credentials, query, or fragment
fails only when `WEB_SEARCH_API` is nonblank.

- [ ] **Step 2: Run focused config tests and confirm they fail**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod \
  go test ./internal/bootstrap -run WebSearch -count=1
```

Expected: compile failure because web-search configuration does not exist.

- [ ] **Step 3: Implement configuration and secret resolution**

Add `WebSearch WebSearchConfig` to `Config` and `commonConfigDocument`, then
thread it through `normalizeConfig`, `commonDocument`, and YAML marshaling.

In `applyDefaults`, set:

```go
if c.WebSearch.Adapter == "" {
	c.WebSearch.Adapter = "searxng"
}
if c.WebSearch.BaseURLEnv == "" {
	c.WebSearch.BaseURLEnv = "WEB_SEARCH_API"
}
if c.WebSearch.Timeout == 0 {
	c.WebSearch.Timeout = Duration(15 * time.Second)
}
if c.WebSearch.MaxResults == 0 {
	c.WebSearch.MaxResults = 8
}
if c.WebSearch.SafeSearch == 0 {
	c.WebSearch.SafeSearch = 1
}
```

Because zero is a valid SearXNG safe-search value, represent an explicitly
configured zero without default ambiguity by adding a private
`safeSearchConfigured bool` during YAML normalization, or use a YAML-facing
pointer field and normalize it into the runtime integer. Do not make `0`
impossible to configure.

Resolve:

```go
secrets.WebSearchBaseURL = strings.TrimSpace(getenv(cfg.WebSearch.BaseURLEnv))
if cfg.WebSearch.APIKeyEnv != "" {
	secrets.WebSearchAPIKey = getenv(cfg.WebSearch.APIKeyEnv)
}
```

Validate the resolved URL only when nonblank. SearXNG never requires
`WebSearchAPIKey`.

- [ ] **Step 4: Add fake and real adapter construction**

Create `web_search.go`. When `secrets.WebSearchBaseURL` is blank, return
`(nil, nil)`. When `options.FakeAdapters` is true, return:

```go
type fakeWebSearcher struct{}

func (fakeWebSearcher) Search(_ context.Context, request ports.WebSearchRequest) ([]ports.WebSearchResult, error) {
	return []ports.WebSearchResult{{
		Title: "Fake web search result",
		URL:   "https://example.com/search?q=" + url.QueryEscape(request.Query),
	}}, nil
}
```

For real mode, switch on `config.WebSearch.Adapter`. The `searxng` branch calls
`searxng.New` with the resolved base URL, configured timeout and safe-search,
`MaxBytes: 1 << 20`, and `options.HTTPClient`. The default branch returns
`unsupported web search adapter "<name>"`.

- [ ] **Step 5: Register only the configured tool**

In `NewApp`, call `newWebSearcher` after the registry is created and before MCP
registration. When the result is non-nil, register:

```go
services.NewWebSearchTool(searcher, config.WebSearch.MaxResults)
```

Do not add an `App` field unless runtime lifecycle management needs it; the
SearXNG adapter has no close operation.

Add bootstrap tests:

```go
func TestWebSearchToolIsAbsentWithoutEnvironment(t *testing.T)
func TestWebSearchToolIsRegisteredWhenConfigured(t *testing.T)
func TestFakeWebSearchRegistrationMakesNoNetworkCall(t *testing.T)
```

Use `app.loop.ToolNames(agent.RunOptions{})` and `slices.Contains`. Also assert
`web_search` is absent from `readOnlyRunOptions()` and
`heartbeatRunOptions()`.

- [ ] **Step 6: Run focused bootstrap tests and confirm they pass**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod \
  go test ./internal/bootstrap -run WebSearch -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit optional bootstrap wiring**

```bash
git add internal/bootstrap/config.go \
  internal/bootstrap/config_init.go \
  internal/bootstrap/config_test.go \
  internal/bootstrap/app.go \
  internal/bootstrap/web_search.go \
  internal/bootstrap/web_search_test.go \
  internal/bootstrap/primitive_tools_test.go
git commit -m "feat: register optional native web search"
```

---

### Task 4: Deployment documentation and full verification

**Files:**
- Modify: `.env.example`
- Modify: `config.example.yaml`
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `internal/bootstrap/docs_consistency_test.go`

**Interfaces:**
- Consumes: the completed `WEB_SEARCH_API` optional configuration.
- Produces: deployment instructions that keep SearXNG and Eggy variables
  distinct.

- [ ] **Step 1: Write a failing documentation-consistency test**

Add a test asserting all operational surfaces mention the opt-in:

```go
func TestWebSearchDocumentationStaysConsistent(t *testing.T) {
	for _, path := range []string{"../../.env.example", "../../config.example.yaml", "../../README.md"} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(body, []byte("WEB_SEARCH_API")) {
			t.Fatalf("%s does not document WEB_SEARCH_API", path)
		}
	}
}
```

- [ ] **Step 2: Run the documentation test and confirm it fails**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod \
  go test ./internal/bootstrap -run WebSearchDocumentation -count=1
```

Expected: FAIL because the examples do not yet mention `WEB_SEARCH_API`.

- [ ] **Step 3: Document exact configuration**

Add to `.env.example`:

```dotenv
# Optional: registers the native web_search tool using the configured adapter.
# For SearXNG this is its reachable base URL; Eggy never needs SEARXNG_SECRET.
WEB_SEARCH_API=
```

Add the exact `web_search` block from the approved spec to
`config.example.yaml`.

Document in README:

- SearXNG keeps `SEARXNG_BASE_URL` and `SEARXNG_SECRET` on its own Railway
  service.
- Eggy receives only `WEB_SEARCH_API=https://<searxng-host>/`.
- Unset means the tool is absent and Eggy behaves normally.
- JSON must be enabled in SearXNG `search.formats`.
- The verification command is:

```bash
curl -fsS "$WEB_SEARCH_API/search?q=Eggy&format=json"
```

Update `docs/ARCHITECTURE.md` in the adapter/tool sections, preserving its
single existing Mermaid overview rather than adding a second diagram.

- [ ] **Step 4: Run documentation and focused package tests**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod \
  go test ./internal/ports ./internal/kernel/services \
  ./internal/adapters/search/searxng ./internal/bootstrap -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit documentation**

```bash
git add .env.example config.example.yaml README.md docs/ARCHITECTURE.md \
  internal/bootstrap/docs_consistency_test.go
git commit -m "docs: explain optional SearXNG web search"
```

- [ ] **Step 6: Run the repository-required verification matrix**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod \
  make fmt vet test race build
```

Expected: every target exits zero.

- [ ] **Step 7: Run Docker smoke when available**

Check:

```bash
docker info
```

If it exits zero, run:

```bash
make smoke
```

If the daemon is unavailable, record the exact Docker error and do not present
it as an application regression.

- [ ] **Step 8: Review final scope and history**

Run:

```bash
git status --short
git diff --check HEAD~4..HEAD
git log -5 --oneline
```

Expected: no unintended files, no whitespace errors, and four focused feature
commits after the design/plan checkpoints.
