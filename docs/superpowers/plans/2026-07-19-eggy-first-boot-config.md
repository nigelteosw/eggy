# Eggy First-Boot Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `eggyd` start on an empty Railway Volume by generating and persisting a safe `/data/config.yaml` from a small set of non-secret Railway variables.

**Architecture:** Keep YAML loading and first-boot generation in `internal/bootstrap`, with the existing config file remaining canonical. A new `LoadOrCreateConfig` entry point serializes initialization with the existing Unix file lock, validates before an atomic mode-`0600` write, then delegates to the strict loader; the strict loader also applies a validated, non-persisted Railway `PORT` override.

**Tech Stack:** Go 1.26, standard library, `gopkg.in/yaml.v3`, existing `internal/adapters/filelock`, POSIX Docker image, Railway service variables and Volume.

## Global Constraints

- Keep `internal/kernel` and `internal/ports` provider-neutral; first-boot and Railway environment handling stay in `internal/bootstrap`.
- Do not write secrets into `config.yaml`, state, logs, or errors.
- Never overwrite an existing config file, including an existing invalid file.
- Preserve `/data/state.json` schema compatibility; this feature does not modify state persistence.
- Use safe defaults from `docs/superpowers/specs/2026-07-19-eggy-first-boot-config-design.md` exactly.
- Add behavior test-first and run the focused test before the full suite.
- Run `GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod make fmt vet test race build` before completion; run `make smoke` only when Docker is available.

---

### Task 1: Generate and persist canonical first-boot configuration

**Files:**
- Create: `internal/bootstrap/config_init.go`
- Create: `internal/bootstrap/config_init_test.go`
- Modify: `internal/bootstrap/config.go`

**Interfaces:**
- Consumes: `filelock.With(path string, operation func() error) error`, existing `Config.Validate`, existing `LoadConfig`.
- Produces: `LoadOrCreateConfig(path string, getenv func(string) string) (Config, Secrets, error)` for both binaries.
- Produces: `Duration.MarshalYAML() (any, error)` so generated durations use strings such as `"45m0s"`, not nanoseconds.

- [ ] **Step 1: Write failing tests for safe default generation**

Create `internal/bootstrap/config_init_test.go` with a helper that supplies the three always-required provider secrets plus:

```go
func firstBootEnv() map[string]string {
	values := testSecrets()
	values["EGGY_TELEGRAM_OWNER_ID"] = "42"
	values["EGGY_PUBLIC_BASE_URL"] = "https://eggy.up.railway.app"
	return values
}
```

Add `TestLoadOrCreateConfigGeneratesSafeDefaults` that calls `LoadOrCreateConfig(filepath.Join(t.TempDir(), "config.yaml"), mapEnv(env))` and asserts:

```go
if cfg.Version != 1 || cfg.Telegram.OwnerID != 42 || cfg.Server.PublicBaseURL != "https://eggy.up.railway.app" {
	t.Fatalf("generated config = %#v", cfg)
}
if cfg.DataDir != "/data" || cfg.Server.TelegramWebhookPath != "/webhooks/telegram" || cfg.Calendar.Enabled || len(cfg.Repositories) != 0 {
	t.Fatalf("unsafe generated defaults = %#v", cfg)
}
if cfg.Models.Flash != (ModelConfig{Adapter: "deepseek", ID: "deepseek-v4-flash"}) || cfg.Models.Pro != (ModelConfig{Adapter: "deepseek", ID: "deepseek-v4-pro"}) {
	t.Fatalf("generated models = %#v", cfg.Models)
}
```

Read the file and assert its mode is `0600`, it parses again through `LoadConfig`, duration fields appear as YAML strings, and none of the values from `testSecrets()` occur in the bytes.

- [ ] **Step 2: Run the default-generation test and verify RED**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap -run '^TestLoadOrCreateConfigGeneratesSafeDefaults$' -count=1
```

Expected: build failure because `LoadOrCreateConfig` does not exist.

- [ ] **Step 3: Add failing tests for environment derivation and validation**

Add table-driven tests covering:

```go
tests := []struct {
	name   string
	mutate func(map[string]string)
	want   string
}{
	{"missing owner", func(v map[string]string) { delete(v, "EGGY_TELEGRAM_OWNER_ID") }, "EGGY_TELEGRAM_OWNER_ID is required"},
	{"invalid owner", func(v map[string]string) { v["EGGY_TELEGRAM_OWNER_ID"] = "not-a-number" }, "EGGY_TELEGRAM_OWNER_ID must be a positive integer"},
	{"missing public URL", func(v map[string]string) { delete(v, "EGGY_PUBLIC_BASE_URL") }, "EGGY_PUBLIC_BASE_URL is required when RAILWAY_PUBLIC_DOMAIN is unavailable"},
	{"invalid public URL", func(v map[string]string) { v["EGGY_PUBLIC_BASE_URL"] = "ftp://invalid" }, "server.public_base_url"},
}
```

For every case assert the returned error contains `want` and `os.Stat(path)` reports `os.ErrNotExist`.

Add `TestLoadOrCreateConfigUsesRailwayDomain` with no `EGGY_PUBLIC_BASE_URL` and `RAILWAY_PUBLIC_DOMAIN=eggy-production.up.railway.app`; expect `https://eggy-production.up.railway.app`.

Add `TestLoadOrCreateConfigAddsOptionalRepository` with URL, name, base branch, and comma-separated protected branches; expect one validated `RepositoryConfig`. Add a second assertion that only the URL is required and the other repository fields default to `eggy`, `main`, and `[main]`.

- [ ] **Step 4: Implement minimal first-boot config construction**

Create `internal/bootstrap/config_init.go` with:

```go
func LoadOrCreateConfig(path string, getenv func(string) string) (Config, Secrets, error) {
	if _, err := os.Stat(path); err == nil {
		return LoadConfig(path, getenv)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, Secrets{}, fmt.Errorf("stat config: %w", err)
	}
	if err := initializeConfig(path, getenv); err != nil {
		return Config{}, Secrets{}, err
	}
	return LoadConfig(path, getenv)
}
```

Implement `firstBootConfig(getenv func(string) string) (Config, error)` by parsing `EGGY_TELEGRAM_OWNER_ID`, deriving the public URL, and constructing the exact defaults from the design. Build the repository slice only when `EGGY_REPOSITORY_URL` is non-empty. Parse protected branches by splitting on commas, trimming whitespace, rejecting an explicitly supplied list that contains no branch, and defaulting to the base branch when the variable is absent.

Add this method to `internal/bootstrap/config.go`:

```go
func (d Duration) MarshalYAML() (any, error) {
	return d.Value().String(), nil
}
```

Call `cfg.Validate()` before returning the candidate so invalid environment input cannot reach persistence.

- [ ] **Step 5: Implement locked atomic persistence**

Implement `initializeConfig` with `filelock.With(path, func() error { ... })`. Inside the lock:

1. Recheck `path`; return without writing if it now exists.
2. Build and validate the candidate.
3. Marshal with `yaml.Marshal`.
4. Create a same-directory temporary file with `os.CreateTemp(filepath.Dir(path), ".config-*.tmp")`.
5. Set mode `0600`, write all bytes, call `Sync`, and close it.
6. Recheck that the destination still does not exist.
7. Rename the temporary file to `path`.
8. Defer cleanup of the temporary name on every error path.

Wrap errors with stable operation names such as `generate config`, `marshal generated config`, and `persist generated config`; never include environment values.

- [ ] **Step 6: Run focused generation tests and verify GREEN**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap -run '^(TestLoadOrCreateConfig|TestDuration)' -count=1
```

Expected: PASS.

- [ ] **Step 7: Add overwrite and concurrency regression tests**

Add `TestLoadOrCreateConfigNeverOverwritesExistingFile`: write `validConfig()` to the path, pass invalid first-boot variables, call `LoadOrCreateConfig`, and assert byte-for-byte equality before and after.

Add a malformed existing-file subtest that writes `invalid: yaml: [` and asserts an error while preserving the exact bytes.

Add `TestLoadOrCreateConfigSerializesConcurrentInitialization`: start eight goroutines behind a closed start channel, call `LoadOrCreateConfig` on the same missing path, collect errors, then assert every call succeeded and the final file strictly reloads.

- [ ] **Step 8: Run bootstrap tests and commit Task 1**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/bootstrap/config.go internal/bootstrap/config_init.go internal/bootstrap/config_init_test.go
git commit -m "feat: generate first-boot config"
```

---

### Task 2: Apply validated runtime port and wire both binaries

**Files:**
- Modify: `internal/bootstrap/config.go`
- Modify: `internal/bootstrap/config_test.go`
- Modify: `cmd/eggyd/main.go`
- Modify: `cmd/eggy/main.go`

**Interfaces:**
- Consumes: `LoadOrCreateConfig(path, getenv)` from Task 1.
- Produces: `applyRuntimeOverrides(*Config, getenv func(string) string) error`, called by `LoadConfig` after YAML defaults and before validation.

- [ ] **Step 1: Write failing runtime-port tests**

Add `TestLoadConfigUsesValidatedRuntimePort` with these cases:

```go
tests := []struct {
	port       string
	wantListen string
	wantError  string
}{
	{"4317", ":4317", ""},
	{"", ":8080", ""},
	{"0", "", "PORT must be an integer between 1 and 65535"},
	{"65536", "", "PORT must be an integer between 1 and 65535"},
	{"http", "", "PORT must be an integer between 1 and 65535"},
}
```

Load `validConfig()` with each environment, assert the in-memory listen address or the exact error fragment, and reread the YAML bytes to prove `PORT` was not persisted.

- [ ] **Step 2: Run runtime-port test and verify RED**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap -run '^TestLoadConfigUsesValidatedRuntimePort$' -count=1
```

Expected: FAIL because `PORT=4317` still yields `:8080`.

- [ ] **Step 3: Implement the runtime override**

In `internal/bootstrap/config.go`, after `cfg.applyDefaults()` and before `cfg.Validate()`, call:

```go
if err := applyRuntimeOverrides(&cfg, getenv); err != nil {
	return cfg, Secrets{}, err
}
```

Implement:

```go
func applyRuntimeOverrides(cfg *Config, getenv func(string) string) error {
	raw := strings.TrimSpace(getenv("PORT"))
	if raw == "" {
		return nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("PORT must be an integer between 1 and 65535")
	}
	cfg.Server.Listen = ":" + strconv.Itoa(port)
	return nil
}
```

- [ ] **Step 4: Wire daemon and CLI through first-boot loading**

Replace both calls to `bootstrap.LoadConfig(*configPath, getenv)` in `cmd/eggyd/main.go` and `cmd/eggy/main.go` with `bootstrap.LoadOrCreateConfig(*configPath, getenv)`.

This ensures the operational CLI and daemon agree on config initialization and avoids a second first-boot path.

- [ ] **Step 5: Run focused tests and commit Task 2**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap ./cmd/eggy ./cmd/eggyd -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/bootstrap/config.go internal/bootstrap/config_test.go cmd/eggyd/main.go cmd/eggy/main.go
git commit -m "fix: honor Railway runtime configuration"
```

---

### Task 3: Exercise empty-volume deployment and document Railway setup

**Files:**
- Modify: `scripts/docker-smoke.sh`
- Modify: `README.md`
- Modify: `.env.example`

**Interfaces:**
- Consumes: first-boot variables defined in the design and `LoadOrCreateConfig` from Task 1.
- Produces: a smoke test that starts from an empty `/data` mount and proves config persistence plus HTTP health.

- [ ] **Step 1: Change smoke setup to reproduce a fresh Railway Volume**

Remove:

```sh
cp config.example.yaml "$data_dir/config.yaml"
```

Add these container variables:

```sh
--env PORT=8080 \
--env EGGY_TELEGRAM_OWNER_ID=42 \
--env EGGY_PUBLIC_BASE_URL=https://eggy-smoke.example \
```

Remove GitHub and Google variables from the smoke command because generated first-boot config has no repository and Calendar is disabled. Keep the three always-required fake secrets.

After readiness succeeds, assert:

```sh
docker exec "$container" test -s /data/config.yaml
docker exec "$container" sh -c 'test "$(stat -c %a /data/config.yaml)" = 600'
```

- [ ] **Step 2: Document the exact Railway variable contract**

Add these non-secret names to `.env.example` with empty values:

```dotenv
EGGY_TELEGRAM_OWNER_ID=
EGGY_PUBLIC_BASE_URL=
EGGY_REPOSITORY_URL=
EGGY_REPOSITORY_NAME=
EGGY_REPOSITORY_BASE_BRANCH=
EGGY_REPOSITORY_PROTECTED_BRANCHES=
```

Rewrite the Railway steps in `README.md` so they say:

1. Mount a persistent Railway Volume at `/data`.
2. Set `EGGY_TELEGRAM_OWNER_ID`.
3. Let `EGGY_PUBLIC_BASE_URL` default from `RAILWAY_PUBLIC_DOMAIN`, or set it explicitly for a custom domain.
4. Set repository variables only when coding support should be enabled; this makes `GITHUB_TOKEN` required.
5. Set the three always-required provider secrets.
6. Deploy; Eggy creates `/data/config.yaml` once and never overwrites it.
7. Explain that Calendar defaults off and must be enabled later by editing the persisted YAML and adding Google variables.

State explicitly that `EGGY_CONFIG_YAML` is not supported and that `PORT` is supplied by Railway.

- [ ] **Step 3: Run static deployment checks**

Run:

```bash
sh -n scripts/docker-smoke.sh
git diff --check
rg -n 'EGGY_TELEGRAM_OWNER_ID|RAILWAY_PUBLIC_DOMAIN|creates /data/config.yaml' README.md .env.example
```

Expected: shell syntax and diff checks exit zero; documentation contains the new first-boot contract.

- [ ] **Step 4: Run the complete verification matrix**

Run:

```bash
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod make fmt vet test race build
```

Expected: every target exits zero.

Check Docker:

```bash
docker info --format '{{.ServerVersion}}'
```

If it succeeds, run `make smoke` and expect `Eggy Docker smoke test passed`. If Docker is unavailable, record the daemon error as an environment-only verification gap without claiming smoke passed.

- [ ] **Step 5: Review the final diff against the design and commit Task 3**

Run:

```bash
git diff --check
git status --short
git diff -- README.md .env.example scripts/docker-smoke.sh
```

Confirm no kernel/port boundary imports, no provider secret values in generated YAML or docs, no `state.json` changes, and no unrelated worktree edits.

Commit:

```bash
git add README.md .env.example scripts/docker-smoke.sh
git commit -m "docs: simplify Railway first boot"
```
