package bootstrap

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/turns"
	sqlitestore "github.com/nigelteosw/eggy/internal/storage/sqlite"
)

func newFinanceTestStore(t *testing.T) *sqlitestore.Store {
	t.Helper()
	store, err := sqlitestore.Open(filepath.Join(t.TempDir(), "eggy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func financeConfig(enabled bool) config.Config {
	return config.Config{Finance: config.FinanceConfig{Enabled: enabled, Currency: "SGD"}}
}

// A disabled capability must cost nothing: no service, no tool schema in any
// model call, and no touch of the store (it is nil here, so a touch would
// panic).
func TestFinanceDisabledBuildsNothing(t *testing.T) {
	service, tools, err := newFinance(financeConfig(false), nil, time.UTC, time.Now)
	if err != nil || service != nil || len(tools) != 0 {
		t.Fatalf("disabled finance built service=%v tools=%d err=%v", service, len(tools), err)
	}
}

func TestFinanceEnabledBuildsTheServiceAndOneTool(t *testing.T) {
	service, tools, err := newFinance(financeConfig(true), newFinanceTestStore(t), time.UTC, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if service == nil || len(tools) != 1 || tools[0].Definition().Name != "finance" {
		t.Fatalf("service=%v tools=%d", service, len(tools))
	}
	if service.DefaultCurrency() != "SGD" || !slices.Equal(service.Currencies(), config.FinanceCurrencies) {
		t.Fatalf("default=%s currencies=%v", service.DefaultCurrency(), service.Currencies())
	}
}

func TestFinanceRefusesADefaultOutsideTheSupportedList(t *testing.T) {
	cfg := financeConfig(true)
	cfg.Finance.Currency = "XYZ"
	if _, _, err := newFinance(cfg, newFinanceTestStore(t), time.UTC, time.Now); err == nil {
		t.Fatal("a default currency outside the list built a service")
	}
}

func financeToolListed(t *testing.T, enabled bool) (description string, schema json.RawMessage, found bool) {
	t.Helper()
	cfg := appTestConfig(t.TempDir())
	cfg.Finance = config.FinanceConfig{Enabled: enabled, Currency: "SGD"}
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, listing := range app.tools.Catalog() {
		if listing.Definition.Name == "finance" {
			return listing.Definition.Description, listing.Definition.Schema, true
		}
	}
	return "", nil, false
}

func TestNewAppRegistersFinanceOnlyWhenEnabled(t *testing.T) {
	if _, _, found := financeToolListed(t, false); found {
		t.Fatal("finance is in the tool catalog while disabled")
	}
	description, schema, found := financeToolListed(t, true)
	if !found {
		t.Fatal("finance is missing from the tool catalog while enabled")
	}
	// Internal, so normal mode does not ask per logged expense: the gate
	// wrapper must not have added its approval notice.
	if strings.Contains(description, "requires the owner's approval") {
		t.Fatalf("finance carries an approval notice in normal mode: %q", description)
	}
	if len(schema) == 0 {
		t.Fatal("finance registered without a schema")
	}
}

// Unprompted turns run on an explicit allowlist, so the tool is off it unless
// someone adds it: a heartbeat or schedule cannot log, edit or delete.
func TestUnpromptedTurnsCannotReachFinance(t *testing.T) {
	if turns.ReadOnlyTools().AllowedTools["finance"] {
		t.Fatal("the read-only allowlist names finance; an unprompted turn could mutate an owner's records")
	}
}
