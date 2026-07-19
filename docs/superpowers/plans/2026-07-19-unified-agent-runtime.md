# Unified Agent Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route every non-command Telegram message through one configurable OpenAI-compatible agent using DeepSeek Pro by default, with truthful context, curated memory, persistent model usage, and separate read-only and modifying Codex repository tools.

**Architecture:** Normalize legacy and version 2 YAML into one bootstrap configuration, register named OpenAI-compatible models behind `ports.Model`, and select an alias from schema-versioned state. Build each turn from compiled policy, a generated capability manifest, `SOUL.md`, `USER.md`, `MEMORY.md`, and conversation state; expose repository inspection and modification as typed Codex-backed tools instead of keyword routing.

**Tech Stack:** Go 1.26, standard library HTTP/JSON/filesystem/process APIs, `gopkg.in/yaml.v3`, existing ports-and-adapters packages, Codex CLI, Telegram Bot API, file-backed YAML/Markdown/JSON state.

## Global Constraints

- Keep `internal/kernel` and `internal/ports` provider-neutral; they must not import Telegram, DeepSeek, Codex, GitHub, Google, YAML, JSON-file persistence, Docker, or Railway packages.
- Provider request/response types and credentials stay inside adapter packages; register adapters and tools only through `internal/bootstrap`.
- Retain path, environment, timeout, output, process-group, protected-branch, and independent commit/push/PR/Calendar approval restrictions.
- Use DeepSeek Pro for every default agent turn; do not retain flash-first escalation or silently fail over providers.
- Support only OpenAI-compatible chat-completions providers; add no framework, ORM, database, plugin runtime, multi-user routing, browsing, or subagents.
- Keep credentials in environment variables and exclude values from YAML, prompts, tool payloads, durable context, state, logs, and errors.
- Preserve v1 configuration through in-memory normalization and migrate `/data/state.json` explicitly from schema 1 to schema 2 without losing fields.
- Implement behavior test-first; run `make fmt vet test race build` before completion and `make smoke` when Docker is available.

---

### Task 1: Normalize Provider and Model Configuration

**Files:**
- Modify: `internal/bootstrap/config.go`
- Modify: `internal/bootstrap/config_test.go`
- Modify: `internal/bootstrap/config_init.go`
- Modify: `internal/bootstrap/config_init_test.go`
- Modify: `config.example.yaml`

**Interfaces:**
- Produces: `AgentConfig`, `ProviderConfig`, `ModelAliasConfig`, `Secrets.ProviderAPIKeys`, and `Config.ActiveModel`.
- Consumed by: bootstrap model registration and `/model` validation.
- Compatibility: retain the legacy runtime `ModelsConfig` and `Secrets.DeepSeekAPIKey` fields used by the current app until Task 7 switches bootstrap atomically; strict persisted v2 decoding uses a separate `configV2` document type.

- [ ] **Step 1: Write failing v1/v2 tests**

Test a complete v1 fixture and a v2 fixture containing:

```yaml
version: 2
agent: {default_model: deepseek-pro}
providers:
  deepseek:
    adapter: openai_compatible
    base_url: https://api.deepseek.com
    api_key_env: DEEPSEEK_API_KEY
models:
  deepseek-pro: {provider: deepseek, model: deepseek-v4-pro}
```

Assert v2 key lookup, strict unknown-field rejection, valid alias resolution, and failures for bad names, URLs, adapters, env names, provider references, default aliases, and missing credentials. Assert v1 maps its legacy Pro ID to `deepseek-pro`.

- [ ] **Step 2: Verify failure**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap -run 'TestLoadConfigVersion2|TestLoadConfigVersion1Compatibility|TestVersion2ProviderValidation' -count=1
```

Expected: FAIL because v2 aliases do not exist.

- [ ] **Step 3: Implement version-discriminated decoding**

Add these normalized types:

```go
type AgentConfig struct { DefaultModel string `yaml:"default_model"` }
type ProviderConfig struct {
	Adapter string `yaml:"adapter"`
	BaseURL string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
}
type ModelAliasConfig struct {
	Provider string `yaml:"provider"`
	Model string `yaml:"model"`
}
```

Read bytes, decode only `version`, then strict-decode `legacyConfigV1` or v2 `Config`. Normalize v1 to provider `deepseek`, base URL `https://api.deepseek.com`, env `DEEPSEEK_API_KEY`, default alias `deepseek-pro`, and its legacy Pro ID. Validate identifiers with `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` and env names with `^[A-Z][A-Z0-9_]{0,127}$`.

Keep current bootstrap compiling by normalizing through a runtime `Config` that still exposes the legacy Pro fields alongside new provider maps. Marshal first boot through the strict `configV2` document; Task 7 removes the temporary compatibility fields after all callers use aliases.

- [ ] **Step 4: Generate v2 on first boot**

Generate exactly one DeepSeek provider and `deepseek-pro` alias, keep secrets out of YAML, and update `config.example.yaml`.

- [ ] **Step 5: Verify and commit**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap -run 'TestLoadConfig|TestFirstBoot|TestVersion2' -count=1
git add internal/bootstrap/config.go internal/bootstrap/config_test.go internal/bootstrap/config_init.go internal/bootstrap/config_init_test.go config.example.yaml
git commit -m "feat: configure provider model aliases"
```

Expected: PASS.

---

### Task 2: Add OpenAI-Compatible Transport and Usage

**Files:**
- Modify: `internal/ports/ports.go`
- Create: `internal/adapters/models/openaicompat/model.go`
- Create: `internal/adapters/models/openaicompat/model_test.go`
- Keep temporarily: `internal/adapters/models/deepseek/model.go` and tests, until bootstrap switches in Task 7.

**Interfaces:**
- Produces: `ports.ModelUsage`, `ModelResponse.Usage`, and `openaicompat.New(baseURL, apiKey, client)`.

- [ ] **Step 1: Write failing adapter tests**

Assert POST to `<baseURL>/chat/completions`, bearer auth, provider-native model/tool translation, one attempt for auth errors, three for transport/408/429/5xx, no credential-bearing errors, and parsing prompt/completion/total/cached/reasoning tokens.

- [ ] **Step 2: Verify failure**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/adapters/models/openaicompat -count=1
```

Expected: FAIL because the package is absent.

- [ ] **Step 3: Add usage contract**

```go
type ModelUsage struct {
	PromptTokens int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens int64 `json:"total_tokens"`
	CachedPromptTokens int64 `json:"cached_prompt_tokens,omitempty"`
	ReasoningTokens int64 `json:"reasoning_tokens,omitempty"`
}
func (u ModelUsage) Add(v ModelUsage) ModelUsage
type ModelResponse struct {
	Message Message `json:"message"`
	Usage ModelUsage `json:"usage,omitempty"`
}
```

Implement `Add` as field-wise addition.

- [ ] **Step 4: Implement generic adapter**

Move private translation structs from the DeepSeek adapter, normalize `baseURL`, append `/chat/completions`, parse optional usage details, and classify safe errors without response bodies.

- [ ] **Step 5: Verify and commit the additive adapter**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/adapters/models/openaicompat -count=1
git add internal/ports/ports.go internal/adapters/models/openaicompat
git commit -m "feat: add OpenAI-compatible model adapter"
```

Expected: PASS; the existing app continues compiling with the old adapter until the atomic bootstrap switch in Task 7.

---

### Task 3: Migrate State and Persist Model Runtime

**Files:**
- Modify: `internal/ports/ports.go`
- Modify: `internal/adapters/state/jsonfile/store.go`
- Modify: `internal/adapters/state/jsonfile/store_test.go`
- Create: `internal/kernel/services/agent_runtime.go`
- Create: `internal/kernel/services/agent_runtime_test.go`

**Interfaces:**
- Produces: `ports.State.Agent`, `ports.ErrStateVersionConflict`, and selection/usage operations.

- [ ] **Step 1: Write failing migration tests**

Load a schema 1 fixture containing every legacy field. Assert persisted schema 2, exact legacy equality, empty runtime fields, future-schema rejection, and no replacement of malformed input.

- [ ] **Step 2: Verify failure**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/adapters/state/jsonfile -run 'TestMigratesSchemaOne|TestRejectsFutureSchema' -count=1
```

Expected: FAIL because only schema 1 is supported.

- [ ] **Step 3: Implement schema 2**

```go
var ErrStateVersionConflict = errors.New("state version conflict")
type AgentRuntimeState struct {
	SelectedModel string `json:"selected_model,omitempty"`
	Usage map[string]ModelUsage `json:"usage,omitempty"`
}
```

Add it to `ports.State`, migrate under the existing lock and atomic writer, preserve `Version`, and make conflicts return the port sentinel.

- [ ] **Step 4: Write and implement runtime service tests**

Test default/valid/invalid/reset selection, additive/reset usage, copied maps, and 16 concurrent updates. Implement:

```go
func NewAgentRuntime(store ports.StateStore, defaultAlias string, aliases []string) *AgentRuntime
func (r *AgentRuntime) SelectedModel(context.Context) (string, error)
func (r *AgentRuntime) SelectModel(context.Context, string) error
func (r *AgentRuntime) RecordUsage(context.Context, string, ports.ModelUsage) error
func (r *AgentRuntime) Usage(context.Context) (map[string]ports.ModelUsage, error)
func (r *AgentRuntime) ResetUsage(context.Context) error
```

Retry only version conflicts, reloading each time, for at most eight attempts.

- [ ] **Step 5: Verify and commit**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/adapters/state/jsonfile ./internal/kernel/services -run 'TestMigratesSchemaOne|TestAgentRuntime' -count=1
git add internal/ports/ports.go internal/adapters/state/jsonfile internal/kernel/services/agent_runtime.go internal/kernel/services/agent_runtime_test.go
git commit -m "feat: persist agent model usage"
```

Expected: PASS, including concurrency.

---

### Task 4: Add Layered Context and Steering

**Files:**
- Modify: `internal/ports/ports.go`
- Create: `internal/adapters/context/markdown/store.go`
- Create: `internal/adapters/context/markdown/store_test.go`
- Create: `internal/kernel/services/context.go`
- Create: `internal/kernel/services/context_test.go`
- Create: `internal/kernel/agent/prompt.go`
- Create: `internal/kernel/agent/prompt_test.go`
- Keep temporarily: `internal/adapters/memory/markdown/store.go` and tests, until bootstrap switches in Task 7.

**Interfaces:**
- Produces: `ports.AgentContext`, `ports.ContextStore`, curated tools, `SecretGuard`, and `BuildInstructions`.

- [ ] **Step 1: Write failing lifecycle tests**

Assert `Open(dir, 64<<10).Load` creates SOUL/USER/MEMORY with `0600`, preserves existing bytes, rejects oversized files, permits section edits only for USER/MEMORY, and remains valid under concurrent writes.

- [ ] **Step 2: Verify failure**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/adapters/context/markdown -count=1
```

Expected: FAIL because the package is absent.

- [ ] **Step 3: Implement context port and adapter**

```go
type AgentContext struct { Soul, User, Memory string }
type ContextDocument string
const ContextUser ContextDocument = "user"
const ContextMemory ContextDocument = "memory"
type ContextStore interface {
	Load(context.Context) (AgentContext, error)
	Append(context.Context, ContextDocument, string, string) error
	ReplaceSection(context.Context, ContextDocument, string, string) error
}
```

Seed missing files atomically, never overwrite existing files, bound reads, reuse file locks, and reject SOUL edits.

- [ ] **Step 4: Write secret and prompt tests**

Reject GitHub PATs, bearer tokens, password assignments, PEM private keys, credential section names, and exact active secrets; accept stable preferences. Assert prompt order hard policy → manifest → SOUL → USER → MEMORY and no secret values.

- [ ] **Step 5: Implement curated tools and prompt builder**

Register `user_append`, `user_replace_section`, `memory_append`, and `memory_replace_section` with strict `{section,content}` schemas. Add:

```go
type CapabilityManifest struct {
	ActiveModel string
	Repositories []string
	Tools []string
	CodexReady bool
	CalendarEnabled bool
}
func BuildInstructions(ctx ports.AgentContext, capability CapabilityManifest) []ports.Message
```

Compile the approved truthfulness, evidence, credential, memory, instruction-precedence, and protected-action policy. Sort manifest names and label USER/MEMORY as potentially stale.

- [ ] **Step 6: Verify and commit the additive context path**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/adapters/context/markdown ./internal/kernel/services ./internal/kernel/agent -run 'TestContext|TestSecretGuard|TestBuildInstructions' -count=1
git add internal/ports/ports.go internal/adapters/context/markdown internal/kernel/services/context.go internal/kernel/services/context_test.go internal/kernel/agent/prompt.go internal/kernel/agent/prompt_test.go
git commit -m "feat: add layered agent context"
```

Expected: PASS.

---

### Task 5: Unify the Agent Loop

**Files:**
- Modify: `internal/kernel/agent/loop.go`
- Modify: `internal/kernel/agent/loop_test.go`
- Remove: `internal/kernel/agent/router.go`

**Interfaces:**
- Produces: additive `RunSelected` alias execution, usage aggregation, and per-run tool filtering; the legacy `Run` and router remain until Task 7 switches bootstrap.

- [ ] **Step 1: Write failing tests**

Use:

```go
type ModelTarget struct { Model ports.Model; ModelID string }
type RunOptions struct { AllowedTools map[string]bool }
type RunResult struct { Message ports.Message; Usage ports.ModelUsage }
```

Test exact alias/model ID, usage summed across tool turns, unknown alias before execution, and mutation tools excluded by a heartbeat allowlist.

- [ ] **Step 2: Verify failure**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/kernel/agent -run 'TestLoopSelectsAlias|TestLoopAccumulatesUsage|TestLoopFiltersTools' -count=1
```

Expected: FAIL because the loop still escalates flash to Pro.

- [ ] **Step 3: Implement one-model execution**

```go
func (l *Loop) RunSelected(ctx context.Context, alias, input string, history []ports.Message, options RunOptions) (RunResult, error)
```

Resolve once, use one target for all steps, add every response's usage, filter definitions and execution, and return accumulated usage with later errors. Nil allowlist means all tools.

- [ ] **Step 4: Verify and commit the additive loop**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/kernel/agent -count=1
git add internal/kernel/agent/loop.go internal/kernel/agent/loop_test.go
git commit -m "refactor: unify agent model routing"
```

Expected: PASS; legacy entry points remain only to keep the pre-switch app compiling.

---

### Task 6: Add Read-Only Codex Inspection and Repository Tools

**Files:**
- Modify: `internal/ports/ports.go`
- Modify: `internal/adapters/coding/codexcli/codex.go`
- Modify: `internal/adapters/coding/codexcli/codex_test.go`
- Modify: `internal/kernel/services/coding.go`
- Modify: `internal/kernel/services/coding_test.go`
- Create: `internal/kernel/services/repository_tools.go`
- Create: `internal/kernel/services/repository_tools_test.go`

**Interfaces:**
- Produces: `CodingRequest.ReadOnly`, `CodingService.Inspect`, and list/inspect/modify tools.

- [ ] **Step 1: Write failing sandbox tests**

Assert read-only requests execute `codex exec --json --sandbox read-only` and modifying requests use `workspace-write`.

- [ ] **Step 2: Verify failure**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/adapters/coding/codexcli -run 'TestAdapterUsesReadOnlySandbox|TestAdapterUsesWorkspaceWriteSandbox' -count=1
```

Expected: FAIL because `ReadOnly` is absent.

- [ ] **Step 3: Add sandbox selection and inspection**

Add `ReadOnly bool` to `CodingRequest`. Select `read-only` or `workspace-write`. Implement:

```go
func (s *CodingService) Inspect(ctx context.Context, runID string, repository ports.Repository, question string) (ports.CodingResult, error)
```

Create/clone/destroy a temporary workspace, load AGENTS guidance, run Codex read-only with `CODEX_HOME`, create no branch/state run, and reject a non-empty diff.

- [ ] **Step 4: Implement repository tools test-first**

Create narrow `RepositoryInspector`, `RepositoryModifier`, and `CommitApprovalRequester` interfaces. Implement strict `repository_list`, `repository_inspect`, and `repository_modify`. Missing config returns `not_configured`; modification returns `awaiting_owner` only after commit approval creation. Progress and approval delivery use callbacks without importing Telegram.

- [ ] **Step 5: Verify and commit**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/adapters/coding/codexcli ./internal/kernel/services -run 'TestAdapterUses|TestCodingServiceInspect|TestRepositoryTool' -count=1
git add internal/ports/ports.go internal/adapters/coding/codexcli internal/kernel/services/coding.go internal/kernel/services/coding_test.go internal/kernel/services/repository_tools.go internal/kernel/services/repository_tools_test.go
git commit -m "feat: delegate repository tools to Codex"
```

Expected: PASS.

---

### Task 7: Wire Bootstrap, Commands, and Acceptance Behavior

**Files:**
- Modify: `internal/bootstrap/app.go`
- Modify: `internal/bootstrap/app_test.go`
- Modify: `internal/bootstrap/assistant_tools.go`
- Modify: `internal/bootstrap/commands.go`
- Create: `internal/bootstrap/commands_test.go`

**Interfaces:**
- Consumes: Tasks 1–6.
- Produces: unified owner-message routing, `/model`, `/usage`, and transcript regression tests.

- [ ] **Step 1: Write failing command tests**

Assert `/model`, `/model deepseek-pro`, `/model default`, invalid aliases, `/usage`, and `/usage reset`. `/status` must not consume usage and `/new` must retain the selected alias. Usage output ends: `Local totals are provider-reported and do not replace the provider billing dashboard.`

- [ ] **Step 2: Verify failure**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap -run 'TestCommandModel|TestCommandUsage' -count=1
```

Expected: FAIL because commands are absent.

- [ ] **Step 3: Register unified dependencies**

Open schema 2 state and layered context. Create one `openaicompat.Model` per provider, resolve aliases to `agent.ModelTarget`, construct `AgentRuntime`, build `SecretGuard` from non-empty secrets, register context/repository/schedule/status/optional Calendar tools, then construct the unified loop and command service. Replace `AppOptions.DeepSeekEndpoint` with test-only `ProviderBaseURLs map[string]string`.

Update `App.Ready` to load both state and the complete layered context without making a provider request. Emit one safe startup log containing only active alias, provider name, sorted repository names, enabled integration names, and context filenames; test that keys, authorization values, and environment contents never appear.

- [ ] **Step 4: Replace `handleMessage` routing**

After commands: load context/state, select alias, build instructions plus conversation, call the loop, persist returned usage even with a later error, record successful conversation messages, and deliver the final response. Delete `App.router`, coding keywords, and `forcePro`. Heartbeat uses the selected alias with a read-only tool allowlist and records usage.

- [ ] **Step 5: Preserve modifying approvals**

Repository callbacks deliver progress and the commit approval. Keep commit/push/PR/Calendar execution solely in existing approval handling; do not add bypasses.

- [ ] **Step 6: Remove compatibility paths after the switch compiles**

Delete the legacy DeepSeek adapter, memory-only adapter, router, flash/Pro loop entry point, and temporary legacy bootstrap config/secret fields only after `app.go` builds entirely on provider aliases, layered context, and `RunSelected`:

```sh
git rm internal/adapters/models/deepseek/model.go internal/adapters/models/deepseek/model_test.go
git rm internal/adapters/memory/markdown/store.go internal/adapters/memory/markdown/store_test.go
git rm internal/kernel/agent/router.go
```

- [ ] **Step 7: Add defect transcript acceptance test**

Script tool calls so repository questions use runtime list/inspection, repository claims are not saved as configuration, GitHub credentials are described as externally managed and never solicited, stable memory writes work, secret writes fail, inspection is clean, and usage increases. Assert no credential appears in provider request bodies.

- [ ] **Step 8: Verify and commit**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap ./internal/kernel/... ./internal/adapters/... -count=1
git add internal/bootstrap internal/adapters/models internal/kernel/agent internal/kernel/services
git commit -m "feat: unify Telegram agent runtime"
```

Expected: PASS and no old DeepSeek/memory imports.

---

### Task 8: Documentation, Smoke, and Final Verification

**Files:**
- Modify: `.env.example`
- Modify: `README.md`
- Modify: `config.example.yaml`
- Modify: `scripts/docker-smoke.sh`
- Modify only exact code files required by verified failures.

**Interfaces:**
- Documents and verifies the complete runtime.

- [ ] **Step 1: Update operations docs**

Document v2 aliases, env-backed keys, DeepSeek Pro default, `/model`, `/usage`, local-versus-provider totals, SOUL/USER/MEMORY roles, no secrets in Telegram, repository setup, webhook, `/data`, `GITHUB_TOKEN`, and Codex device auth. Explain that `/status` is local and consumes no model tokens.

- [ ] **Step 2: Extend smoke**

Assert v2 config and `0600` config/context files, health/readiness, and absence of `DEEPSEEK_API_KEY` values from persisted YAML/Markdown.

- [ ] **Step 3: Run syntax and Docker checks**

```sh
git diff --check
sh -n scripts/docker-smoke.sh
make smoke
```

Expected: PASS. If Docker is unavailable, record the exact daemon error.

- [ ] **Step 4: Run required verification**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod make fmt vet test race build
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap -run TestUnifiedAgentDefectTranscript -count=1 -v
git diff --check
```

Expected: all pass.

- [ ] **Step 5: Commit docs and any verified correction**

```sh
git add .env.example README.md config.example.yaml scripts/docker-smoke.sh
git commit -m "docs: configure unified agent runtime"
git status --short
git log --oneline --decorate -12
```

Expected: clean feature branch. Do not create an empty correction commit.
