package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// newFinanceApp is a web-only deployment with one account, nigel, optionally
// with finance switched on. FakeAdapters keeps it off the network.
func newFinanceApp(t *testing.T, financeOn bool) *App {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	body := `data_dir: ` + dir + `
server:
  public_base_url: https://eggy.example
accounts:
  - id: nigel
web:
  password_account_id: nigel
agent:
  default_model: model
  timezone: Asia/Singapore
providers:
  provider:
    adapter: openai_compatible
    base_url: https://api.example.com
    api_key_env: MODEL_KEY
models:
  model:
    provider: provider
    model: model-id
repositories: []
runner:
  root: ` + filepath.Join(dir, "runs") + `
  timeout: 5m
  retention: 15m
  max_output_bytes: 1048576
  allowed_env: [PATH]
`
	if financeOn {
		body += "finance:\n  enabled: true\n  currency: SGD\n"
	}
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		return map[string]string{
			"EGGY_UI_USER_EMAIL": "owner@example.com", "EGGY_UI_PASSWORD": "operator-password", "MODEL_KEY": "k",
			"EGGY_ENCRYPTION_KEY": strings.Repeat("A", 43) + "=",
		}[key]
	}
	cfg, secrets, err := config.LoadConfig(configPath, getenv)
	if err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true, ConfigPath: configPath, Getenv: getenv})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.database.Close() })
	return app
}

func sessionFeatures(t *testing.T, app *App, cookie *http.Cookie) []string {
	t.Helper()
	response := apiCall(t, app, cookie, "", http.MethodGet, "/api/session", "")
	var body struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", response.Body.String(), err)
	}
	return body.Features
}

// The model's tool and the panel are two doors onto one store: what the tool
// logs in a chat turn is in the panel's list, and what one account logs is
// invisible to another through either door.
func TestFinanceToolAndPanelShareOneStoreAndStayPrivatePerAccount(t *testing.T) {
	app := newFinanceApp(t, true)
	cookie := loginThrough(t, app, "owner@example.com", "operator-password")
	if cookie == nil {
		t.Fatal("could not sign in")
	}
	_, csrf := sessionOf(t, app, cookie)
	if features := sessionFeatures(t, app, cookie); !slices.Equal(features, []string{"finance"}) {
		t.Fatalf("features = %v", features)
	}

	// The tool, as a chat turn would call it: the registry's gated wrapper, as
	// nigel. Normal mode runs it without asking.
	tool, ok := app.tools.Lookup("finance")
	if !ok {
		t.Fatal("finance is not registered")
	}
	nigel := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "nigel"})
	raw, err := tool.Execute(nigel, json.RawMessage(`{"action":"log","amount":"4.20","category":"food","merchant":"Ya Kun","source":"photo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Logged") {
		t.Fatalf("the tool did not log: %s (in normal mode it must run, not ask)", raw)
	}

	// The panel sees it, and adds its own.
	if response := apiCall(t, app, cookie, csrf, http.MethodPost, "/api/finance/entries", `{"amount":"14.50","category":"transport"}`); response.Code != http.StatusOK {
		t.Fatalf("create: status=%d body=%s", response.Code, response.Body.String())
	}
	list := apiCall(t, app, cookie, csrf, http.MethodGet, "/api/finance/entries", "")
	var listed struct {
		Entries []struct{ ID, Amount, Category, Merchant, Source string } `json:"entries"`
		Total   int                                                       `json:"total"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || listed.Total != 2 {
		t.Fatalf("list = %s (err %v), want both entries", list.Body.String(), err)
	}
	sources := map[string]string{}
	for _, e := range listed.Entries {
		sources[e.Category] = e.Source
	}
	if sources["food"] != "photo" || sources["transport"] != "panel" {
		t.Fatalf("sources = %v", sources)
	}

	summary := apiCall(t, app, cookie, csrf, http.MethodGet, "/api/finance/summary", "")
	var sum struct {
		Totals []struct {
			Currency, Amount string
			Count            int
		} `json:"totals"`
	}
	_ = json.Unmarshal(summary.Body.Bytes(), &sum)
	if len(sum.Totals) != 1 || sum.Totals[0].Currency != "SGD" || sum.Totals[0].Amount != "18.70" || sum.Totals[0].Count != 2 {
		t.Fatalf("summary = %s, want SGD 18.70 over 2 entries", summary.Body.String())
	}

	// A second account: its tool and its panel both see an empty ledger, and
	// it cannot reach nigel's entry by id.
	if response := apiCall(t, app, cookie, csrf, http.MethodPost, "/api/config/accounts", `{"id":"third","password":"third-password-long"}`); response.Code != http.StatusOK {
		t.Fatalf("create third: status=%d body=%s", response.Code, response.Body.String())
	}
	thirdCookie := loginThrough(t, app, "third", "third-password-long")
	if thirdCookie == nil {
		t.Fatal("third could not sign in")
	}
	_, thirdCSRF := sessionOf(t, app, thirdCookie)
	theirs := apiCall(t, app, thirdCookie, thirdCSRF, http.MethodGet, "/api/finance/entries", "")
	if !strings.Contains(theirs.Body.String(), `"total":0`) {
		t.Fatalf("third sees nigel's entries: %s", theirs.Body.String())
	}
	if response := apiCall(t, app, thirdCookie, thirdCSRF, http.MethodDelete, "/api/finance/entries/"+listed.Entries[0].ID, ""); response.Code != http.StatusNotFound {
		t.Fatalf("third deleting nigel's entry: status=%d, want 404", response.Code)
	}
	third := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "third"})
	raw, err = tool.Execute(third, json.RawMessage(`{"action":"list"}`))
	if err != nil || !strings.Contains(string(raw), "No entries") {
		t.Fatalf("third's tool listed %s, %v", raw, err)
	}
	if _, err := tool.Execute(third, json.RawMessage(`{"action":"delete","id":"`+listed.Entries[0].ID+`"}`)); err == nil {
		t.Fatal("third's tool deleted nigel's entry")
	}

	// Nigel's ledger is untouched, and a correction through the panel shows in
	// the tool's next read: one store.
	if response := apiCall(t, app, cookie, csrf, http.MethodPatch, "/api/finance/entries/"+listed.Entries[0].ID, `{"amount":"15.40"}`); response.Code != http.StatusOK {
		t.Fatalf("patch: status=%d body=%s", response.Code, response.Body.String())
	}
	raw, _ = tool.Execute(nigel, json.RawMessage(`{"action":"summary"}`))
	if !strings.Contains(string(raw), "SGD 19.60 (2 entries)") {
		t.Fatalf("the tool's summary missed the panel's correction: %s", raw)
	}
}

func TestFinanceDisabledMountsNothingAndListsNoFeature(t *testing.T) {
	app := newFinanceApp(t, false)
	cookie := loginThrough(t, app, "owner@example.com", "operator-password")
	if cookie == nil {
		t.Fatal("could not sign in")
	}
	_, csrf := sessionOf(t, app, cookie)
	if features := sessionFeatures(t, app, cookie); len(features) != 0 {
		t.Fatalf("features = %v, want none", features)
	}
	for _, c := range [][2]string{
		{http.MethodGet, "/api/finance/entries"}, {http.MethodGet, "/api/finance/summary"},
		{http.MethodPost, "/api/finance/entries"},
	} {
		if response := apiCall(t, app, cookie, csrf, c[0], c[1], `{}`); response.Code == http.StatusOK {
			t.Errorf("%s %s answered 200 with finance disabled", c[0], c[1])
		}
	}
	if _, ok := app.tools.Lookup("finance"); ok {
		t.Fatal("the finance tool is registered while disabled")
	}
	// The setting itself is always reachable: it is how the owner turns the
	// capability on.
	if response := apiCall(t, app, cookie, csrf, http.MethodGet, "/api/config/finance", ""); response.Code != http.StatusOK {
		t.Fatalf("config/finance: status=%d body=%s", response.Code, response.Body.String())
	}
}
