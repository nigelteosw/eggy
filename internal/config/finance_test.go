package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFinanceDefaultsToSGDAndOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Finance.Enabled {
		t.Fatal("finance must be off until the owner turns it on")
	}
	if cfg.Finance.Currency != "SGD" {
		t.Fatalf("default currency = %q, want SGD", cfg.Finance.Currency)
	}
}

func TestFinanceCurrenciesAreTheTenSupported(t *testing.T) {
	want := []string{"USD", "EUR", "JPY", "GBP", "CNY", "AUD", "CAD", "CHF", "HKD", "SGD"}
	if !slices.Equal(FinanceCurrencies, want) {
		t.Fatalf("FinanceCurrencies = %v, want %v", FinanceCurrencies, want)
	}
}

func TestSetFinanceSavesAndCanonicalizesTheCurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetFinance(path, true, " usd "); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Finance.Enabled || cfg.Finance.Currency != "USD" {
		t.Fatalf("saved finance = %+v", cfg.Finance)
	}
	// Off is written out, so the file says what the owner chose.
	if err := SetFinance(path, false, "USD"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "enabled: false") {
		t.Fatalf("off must be written out, not left absent:\n%s", body)
	}
}

func TestSetFinanceBlankCurrencyTakesTheDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte(validConfig()), 0o600)
	if err := SetFinance(path, true, ""); err != nil {
		t.Fatal(err)
	}
	cfg, _ := LoadDocument(path)
	if cfg.Finance.Currency != "SGD" {
		t.Fatalf("currency = %q, want SGD", cfg.Finance.Currency)
	}
}

func TestSetFinanceRefusesAnUnsupportedCurrencyAndLeavesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte(validConfig()), 0o600)
	before, _ := os.ReadFile(path)
	for _, code := range []string{"XYZ", "US", "DOLLAR", "$"} {
		if err := SetFinance(path, true, code); err == nil {
			t.Fatalf("accepted unsupported currency %q", code)
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("a refused save changed the file")
	}
}

func TestFinanceRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := validConfig() + "\nfinance:\n  enabld: true\n"
	os.WriteFile(path, []byte(body), 0o600)
	if _, err := LoadDocument(path); err == nil || !strings.Contains(err.Error(), "enabld") {
		t.Fatalf("a typo under finance: was accepted: %v", err)
	}
}

func TestValidateRefusesAnUnsupportedFinanceCurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := validConfig() + "\nfinance:\n  enabled: true\n  currency: XYZ\n"
	os.WriteFile(path, []byte(body), 0o600)
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "finance.currency") {
		t.Fatalf("Validate = %v, want a finance.currency error", err)
	}
}
