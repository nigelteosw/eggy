# Web Config UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Eggy's owner view and edit providers, models, and calendar settings through a small React + Tailwind web UI, embedded in the existing Go binary and gated by a single owner login.

**Architecture:** A new `internal/adapters/webui` package owns the embedded static frontend build and two generic, Eggy-agnostic primitives (signed session cookies, login-attempt throttling). A new `internal/bootstrap/web.go` wires those into HTTP handlers that (a) serve the static app, (b) handle login/logout/session-check, and (c) translate `/api/config/*` requests into the exact same `CommandRequest`/`CommandResult` shapes Telegram and the CLI already use, so there is exactly one place config validation and mutation logic lives. The frontend (`web/`, Vite + React + TypeScript + Tailwind) is built at `make build-web` time into `internal/adapters/webui/dist`, which is embedded via `//go:embed` — the deployed binary stays a single process, single Railway service.

**Tech Stack:** Go 1.26 (stdlib `net/http`, `crypto/hmac`, `crypto/subtle`), Vite + React 18 + TypeScript + Tailwind CSS (no component library, no client-side router).

## Global Constraints

- Full design spec: `docs/superpowers/specs/2026-07-22-web-config-ui-design.md`. Every task below implements one section of it.
- Do not change `CommandService`, the command catalog, or the meaning of `CommandResult`'s existing fields — only add a `RenderJSON` method and JSON tags to `CommandResult`/`ResultField`.
- Do not add a database, session store, or new encryption key — session cookies are signed with the existing `EGGY_ENCRYPTION_KEY`.
- Do not implement repository or MCP-server management through the web UI in this iteration.
- The two new env vars (`EGGY_UI_USER_EMAIL`, `EGGY_UI_PASSWORD`) must NOT be unconditionally required — existing deployments that don't set them must keep booting exactly as before; only require them (and `EGGY_ENCRYPTION_KEY`) when the owner has started configuring the web UI (i.e., set at least one of the two).
- Frontend has no automated test framework in this iteration — verify with `tsc`/`npm run build` and manual checks.
- Run `go build ./... && go vet ./... && go test ./...` after every backend task; run `make fmt vet test race build` before the plan is considered done.

---

### Task 1: Web UI login credentials as conditionally-required secrets

**Files:**
- Modify: `internal/bootstrap/config.go:136-146` (`Secrets` struct), `internal/bootstrap/config.go:167-204` (`LoadConfig`), `internal/bootstrap/config.go:503-533` (`validateSecrets`)
- Modify: `.env.example`
- Test: `internal/bootstrap/config_test.go`

**Interfaces:**
- Produces: `Secrets.UIUserEmail string`, `Secrets.UIPassword string` — read by Task 9 (`app.go`) to build `bootstrap.WebUIConfig`.

- [ ] **Step 1: Write the failing test**

`internal/bootstrap/config_test.go` already has everything this needs: `validConfig() string` (a minimal valid YAML document), `testSecrets() map[string]string` (a base set of required env vars), and `loadText(t, body, env) (Config, Secrets, error)` (writes `body` to a temp file and calls `LoadConfig`). Add a new test function using them directly — no new helper needed:

```go
func TestLoadConfigResolvesWebUICredentialsAndRequiresEncryptionKeyWhenSet(t *testing.T) {
	// validConfig() enables Calendar by default, which already requires
	// EGGY_ENCRYPTION_KEY for an unrelated reason. Disable it here so this
	// test isolates the new web UI credential check specifically.
	body := strings.Replace(validConfig(), "enabled: true\n  default_calendar", "enabled: false\n  default_calendar", 1)
	env := testSecrets()
	delete(env, "EGGY_ENCRYPTION_KEY")

	if _, secrets, err := loadText(t, body, env); err != nil {
		t.Fatalf("unconfigured web UI must not block boot: %v", err)
	} else if secrets.UIUserEmail != "" || secrets.UIPassword != "" {
		t.Fatalf("expected empty web UI credentials, got %#v", secrets)
	}

	env["EGGY_UI_USER_EMAIL"] = "owner@example.com"
	env["EGGY_UI_PASSWORD"] = "hunter2"
	if _, _, err := loadText(t, body, env); err == nil {
		t.Fatal("expected error: web UI configured without EGGY_ENCRYPTION_KEY")
	}

	env["EGGY_ENCRYPTION_KEY"] = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	_, secrets, err := loadText(t, body, env)
	if err != nil {
		t.Fatalf("fully configured web UI must load: %v", err)
	}
	if secrets.UIUserEmail != "owner@example.com" || secrets.UIPassword != "hunter2" {
		t.Fatalf("secrets=%#v", secrets)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap/... -run TestLoadConfigResolvesWebUICredentialsAndRequiresEncryptionKeyWhenSet -v`
Expected: FAIL — `secrets.UIUserEmail` doesn't exist (compile error) until Step 3.

- [ ] **Step 3: Implement**

In `internal/bootstrap/config.go`, add two fields to `Secrets` (near `MCPBearerTokens`):

```go
type Secrets struct {
	TelegramBotToken      string
	TelegramWebhookSecret string
	ProviderAPIKeys       map[string]string
	GitHubToken           string
	GoogleClientID        string
	GoogleClientSecret    string
	EncryptionKey         string
	MCPBearerTokens       map[string]string
	UIUserEmail           string
	UIPassword            string
}
```

In `LoadConfig`, add to the `secrets := Secrets{...}` literal:

```go
	secrets := Secrets{
		TelegramBotToken: getenv("TELEGRAM_BOT_TOKEN"), TelegramWebhookSecret: getenv("TELEGRAM_WEBHOOK_SECRET"),
		GitHubToken:    getenv("GITHUB_TOKEN"),
		GoogleClientID: getenv("GOOGLE_CLIENT_ID"), GoogleClientSecret: getenv("GOOGLE_CLIENT_SECRET"),
		EncryptionKey:   getenv("EGGY_ENCRYPTION_KEY"),
		UIUserEmail:     getenv("EGGY_UI_USER_EMAIL"),
		UIPassword:      getenv("EGGY_UI_PASSWORD"),
		ProviderAPIKeys: map[string]string{},
		MCPBearerTokens: map[string]string{},
	}
```

In `validateSecrets`, after the existing `MCP.Servers` loop and before the final `for _, item := range required` loop, add:

```go
	if strings.TrimSpace(s.UIUserEmail) != "" || strings.TrimSpace(s.UIPassword) != "" {
		required = append(required,
			struct{ name, value string }{"EGGY_UI_USER_EMAIL", s.UIUserEmail},
			struct{ name, value string }{"EGGY_UI_PASSWORD", s.UIPassword},
			struct{ name, value string }{"EGGY_ENCRYPTION_KEY", s.EncryptionKey})
	}
```

In `.env.example`, after the `EGGY_ENCRYPTION_KEY` line, add:

```
# Optional: enables the embedded web config UI at Eggy's public URL. Both
# must be set together; EGGY_ENCRYPTION_KEY (above) is then required too.
EGGY_UI_USER_EMAIL=
EGGY_UI_PASSWORD=
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bootstrap/... -run TestLoadConfigResolvesWebUICredentialsAndRequiresEncryptionKeyWhenSet -v`
Expected: PASS

- [ ] **Step 5: Run full package tests, then commit**

Run: `go test ./internal/bootstrap/...`
Expected: all pass (no regression to existing `validateSecrets`/`LoadConfig` tests).

```bash
git add internal/bootstrap/config.go internal/bootstrap/config_test.go .env.example
git commit -m "Add conditionally-required web UI login credentials"
```

---

### Task 2: Signed session tokens (`internal/adapters/webui`)

**Files:**
- Create: `internal/adapters/webui/cookie.go`
- Test: `internal/adapters/webui/cookie_test.go`

**Interfaces:**
- Produces: `webui.SignSession(key []byte, expiresAt time.Time) string`, `webui.VerifySession(key []byte, token string, now time.Time) bool` — consumed by Task 6 (`bootstrap/web.go`).

- [ ] **Step 1: Write the failing test**

```go
package webui

import (
	"testing"
	"time"
)

func TestSignSessionRoundTrips(t *testing.T) {
	key := []byte("test-key")
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	token := SignSession(key, now.Add(time.Hour))
	if !VerifySession(key, token, now) {
		t.Fatal("expected valid, unexpired token to verify")
	}
}

func TestVerifySessionRejectsExpiredToken(t *testing.T) {
	key := []byte("test-key")
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	token := SignSession(key, now.Add(-time.Second))
	if VerifySession(key, token, now) {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestVerifySessionRejectsTamperedToken(t *testing.T) {
	key := []byte("test-key")
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	token := SignSession(key, now.Add(time.Hour))
	tampered := token[:len(token)-1] + "0"
	if VerifySession(key, tampered, now) {
		t.Fatal("expected tampered signature to be rejected")
	}
}

func TestVerifySessionRejectsWrongKey(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	token := SignSession([]byte("key-a"), now.Add(time.Hour))
	if VerifySession([]byte("key-b"), token, now) {
		t.Fatal("expected token signed with a different key to be rejected")
	}
}

func TestVerifySessionRejectsMalformedToken(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, token := range []string{"", "no-dot-here", "not-a-number.deadbeef", "123.not-hex"} {
		if VerifySession([]byte("key"), token, now) {
			t.Fatalf("expected malformed token %q to be rejected", token)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/webui/... -v`
Expected: FAIL — package `webui` / functions don't exist yet.

- [ ] **Step 3: Implement**

```go
// Package webui embeds Eggy's built web configuration UI and provides the
// small, Eggy-agnostic primitives its login sits on: signed session tokens
// and login-attempt throttling. It has no knowledge of CommandService, config
// sections, or any other Eggy-specific type.
package webui

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// SignSession returns an HMAC-SHA256-signed session token encoding
// expiresAt, verifiable later with only key -- no server-side session store.
// The token carries no other payload: Eggy's web UI has exactly one owner
// account, so there is nothing else to encode.
func SignSession(key []byte, expiresAt time.Time) string {
	payload := strconv.FormatInt(expiresAt.Unix(), 10)
	return payload + "." + hex.EncodeToString(sign(key, payload))
}

// VerifySession reports whether token was produced by SignSession with key
// and has not yet expired as of now.
func VerifySession(key []byte, token string, now time.Time) bool {
	payload, sigHex, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	if !hmac.Equal(sig, sign(key, payload)) {
		return false
	}
	expiresAtUnix, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return false
	}
	return now.Before(time.Unix(expiresAtUnix, 0))
}

func sign(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/webui/... -v`
Expected: PASS (all 5 test functions)

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/webui/cookie.go internal/adapters/webui/cookie_test.go
git commit -m "Add signed session tokens for the web UI"
```

---

### Task 3: Login-attempt throttle (`internal/adapters/webui`)

**Files:**
- Create: `internal/adapters/webui/throttle.go`
- Test: `internal/adapters/webui/throttle_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `webui.NewLoginThrottle(now func() time.Time) *LoginThrottle`, `(*LoginThrottle) Delay(key string) time.Duration`, `(*LoginThrottle) RecordFailure(key string)`, `(*LoginThrottle) Reset(key string)` — consumed by Task 6.

- [ ] **Step 1: Write the failing test**

```go
package webui

import (
	"testing"
	"time"
)

func TestLoginThrottleDelaysAfterThreshold(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	throttle := NewLoginThrottle(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		if delay := throttle.Delay("1.2.3.4"); delay != 0 {
			t.Fatalf("attempt %d: expected no delay yet, got %v", i, delay)
		}
		throttle.RecordFailure("1.2.3.4")
	}
	if delay := throttle.Delay("1.2.3.4"); delay != 2*time.Second {
		t.Fatalf("expected 2s delay after 5 failures, got %v", delay)
	}
}

func TestLoginThrottleIsKeyedIndependently(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	throttle := NewLoginThrottle(func() time.Time { return now })
	for i := 0; i < 5; i++ {
		throttle.RecordFailure("1.2.3.4")
	}
	if delay := throttle.Delay("5.6.7.8"); delay != 0 {
		t.Fatalf("expected a different key to be unaffected, got %v", delay)
	}
}

func TestLoginThrottleResetsOnSuccess(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	throttle := NewLoginThrottle(func() time.Time { return now })
	for i := 0; i < 5; i++ {
		throttle.RecordFailure("1.2.3.4")
	}
	throttle.Reset("1.2.3.4")
	if delay := throttle.Delay("1.2.3.4"); delay != 0 {
		t.Fatalf("expected reset to clear the delay, got %v", delay)
	}
}

func TestLoginThrottleWindowExpires(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	throttle := NewLoginThrottle(func() time.Time { return now })
	for i := 0; i < 5; i++ {
		throttle.RecordFailure("1.2.3.4")
	}
	if delay := throttle.Delay("1.2.3.4"); delay != 2*time.Second {
		t.Fatalf("expected delay before window expiry, got %v", delay)
	}
	now = now.Add(16 * time.Minute)
	if delay := throttle.Delay("1.2.3.4"); delay != 0 {
		t.Fatalf("expected delay to clear after the window expires, got %v", delay)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/webui/... -run TestLoginThrottle -v`
Expected: FAIL — `LoginThrottle` doesn't exist yet.

- [ ] **Step 3: Implement**

```go
package webui

import (
	"sync"
	"time"
)

const (
	throttleWindow    = 15 * time.Minute
	throttleThreshold = 5
	throttleDelay     = 2 * time.Second
)

// LoginThrottle delays repeated failed login attempts from the same key
// (typically a client IP) so casual password guessing costs real time,
// without a persistent lockout store -- state resets on process restart,
// which is acceptable for Eggy's single-owner threat model.
type LoginThrottle struct {
	mu       sync.Mutex
	now      func() time.Time
	attempts map[string]*attemptState
}

type attemptState struct {
	failures    int
	windowStart time.Time
}

func NewLoginThrottle(now func() time.Time) *LoginThrottle {
	if now == nil {
		now = time.Now
	}
	return &LoginThrottle{now: now, attempts: map[string]*attemptState{}}
}

// Delay returns how long the caller should wait before processing a login
// attempt from key.
func (t *LoginThrottle) Delay(key string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stateLocked(key).failures >= throttleThreshold {
		return throttleDelay
	}
	return 0
}

// RecordFailure counts one more failed attempt from key.
func (t *LoginThrottle) RecordFailure(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stateLocked(key).failures++
}

// Reset clears key's failure count, e.g. after a successful login.
func (t *LoginThrottle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.attempts, key)
}

func (t *LoginThrottle) stateLocked(key string) *attemptState {
	state, ok := t.attempts[key]
	if !ok || t.now().Sub(state.windowStart) > throttleWindow {
		state = &attemptState{windowStart: t.now()}
		t.attempts[key] = state
	}
	return state
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/webui/... -v`
Expected: PASS (all tests in the package, including Task 2's)

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/webui/throttle.go internal/adapters/webui/throttle_test.go
git commit -m "Add login-attempt throttle for the web UI"
```

---

### Task 4: Embedded static assets with a build placeholder

**Files:**
- Create: `internal/adapters/webui/webui.go`
- Create: `internal/adapters/webui/dist/index.html` (committed placeholder, overwritten by `make build-web`)
- Test: `internal/adapters/webui/webui_test.go`
- Modify: `.gitignore`

**Interfaces:**
- Produces: `webui.Assets() fs.FS` — consumed by Task 6 (`bootstrap/web.go`, `http.FileServer(http.FS(webui.Assets()))`).

- [ ] **Step 1: Write the placeholder file first (embed needs something to embed)**

Create `internal/adapters/webui/dist/index.html`:

```html
<!doctype html>
<html>
  <head><meta charset="utf-8"><title>Eggy</title></head>
  <body>
    <p>This is a placeholder. Run <code>make build-web</code> to build the real web config UI.</p>
  </body>
</html>
```

- [ ] **Step 2: Write the failing test**

```go
package webui

import (
	"io/fs"
	"testing"
)

func TestAssetsServesThePlaceholderOrRealBuild(t *testing.T) {
	assets := Assets()
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("expected index.html to be embedded: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty index.html")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/adapters/webui/... -run TestAssets -v`
Expected: FAIL — `Assets` doesn't exist yet.

- [ ] **Step 4: Implement**

```go
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Assets returns the embedded web UI build, rooted so paths like
// "index.html" and "assets/app.js" resolve directly (stripping the "dist/"
// prefix embed.FS otherwise keeps). Until `make build-web` has run, this
// serves the committed placeholder in dist/index.html.
func Assets() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Unreachable: the go:embed directive above guarantees "dist" is a
		// directory in distFS at compile time.
		panic(err)
	}
	return sub
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/adapters/webui/... -v`
Expected: PASS (all tests in the package)

- [ ] **Step 6: Update .gitignore so the real build output isn't committed**

Add to `.gitignore`:

```
internal/adapters/webui/dist/*
!internal/adapters/webui/dist/index.html
```

This keeps the committed placeholder tracked while ignoring everything `npm run build` writes into `dist/` (including the real `index.html` it overwrites — after running a real frontend build locally, `git status` will show `dist/index.html` as modified; that's expected and should not be committed on its own by a backend-only change).

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/webui/webui.go internal/adapters/webui/webui_test.go internal/adapters/webui/dist/index.html .gitignore
git commit -m "Embed the web UI build with a placeholder for pre-frontend builds"
```

---

### Task 5: `CommandResult.RenderJSON`

**Files:**
- Modify: `internal/bootstrap/command_result.go`
- Test: `internal/bootstrap/command_result_test.go` (create if it doesn't exist — check first with `ls internal/bootstrap/command_result_test.go`)

**Interfaces:**
- Produces: `CommandResult.RenderJSON() ([]byte, error)` — consumed by Task 6/7 (`bootstrap/web.go`).

- [ ] **Step 1: Write the failing test**

```go
package bootstrap

import (
	"encoding/json"
	"testing"
)

func TestRenderJSONProducesStableLowercaseFieldNames(t *testing.T) {
	result := CommandResult{
		State:  ResultSuccess,
		Title:  "Set provider deepseek.",
		Detail: "Restart Eggy for this to take effect.",
		Fields: []ResultField{{Label: "Provider", Value: "deepseek"}},
		Next:   []string{"/restart"},
	}
	body, err := result.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["state"] != "success" || decoded["title"] != "Set provider deepseek." {
		t.Fatalf("decoded=%#v", decoded)
	}
	fields, ok := decoded["fields"].([]any)
	if !ok || len(fields) != 1 {
		t.Fatalf("fields=%#v", decoded["fields"])
	}
	field, ok := fields[0].(map[string]any)
	if !ok || field["label"] != "Provider" || field["value"] != "deepseek" {
		t.Fatalf("field=%#v", field)
	}
	next, ok := decoded["next"].([]any)
	if !ok || len(next) != 1 || next[0] != "/restart" {
		t.Fatalf("next=%#v", decoded["next"])
	}
}

func TestRenderJSONOmitsEmptyFields(t *testing.T) {
	body, err := CommandResult{State: ResultInfo, Title: "No providers configured."}.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"detail", "fields", "table_headers", "table_rows", "lines", "next"} {
		if _, present := decoded[absent]; present {
			t.Fatalf("expected %q to be omitted, decoded=%#v", absent, decoded)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap/... -run TestRenderJSON -v`
Expected: FAIL — `RenderJSON` doesn't exist yet.

- [ ] **Step 3: Implement**

In `internal/bootstrap/command_result.go`, add `encoding/json` to the imports, add JSON tags to `ResultField` and `CommandResult`, and add the new method:

```go
import (
	"encoding/json"
	"strings"
	"text/tabwriter"
)
```

```go
type ResultField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
```

```go
type CommandResult struct {
	State        ResultState   `json:"state"`
	Title        string        `json:"title,omitempty"`
	Detail       string        `json:"detail,omitempty"`
	Fields       []ResultField `json:"fields,omitempty"`
	TableHeaders []string      `json:"table_headers,omitempty"`
	TableRows    [][]string    `json:"table_rows,omitempty"`
	Lines        []string      `json:"lines,omitempty"`
	Next         []string      `json:"next,omitempty"`
}
```

(Keep every existing doc comment on these fields exactly as it is today — only the struct tags are new.)

Add the method near `RenderMarkdown`:

```go
// RenderJSON renders r as the stable JSON shape Eggy's web UI consumes.
// Field names are part of the web API contract; do not rename them without
// also updating web/src/api.ts.
func (r CommandResult) RenderJSON() ([]byte, error) {
	return json.Marshal(r)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bootstrap/... -v`
Expected: PASS (all tests in the package — confirms adding JSON tags didn't break `RenderPlainText`/`RenderMarkdown`)

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/command_result.go internal/bootstrap/command_result_test.go
git commit -m "Add CommandResult.RenderJSON for the web UI"
```

---

### Task 6: Web login, logout, and session-check handlers

**Files:**
- Create: `internal/bootstrap/web.go`
- Test: `internal/bootstrap/web_test.go`

**Interfaces:**
- Consumes: `webui.SignSession`, `webui.VerifySession` (Task 2), `webui.NewLoginThrottle`/`LoginThrottle` (Task 3), `webui.Assets()` (Task 4), `CommandResult.RenderJSON` (Task 5).
- Produces: `type WebUIConfig struct { UserEmail, Password string; SigningKey []byte; Now func() time.Time }`, `NewWebHandler(configPath string, webConfig WebUIConfig) http.Handler` — consumed by Task 8 (`server.go`) and Task 9 (`app.go`).

- [ ] **Step 1: Write the failing tests**

```go
package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testWebConfig(now time.Time) WebUIConfig {
	return WebUIConfig{
		UserEmail: "owner@example.com", Password: "hunter2",
		SigningKey: []byte("test-signing-key"),
		Now:        func() time.Time { return now },
	}
}

func TestWebLoginSucceedsAndSetsSessionCookie(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))

	body := strings.NewReader(`{"email":"owner@example.com","password":"hunter2"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/login", body)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "eggy_session" || cookies[0].Value == "" {
		t.Fatalf("cookies=%#v", cookies)
	}
	if !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie=%#v", cookies[0])
	}
}

func TestWebLoginRejectsWrongPassword(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))

	body := strings.NewReader(`{"email":"owner@example.com","password":"wrong"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/login", body)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("expected no cookie on failed login")
	}
}

func TestWebLoginRejectsWhenNotConfigured(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := testWebConfig(now)
	config.UserEmail, config.Password = "", ""
	handler := NewWebHandler("", config)

	body := strings.NewReader(`{"email":"anyone@example.com","password":"anything"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/login", body)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestWebSessionRequiresValidCookie(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))

	unauthed := httptest.NewRecorder()
	handler.ServeHTTP(unauthed, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	if unauthed.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", unauthed.Code)
	}

	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"owner@example.com","password":"hunter2"}`)))
	cookie := login.Result().Cookies()[0]

	authed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.AddCookie(cookie)
	handler.ServeHTTP(authed, request)
	if authed.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", authed.Code, authed.Body.String())
	}
}

func TestWebLogoutClearsSessionCookie(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/logout", nil))
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("expected a clearing cookie (negative MaxAge), got %#v", cookies)
	}
}

func TestWebLoginThrottlesRepeatedFailures(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))
	badLogin := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"owner@example.com","password":"wrong"}`))
		request.RemoteAddr = "9.9.9.9:12345"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for i := 0; i < 5; i++ {
		if badLogin().Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401", i)
		}
	}
	start := time.Now()
	badLogin()
	if elapsed := time.Since(start); elapsed < 2*time.Second {
		t.Fatalf("expected the 6th attempt to be delayed ~2s, took %v", elapsed)
	}
}

func TestWebResponseBodyIsRenderJSONShape(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"owner@example.com","password":"hunter2"}`)))
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["state"] != "success" {
		t.Fatalf("decoded=%#v", decoded)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap/... -run TestWeb -v`
Expected: FAIL — `WebUIConfig`/`NewWebHandler` don't exist yet.

- [ ] **Step 3: Implement**

```go
package bootstrap

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/webui"
)

// WebUIConfig holds what NewWebHandler needs beyond the config file path:
// the single owner login credential and the key used to sign session
// cookies (Eggy's existing EGGY_ENCRYPTION_KEY -- see the design spec at
// docs/superpowers/specs/2026-07-22-web-config-ui-design.md).
type WebUIConfig struct {
	UserEmail  string
	Password   string
	SigningKey []byte
	Now        func() time.Time
}

const (
	webSessionCookie = "eggy_session"
	webSessionTTL    = 12 * time.Hour
)

// NewWebHandler serves Eggy's embedded web configuration UI and its small
// JSON API. Every /api/config/* route is a thin translation into the same
// CommandRequest/CommandResult shape Telegram and the CLI already use, so
// there is exactly one place config validation and mutation logic lives.
// configPath may be empty in tests that only exercise login/session/logout.
func NewWebHandler(configPath string, webConfig WebUIConfig) http.Handler {
	now := webConfig.Now
	if now == nil {
		now = time.Now
	}
	throttle := webui.NewLoginThrottle(now)

	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(webui.Assets())))
	mux.HandleFunc("POST /api/login", handleWebLogin(webConfig, throttle, now))
	mux.HandleFunc("POST /api/logout", handleWebLogout())
	mux.Handle("GET /api/session", requireWebSession(webConfig, now, func(w http.ResponseWriter, _ *http.Request) {
		writeWebResult(w, CommandResult{Title: "Session is valid."})
	}))
	return mux
}

func handleWebLogin(webConfig WebUIConfig, throttle *webui.LoginThrottle, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if delay := throttle.Delay(ip); delay > 0 {
			time.Sleep(delay)
		}
		var credentials struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if webConfig.UserEmail == "" || webConfig.Password == "" {
			writeWebError(w, http.StatusUnauthorized, "web UI login is not configured")
			return
		}
		if !constantTimeEqual(credentials.Email, webConfig.UserEmail) || !constantTimeEqual(credentials.Password, webConfig.Password) {
			throttle.RecordFailure(ip)
			writeWebError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		throttle.Reset(ip)
		expiresAt := now().Add(webSessionTTL)
		http.SetCookie(w, &http.Cookie{
			Name: webSessionCookie, Value: webui.SignSession(webConfig.SigningKey, expiresAt),
			Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, Expires: expiresAt,
		})
		writeWebResult(w, CommandResult{Title: "Logged in."})
	}
}

func handleWebLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name: webSessionCookie, Value: "", Path: "/",
			HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1,
		})
		writeWebResult(w, CommandResult{Title: "Logged out."})
	}
}

func requireWebSession(webConfig WebUIConfig, now func() time.Time, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(webSessionCookie)
		if err != nil || !webui.VerifySession(webConfig.SigningKey, cookie.Value, now()) {
			writeWebError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		next(w, r)
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writeWebResult(w http.ResponseWriter, result CommandResult) {
	body, err := result.RenderJSON()
	if err != nil {
		writeWebError(w, http.StatusInternalServerError, "failed to render response")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatusForState(result.State))
	_, _ = w.Write(body)
}

func writeWebError(w http.ResponseWriter, status int, message string) {
	body, _ := json.Marshal(CommandResult{State: ResultError, Title: message})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// httpStatusForState maps a CommandResult's classification to the HTTP
// status the web API returns.
func httpStatusForState(state ResultState) int {
	switch state {
	case ResultError, ResultHelp:
		return http.StatusBadRequest
	default:
		return http.StatusOK
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bootstrap/... -run TestWeb -v`
Expected: PASS (all 7 test functions). `TestWebLoginThrottlesRepeatedFailures` takes >2s — that's expected.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/web.go internal/bootstrap/web_test.go
git commit -m "Add web UI login, logout, and session-check handlers"
```

---

### Task 7: `/api/config/*` routes bridging into `CommandService`

**Files:**
- Modify: `internal/bootstrap/web.go` (extend `NewWebHandler`)
- Modify: `internal/bootstrap/web_test.go`

**Interfaces:**
- Consumes: `CommandService.dispatch(ctx, CommandRequest) (CommandResult, error)` (unexported, same package — `internal/bootstrap/commands.go:76-81`), the existing catalog entries `"config get providers"`, `"config set provider"`, `"config get models"`, `"config set model"`, `"config get calendar"`, `"config set calendar"`.
- Produces: nothing new consumed elsewhere — this is the last piece `NewWebHandler` needs before wiring (Task 8/9).

- [ ] **Step 1: Write the failing tests**

Add `"os"` to `internal/bootstrap/web_test.go`'s existing import block (it's not there yet — Task 6 didn't need it), then add these functions:

```go
func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWebConfigRoutesRoundTripThroughCommandService(t *testing.T) {
	// validConfig() and config_test.go are in this same package (bootstrap),
	// so it's reused directly here rather than duplicating its YAML.
	path := writeConfigFile(t, validConfig())

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))
	cookie := webLoginCookie(t, handler)

	setBody := strings.NewReader(`{"name":"deepseek","adapter":"openai_compatible","base_url":"https://api.deepseek.com","api_key_env":"DEEPSEEK_API_KEY"}`)
	setRequest := httptest.NewRequest(http.MethodPost, "/api/config/providers", setBody)
	setRequest.AddCookie(cookie)
	setResponse := httptest.NewRecorder()
	handler.ServeHTTP(setResponse, setRequest)
	if setResponse.Code != http.StatusOK {
		t.Fatalf("set status=%d body=%s", setResponse.Code, setResponse.Body.String())
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/config/providers", nil)
	getRequest.AddCookie(cookie)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}
	var decoded CommandResult
	if err := json.Unmarshal(getResponse.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range decoded.TableRows {
		if len(row) > 0 && row[0] == "deepseek" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the newly set provider in table rows: %#v", decoded.TableRows)
	}
}

func TestWebConfigRoutesRejectInvalidInputLikeCLIAndTelegram(t *testing.T) {
	path := writeConfigFile(t, validConfig())

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))
	cookie := webLoginCookie(t, handler)

	setRequest := httptest.NewRequest(http.MethodPost, "/api/config/providers", strings.NewReader(`{"name":"deepseek"}`))
	setRequest.AddCookie(cookie)
	setResponse := httptest.NewRecorder()
	handler.ServeHTTP(setResponse, setRequest)
	if setResponse.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", setResponse.Code, setResponse.Body.String())
	}
}

func TestWebConfigRoutesRequireSession(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))
	for _, path := range []string{"/api/config/providers", "/api/config/models", "/api/config/calendar"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", path, response.Code)
		}
	}
}

func webLoginCookie(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"owner@example.com","password":"hunter2"}`)))
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected exactly one cookie, got %d", len(cookies))
	}
	return cookies[0]
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap/... -run TestWebConfigRoutes -v`
Expected: FAIL — `/api/config/*` routes aren't registered yet (404).

- [ ] **Step 3: Implement**

In `internal/bootstrap/web.go`, extend `NewWebHandler`:

```go
func NewWebHandler(configPath string, webConfig WebUIConfig) http.Handler {
	now := webConfig.Now
	if now == nil {
		now = time.Now
	}
	throttle := webui.NewLoginThrottle(now)
	commands := &CommandService{configPath: configPath}

	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(webui.Assets())))
	mux.HandleFunc("POST /api/login", handleWebLogin(webConfig, throttle, now))
	mux.HandleFunc("POST /api/logout", handleWebLogout())
	mux.Handle("GET /api/session", requireWebSession(webConfig, now, func(w http.ResponseWriter, _ *http.Request) {
		writeWebResult(w, CommandResult{Title: "Session is valid."})
	}))

	for _, section := range []struct{ path string; get, set []string }{
		{"providers", []string{"config", "get", "providers"}, []string{"config", "set", "provider"}},
		{"models", []string{"config", "get", "models"}, []string{"config", "set", "model"}},
		{"calendar", []string{"config", "get", "calendar"}, []string{"config", "set", "calendar"}},
	} {
		mux.Handle("GET /api/config/"+section.path, requireWebSession(webConfig, now, webConfigGetRoute(commands, section.get)))
		mux.Handle("POST /api/config/"+section.path, requireWebSession(webConfig, now, webConfigSetRoute(commands, section.set)))
	}

	return mux
}

func webConfigGetRoute(commands *CommandService, path []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := commands.dispatch(r.Context(), CommandRequest{Path: path})
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebResult(w, result)
	}
}

func webConfigSetRoute(commands *CommandService, path []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var named map[string]string
		if err := json.NewDecoder(r.Body).Decode(&named); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		result, err := commands.dispatch(r.Context(), CommandRequest{Path: path, Named: named})
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebResult(w, result)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bootstrap/... -v`
Expected: PASS (entire package, including Task 6's tests — confirms no regression)

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/web.go internal/bootstrap/web_test.go
git commit -m "Bridge web UI config routes into the shared CommandService"
```

---

### Task 8: Mount the web handler on Eggy's HTTP server

**Files:**
- Modify: `internal/bootstrap/server.go`
- Modify: `internal/bootstrap/server_test.go`

**Interfaces:**
- Consumes: `NewWebHandler` (Task 7) — passed in by callers, not called directly by `server.go`.
- Produces: `NewHTTPHandlerAt(telegramPath string, ready func() error, telegram, googleStart, googleCallback, web http.Handler, mcpCallback ...http.Handler) http.Handler`, `NewHTTPHandler(ready func() error, telegram, googleStart, googleCallback, web http.Handler, mcpCallback ...http.Handler) http.Handler` — consumed by Task 9 (`app.go`).

- [ ] **Step 1: Update the existing tests for the new parameter**

In `internal/bootstrap/server_test.go`, every existing call to `NewHTTPHandler` gains one more `http.Handler` argument (`nil` unless the test is specifically about the web handler) in the position right after `googleCallback`:

```go
func TestHTTPHandlerHealthAndReadiness(t *testing.T) {
	readyErr := errors.New("calendar unavailable")
	telegramCalls := 0
	handler := NewHTTPHandler(func() error { return readyErr }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		telegramCalls++
		w.WriteHeader(http.StatusNoContent)
	}), nil, nil, nil)
	// ... rest of the function is unchanged ...
```

```go
func TestHTTPHandlerOptionalGoogleRoutes(t *testing.T) {
	start := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTemporaryRedirect) })
	callback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := NewHTTPHandler(func() error { return nil }, nil, start, callback, nil)
	// ... rest of the function is unchanged ...
```

```go
func TestHTTPHandlerOptionalMCPCallbackRoute(t *testing.T) {
	callback := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.PathValue("server") != "railway" {
			t.Fatalf("server=%q", request.PathValue("server"))
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := NewHTTPHandler(func() error { return nil }, nil, nil, nil, nil, callback)
	// ... rest of the function is unchanged ...
```

Add a new test proving the web handler is mounted as the fallback:

```go
func TestHTTPHandlerMountsWebHandlerAsFallback(t *testing.T) {
	web := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	handler := NewHTTPHandler(func() error { return nil }, nil, nil, nil, web)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/anything", nil))
	if response.Code != http.StatusTeapot {
		t.Fatalf("status=%d, want the web handler's fallback response", response.Code)
	}
	// /healthz must still take priority over the web handler's own "/" route.
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("healthz status=%d", health.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap/... -run TestHTTPHandler -v`
Expected: FAIL — compile error (wrong argument count) until Step 3.

- [ ] **Step 3: Implement**

```go
func NewHTTPHandler(ready func() error, telegram, googleStart, googleCallback, web http.Handler, mcpCallback ...http.Handler) http.Handler {
	return NewHTTPHandlerAt("/webhooks/telegram", ready, telegram, googleStart, googleCallback, web, mcpCallback...)
}

func NewHTTPHandlerAt(telegramPath string, ready func() error, telegram, googleStart, googleCallback, web http.Handler, mcpCallback ...http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if ready != nil {
			if err := ready(); err != nil {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})
	if telegram != nil {
		mux.Handle("POST "+telegramPath, telegram)
	} else {
		mux.HandleFunc(telegramPath, unavailable)
	}
	if googleStart != nil {
		mux.Handle("GET /auth/google", googleStart)
	}
	if googleCallback != nil {
		mux.Handle("GET /auth/google/callback", googleCallback)
	}
	if len(mcpCallback) > 0 && mcpCallback[0] != nil {
		mux.Handle("GET /auth/mcp/{server}/callback", mcpCallback[0])
	}
	if web != nil {
		mux.Handle("/", web)
	}
	return mux
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bootstrap/... -v`
Expected: PASS (entire package)

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/server.go internal/bootstrap/server_test.go
git commit -m "Mount the web UI handler on Eggy's HTTP server"
```

---

### Task 9: Wire the web handler into `App`

**Files:**
- Modify: `internal/bootstrap/app.go:337` (the `NewHTTPHandlerAt` call site)

**Interfaces:**
- Consumes: `Secrets.UIUserEmail`/`UIPassword`/`EncryptionKey` (Task 1), `WebUIConfig`/`NewWebHandler` (Task 6/7), `NewHTTPHandlerAt` (Task 8).

- [ ] **Step 1: Read the current call site and its surrounding context**

Run: `sed -n '325,340p' internal/bootstrap/app.go` to confirm line numbers haven't shifted from earlier tasks before editing.

- [ ] **Step 2: Implement**

Replace the existing line:

```go
	app.httpHandler = NewHTTPHandlerAt(config.Server.TelegramWebhookPath, app.Ready, webhook, googleStart, googleCallback, mcpCallbackHandler(app.mcp, options.RequestRestart))
```

with:

```go
	webHandler := NewWebHandler(options.ConfigPath, WebUIConfig{
		UserEmail: secrets.UIUserEmail, Password: secrets.UIPassword,
		SigningKey: []byte(secrets.EncryptionKey), Now: options.Now,
	})
	app.httpHandler = NewHTTPHandlerAt(config.Server.TelegramWebhookPath, app.Ready, webhook, googleStart, googleCallback, webHandler, mcpCallbackHandler(app.mcp, options.RequestRestart))
```

(`secrets` and `options` are already in scope in `NewApp` at this point — `secrets` is the function's second parameter, `options` the third; confirm this by checking `NewApp`'s signature at the top of the function before editing.)

- [ ] **Step 3: Verify the whole app builds and its own tests still pass**

Run: `go build ./... && go test ./internal/bootstrap/... -v`
Expected: build succeeds; all `app_test.go` tests still pass (they don't need new assertions — this task only changes wiring, not `App`'s public behavior).

- [ ] **Step 4: Run the full test suite**

Run: `go test ./...`
Expected: all packages pass.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/app.go
git commit -m "Wire the web UI into App construction"
```

---

### Task 10: Scaffold the Vite + React + TypeScript + Tailwind project

**Files:**
- Create: `web/package.json`, `web/vite.config.ts`, `web/tailwind.config.js`, `web/postcss.config.js`, `web/tsconfig.json`, `web/tsconfig.node.json`, `web/index.html`, `web/src/main.tsx`, `web/src/index.css`
- Modify: `Makefile`, `Dockerfile`

**Interfaces:**
- Produces: a `web/` project whose `npm run build` writes to `internal/adapters/webui/dist`, ready for Task 11-13 to add real source files into `web/src/`.

- [ ] **Step 1: Create `web/package.json`**

```json
{
  "name": "eggy-web",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc && vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "react": "^18.3.1",
    "react-dom": "^18.3.1"
  },
  "devDependencies": {
    "@types/react": "^18.3.3",
    "@types/react-dom": "^18.3.0",
    "@vitejs/plugin-react": "^4.3.1",
    "autoprefixer": "^10.4.19",
    "postcss": "^8.4.38",
    "tailwindcss": "^3.4.4",
    "typescript": "^5.5.2",
    "vite": "^5.3.1"
  }
}
```

- [ ] **Step 2: Create `web/vite.config.ts`**

```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/adapters/webui/dist",
    emptyOutDir: true,
  },
});
```

- [ ] **Step 3: Create `web/tailwind.config.js`**

```js
/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: { extend: {} },
  plugins: [],
};
```

- [ ] **Step 4: Create `web/postcss.config.js`**

```js
export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
};
```

- [ ] **Step 5: Create `web/tsconfig.json`**

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "resolveJsonModule": true,
    "isolatedModules": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true
  },
  "include": ["src"],
  "references": [{ "path": "./tsconfig.node.json" }]
}
```

- [ ] **Step 6: Create `web/tsconfig.node.json`**

```json
{
  "compilerOptions": {
    "composite": true,
    "skipLibCheck": true,
    "module": "ESNext",
    "moduleResolution": "bundler",
    "allowSyntheticDefaultImports": true
  },
  "include": ["vite.config.ts"]
}
```

- [ ] **Step 7: Create `web/index.html`**

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Eggy</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 8: Create `web/src/index.css`**

```css
@tailwind base;
@tailwind components;
@tailwind utilities;
```

- [ ] **Step 9: Create `web/src/main.tsx`**

(This references `App` from `./App`, which Task 13 creates. Until then `npm run build` in this task will fail on that missing import — that's expected and resolved by Task 13; this task's own verification step only checks dependency install and config validity, not a full build.)

```tsx
import React from "react";
import ReactDOM from "react-dom/client";
import { App } from "./App";
import "./index.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
```

- [ ] **Step 10: Add a `build-web` target to the `Makefile`**

Modify `Makefile`:

```makefile
GO ?= go

.PHONY: fmt vet test race build build-web smoke clean

fmt:
	gofmt -w cmd internal

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

build-web:
	cd web && npm install && npm run build

build: build-web
	mkdir -p bin
	$(GO) build -trimpath -o bin/eggyd ./cmd/eggyd
	$(GO) build -trimpath -o bin/eggy ./cmd/eggy

smoke:
	./scripts/docker-smoke.sh

clean:
	rm -f bin/eggyd bin/eggy
```

- [ ] **Step 11: Add a Node build stage to the `Dockerfile`**

Modify `Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1.7
FROM node:22-bookworm-slim AS web-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm install
COPY web/ ./
RUN npm run build

FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /src/internal/adapters/webui/dist ./internal/adapters/webui/dist
RUN CGO_ENABLED=0 go test ./... \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eggyd ./cmd/eggyd \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eggy ./cmd/eggy

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl git openssh-client tini \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /usr/local/go /usr/local/go
COPY --from=builder /out/eggyd /usr/local/bin/eggyd
COPY --from=builder /out/eggy /usr/local/bin/eggy
RUN mkdir -p /tmp/runs
ENV EGGY_CONFIG=/data/config.yaml \
    PATH="/usr/local/go/bin:${PATH}"
EXPOSE 8080
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["eggyd"]
```

- [ ] **Step 12: Verify dependency install works**

Run: `cd web && npm install`
Expected: completes without error (an `App.tsx` compile error is expected at this point and is not part of this task's verification — Task 13 resolves it).

- [ ] **Step 13: Commit**

```bash
git add web/package.json web/package-lock.json web/vite.config.ts web/tailwind.config.js web/postcss.config.js web/tsconfig.json web/tsconfig.node.json web/index.html web/src/main.tsx web/src/index.css Makefile Dockerfile
git commit -m "Scaffold the Vite + React + Tailwind web UI project"
```

Note: do not commit `web/node_modules/` — add `node_modules/` to `.gitignore` if it isn't already covered (check `git status` after `npm install`; if `web/node_modules/` shows as untracked, add a `web/node_modules/` line to `.gitignore` in this same commit).

---

### Task 11: `api.ts` and the `useConfigSection` hook

**Files:**
- Create: `web/src/api.ts`
- Create: `web/src/useConfigSection.ts`

**Interfaces:**
- Produces: `CommandResult` (TS type matching `internal/bootstrap/command_result.go`'s JSON shape), `SessionExpiredError`, `checkSession()`, `login(email, password)`, `logout()`, `getConfig(section)`, `setConfig(section, values)`, `useConfigSection(section, onSessionExpired)` returning `{ result, error, saving, save }` — consumed by Task 12 (cards) and Task 13 (`LoginPage`/`App`).

- [ ] **Step 1: Create `web/src/api.ts`**

```ts
export type ResultField = { label: string; value: string };

export type CommandResult = {
  state: "success" | "info" | "warning" | "error" | "help";
  title?: string;
  detail?: string;
  fields?: ResultField[];
  table_headers?: string[];
  table_rows?: string[][];
  lines?: string[];
  next?: string[];
};

export class SessionExpiredError extends Error {}

async function request(path: string, init?: RequestInit): Promise<CommandResult> {
  const response = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  const body = (await response.json()) as CommandResult;
  if (response.status === 401) {
    throw new SessionExpiredError(body.title ?? "Not authenticated");
  }
  if (!response.ok) {
    throw new Error(body.title ?? "Request failed");
  }
  return body;
}

export function checkSession(): Promise<CommandResult> {
  return request("/api/session");
}

export function login(email: string, password: string): Promise<CommandResult> {
  return request("/api/login", { method: "POST", body: JSON.stringify({ email, password }) });
}

export function logout(): Promise<CommandResult> {
  return request("/api/logout", { method: "POST" });
}

export type ConfigSection = "providers" | "models" | "calendar";

export function getConfig(section: ConfigSection): Promise<CommandResult> {
  return request(`/api/config/${section}`);
}

export function setConfig(section: ConfigSection, values: Record<string, string>): Promise<CommandResult> {
  return request(`/api/config/${section}`, { method: "POST", body: JSON.stringify(values) });
}
```

- [ ] **Step 2: Create `web/src/useConfigSection.ts`**

```ts
import { useCallback, useEffect, useState } from "react";
import { CommandResult, ConfigSection, SessionExpiredError, getConfig, setConfig } from "./api";

export function useConfigSection(section: ConfigSection, onSessionExpired: () => void) {
  const [result, setResult] = useState<CommandResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const load = useCallback(() => {
    getConfig(section)
      .then(setResult)
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setError(err instanceof Error ? err.message : "Failed to load");
      });
  }, [section, onSessionExpired]);

  useEffect(() => {
    load();
  }, [load]);

  const save = useCallback(
    async (values: Record<string, string>) => {
      setSaving(true);
      setError(null);
      try {
        const saved = await setConfig(section, values);
        setResult(saved);
        load();
      } catch (err) {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setError(err instanceof Error ? err.message : "Failed to save");
      } finally {
        setSaving(false);
      }
    },
    [section, load, onSessionExpired],
  );

  return { result, error, saving, save };
}
```

- [ ] **Step 3: Verify TypeScript compiles these two files in isolation**

Run: `cd web && npx tsc --noEmit src/api.ts src/useConfigSection.ts 2>&1 | grep -v "Cannot find module './App'"`
Expected: no errors reported for `api.ts`/`useConfigSection.ts` themselves (any error must reference a file outside this task's scope, e.g. missing `App.tsx`, which Task 13 adds).

- [ ] **Step 4: Commit**

```bash
git add web/src/api.ts web/src/useConfigSection.ts
git commit -m "Add the web UI's API client and config-section data hook"
```

---

### Task 12: Config cards (`ProvidersCard`, `ModelsCard`, `CalendarCard`, `ConfigPage`)

**Files:**
- Create: `web/src/ProvidersCard.tsx`, `web/src/ModelsCard.tsx`, `web/src/CalendarCard.tsx`, `web/src/ConfigPage.tsx`

**Interfaces:**
- Consumes: `useConfigSection` (Task 11).
- Produces: `ConfigPage({ onSessionExpired: () => void })` — consumed by Task 13 (`App.tsx`).

- [ ] **Step 1: Create `web/src/ProvidersCard.tsx`**

```tsx
import { useState } from "react";
import { useConfigSection } from "./useConfigSection";

export function ProvidersCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save } = useConfigSection("providers", onSessionExpired);
  const [name, setName] = useState("");
  const [adapter, setAdapter] = useState("openai_compatible");
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKeyEnv, setApiKeyEnv] = useState("");

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    await save({ name, adapter, base_url: baseUrl, api_key_env: apiKeyEnv });
    setName("");
    setBaseUrl("");
    setApiKeyEnv("");
  }

  return (
    <section className="rounded-lg bg-white p-6 shadow">
      <h2 className="mb-4 text-lg font-semibold text-slate-900">Providers</h2>
      {result?.table_rows && result.table_rows.length > 0 ? (
        <table className="mb-4 w-full text-left text-sm">
          <thead>
            <tr>
              {result.table_headers?.map((header) => (
                <th key={header} className="border-b border-slate-200 pb-2 pr-4 font-medium text-slate-500">
                  {header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {result.table_rows.map((row, index) => (
              <tr key={index}>
                {row.map((cell, cellIndex) => (
                  <td key={cellIndex} className="border-b border-slate-100 py-2 pr-4 text-slate-700">
                    {cell}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p className="mb-4 text-sm text-slate-500">No providers configured yet.</p>
      )}
      <form onSubmit={handleSubmit} className="grid grid-cols-2 gap-3">
        <input placeholder="name" value={name} onChange={(e) => setName(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <input placeholder="adapter" value={adapter} onChange={(e) => setAdapter(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <input placeholder="base_url" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <input placeholder="api_key_env" value={apiKeyEnv} onChange={(e) => setApiKeyEnv(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <button type="submit" disabled={saving} className="col-span-2 rounded bg-slate-900 px-4 py-2 text-white disabled:opacity-50">
          {saving ? "Saving..." : "Add provider"}
        </button>
      </form>
      {result?.detail && <p className="mt-3 text-xs text-slate-500">{result.detail}</p>}
      {error && <p className="mt-3 text-sm text-red-600">{error}</p>}
    </section>
  );
}
```

- [ ] **Step 2: Create `web/src/ModelsCard.tsx`**

```tsx
import { useState } from "react";
import { useConfigSection } from "./useConfigSection";

export function ModelsCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save } = useConfigSection("models", onSessionExpired);
  const [alias, setAlias] = useState("");
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [reasoningEfforts, setReasoningEfforts] = useState("");

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    await save({ alias, provider, model, reasoning_efforts: reasoningEfforts });
    setAlias("");
    setProvider("");
    setModel("");
    setReasoningEfforts("");
  }

  return (
    <section className="rounded-lg bg-white p-6 shadow">
      <h2 className="mb-4 text-lg font-semibold text-slate-900">Models</h2>
      {result?.table_rows && result.table_rows.length > 0 ? (
        <table className="mb-4 w-full text-left text-sm">
          <thead>
            <tr>
              {result.table_headers?.map((header) => (
                <th key={header} className="border-b border-slate-200 pb-2 pr-4 font-medium text-slate-500">
                  {header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {result.table_rows.map((row, index) => (
              <tr key={index}>
                {row.map((cell, cellIndex) => (
                  <td key={cellIndex} className="border-b border-slate-100 py-2 pr-4 text-slate-700">
                    {cell}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p className="mb-4 text-sm text-slate-500">No models configured yet.</p>
      )}
      <form onSubmit={handleSubmit} className="grid grid-cols-2 gap-3">
        <input placeholder="alias" value={alias} onChange={(e) => setAlias(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <input placeholder="provider" value={provider} onChange={(e) => setProvider(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <input placeholder="model" value={model} onChange={(e) => setModel(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <input
          placeholder="reasoning_efforts (comma-separated, optional)"
          value={reasoningEfforts}
          onChange={(e) => setReasoningEfforts(e.target.value)}
          className="rounded border border-slate-300 px-3 py-2"
        />
        <button type="submit" disabled={saving} className="col-span-2 rounded bg-slate-900 px-4 py-2 text-white disabled:opacity-50">
          {saving ? "Saving..." : "Add model"}
        </button>
      </form>
      {result?.detail && <p className="mt-3 text-xs text-slate-500">{result.detail}</p>}
      {error && <p className="mt-3 text-sm text-red-600">{error}</p>}
    </section>
  );
}
```

- [ ] **Step 3: Create `web/src/CalendarCard.tsx`**

```tsx
import { useEffect, useState } from "react";
import { useConfigSection } from "./useConfigSection";

function fieldValue(fields: { label: string; value: string }[] | undefined, label: string): string {
  return fields?.find((field) => field.label === label)?.value ?? "";
}

export function CalendarCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save } = useConfigSection("calendar", onSessionExpired);
  const [enabled, setEnabled] = useState("false");
  const [defaultCalendar, setDefaultCalendar] = useState("");
  const [timezone, setTimezone] = useState("");
  const [initialized, setInitialized] = useState(false);

  useEffect(() => {
    if (result && !initialized) {
      setEnabled(fieldValue(result.fields, "Enabled") || "false");
      setDefaultCalendar(fieldValue(result.fields, "Default calendar"));
      setTimezone(fieldValue(result.fields, "Timezone"));
      setInitialized(true);
    }
  }, [result, initialized]);

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    await save({ enabled, default_calendar: defaultCalendar, timezone });
  }

  return (
    <section className="rounded-lg bg-white p-6 shadow">
      <h2 className="mb-4 text-lg font-semibold text-slate-900">Calendar</h2>
      <form onSubmit={handleSubmit} className="grid grid-cols-2 gap-3">
        <label className="col-span-2 flex items-center gap-2 text-sm text-slate-700">
          <input type="checkbox" checked={enabled === "true"} onChange={(e) => setEnabled(e.target.checked ? "true" : "false")} />
          Enabled
        </label>
        <input
          placeholder="default_calendar"
          value={defaultCalendar}
          onChange={(e) => setDefaultCalendar(e.target.value)}
          className="rounded border border-slate-300 px-3 py-2"
        />
        <input placeholder="timezone (IANA)" value={timezone} onChange={(e) => setTimezone(e.target.value)} className="rounded border border-slate-300 px-3 py-2" />
        <button type="submit" disabled={saving} className="col-span-2 rounded bg-slate-900 px-4 py-2 text-white disabled:opacity-50">
          {saving ? "Saving..." : "Save calendar settings"}
        </button>
      </form>
      {error && <p className="mt-3 text-sm text-red-600">{error}</p>}
    </section>
  );
}
```

- [ ] **Step 4: Create `web/src/ConfigPage.tsx`**

```tsx
import { ProvidersCard } from "./ProvidersCard";
import { ModelsCard } from "./ModelsCard";
import { CalendarCard } from "./CalendarCard";

export function ConfigPage({ onSessionExpired }: { onSessionExpired: () => void }) {
  return (
    <div className="min-h-screen bg-slate-100 p-8">
      <div className="mx-auto flex max-w-2xl flex-col gap-6">
        <h1 className="text-2xl font-semibold text-slate-900">Eggy config</h1>
        <ProvidersCard onSessionExpired={onSessionExpired} />
        <ModelsCard onSessionExpired={onSessionExpired} />
        <CalendarCard onSessionExpired={onSessionExpired} />
      </div>
    </div>
  );
}
```

- [ ] **Step 5: Verify TypeScript compiles these files in isolation**

Run: `cd web && npx tsc --noEmit src/ProvidersCard.tsx src/ModelsCard.tsx src/CalendarCard.tsx src/ConfigPage.tsx 2>&1 | grep -v "Cannot find module './App'"`
Expected: no errors for these four files (any remaining error must reference `App.tsx`, added in Task 13).

- [ ] **Step 6: Commit**

```bash
git add web/src/ProvidersCard.tsx web/src/ModelsCard.tsx web/src/CalendarCard.tsx web/src/ConfigPage.tsx
git commit -m "Add config UI cards for providers, models, and calendar"
```

---

### Task 13: `LoginPage`, `App`, and end-to-end verification

**Files:**
- Create: `web/src/LoginPage.tsx`, `web/src/App.tsx`

**Interfaces:**
- Consumes: `login` (Task 11), `checkSession` (Task 11), `ConfigPage` (Task 12).
- Produces: `App()` — the root component `main.tsx` (Task 10) already renders.

- [ ] **Step 1: Create `web/src/LoginPage.tsx`**

```tsx
import { useState } from "react";
import { login } from "./api";

export function LoginPage({ onLoggedIn }: { onLoggedIn: () => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await login(email, password);
      onLoggedIn();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Login failed");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-slate-100">
      <form onSubmit={handleSubmit} className="w-full max-w-sm rounded-lg bg-white p-8 shadow">
        <h1 className="mb-6 text-xl font-semibold text-slate-900">Eggy config</h1>
        <label className="mb-1 block text-sm font-medium text-slate-700">Email</label>
        <input
          type="email"
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          className="mb-4 w-full rounded border border-slate-300 px-3 py-2"
          required
        />
        <label className="mb-1 block text-sm font-medium text-slate-700">Password</label>
        <input
          type="password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          className="mb-4 w-full rounded border border-slate-300 px-3 py-2"
          required
        />
        {error && <p className="mb-4 text-sm text-red-600">{error}</p>}
        <button type="submit" disabled={submitting} className="w-full rounded bg-slate-900 px-4 py-2 text-white disabled:opacity-50">
          {submitting ? "Logging in..." : "Log in"}
        </button>
      </form>
    </div>
  );
}
```

- [ ] **Step 2: Create `web/src/App.tsx`**

```tsx
import { useEffect, useState } from "react";
import { checkSession } from "./api";
import { LoginPage } from "./LoginPage";
import { ConfigPage } from "./ConfigPage";

type Status = "checking" | "authenticated" | "unauthenticated";

export function App() {
  const [status, setStatus] = useState<Status>("checking");

  useEffect(() => {
    checkSession()
      .then(() => setStatus("authenticated"))
      .catch(() => setStatus("unauthenticated"));
  }, []);

  if (status === "checking") {
    return <div className="flex min-h-screen items-center justify-center text-slate-500">Loading...</div>;
  }
  if (status === "unauthenticated") {
    return <LoginPage onLoggedIn={() => setStatus("authenticated")} />;
  }
  return <ConfigPage onSessionExpired={() => setStatus("unauthenticated")} />;
}
```

- [ ] **Step 3: Run the real frontend build**

Run: `cd web && npm install && npm run build`
Expected: succeeds with no TypeScript errors, writes real assets into `internal/adapters/webui/dist/` (overwriting the Task 4 placeholder).

- [ ] **Step 4: Verify the Go binary embeds the real build and boots**

Run:
```bash
go build ./... && go test ./... && go build -o /tmp/eggyd ./cmd/eggyd
```
Expected: all pass; `/tmp/eggyd` builds successfully with the real frontend embedded (confirms `//go:embed all:dist` picked up the Vite output, not just the placeholder).

- [ ] **Step 5: Manual end-to-end check**

Run a minimal config (reuse the pattern from earlier manual verification in this project: a temp `config.yaml` with `server.public_base_url`, `data_dir`, `telegram.owner_id` set, plus `EGGY_UI_USER_EMAIL`/`EGGY_UI_PASSWORD`/`EGGY_ENCRYPTION_KEY` env vars set) and confirm in a browser:
1. Visiting the server's root URL shows the login form (not a blank page or the placeholder text).
2. Logging in with the wrong password shows an inline error and does not navigate away.
3. Logging in with the correct credentials shows the three config cards.
4. Adding a provider through the form makes it appear in the table without a page reload.
5. Reloading the page after login stays logged in (cookie persists); clearing cookies returns to the login form.

- [ ] **Step 6: Commit**

```bash
git add web/src/LoginPage.tsx web/src/App.tsx
git commit -m "Add login page and app shell, completing the web config UI"
```

- [ ] **Step 7: Final full verification**

Run: `make fmt vet test race build`
Expected: all pass. Run `make smoke` if Docker is available, confirming the Docker build's new Node stage produces a working image.
