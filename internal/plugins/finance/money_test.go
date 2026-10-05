package finance

import (
	"errors"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestParseAmount(t *testing.T) {
	for _, c := range []struct {
		text, currency string
		want           int64
	}{
		{"14.50", "SGD", 1450},
		{"14.5", "SGD", 1450},
		{"14", "SGD", 1400},
		{"0.05", "USD", 5},
		{"1,234.56", "SGD", 123456},
		{" 8.20 ", "EUR", 820},
		{"1200", "JPY", 1200},
		{"1,200", "JPY", 1200},
	} {
		got, err := ParseAmount(c.text, c.currency)
		if err != nil || got != c.want {
			t.Errorf("ParseAmount(%q, %s) = %d, %v; want %d", c.text, c.currency, got, err, c.want)
		}
	}
}

func TestParseAmountRefusesWhatItCouldMisread(t *testing.T) {
	for _, c := range []struct{ text, currency, why string }{
		{"$14", "SGD", "a symbol: ¥ is JPY and CNY, $ is five currencies, so currency is its own field"},
		{"¥1200", "JPY", "a symbol"},
		{"14 SGD", "SGD", "a code in the amount"},
		{"-3", "SGD", "negative: v1 tracks spending only"},
		{"+3", "SGD", "a sign"},
		{"0", "SGD", "zero"},
		{"0.00", "SGD", "zero"},
		{"", "SGD", "empty"},
		{"abc", "SGD", "not a number"},
		{"1.234", "SGD", "three decimals for a two-decimal currency"},
		{"1200.5", "JPY", "JPY has no minor unit"},
		{"1200.00", "JPY", "JPY has no minor unit"},
		{"14.", "SGD", "a dangling point"},
		{".50", "SGD", "no digits before the point"},
		{"1.2.3", "SGD", "two points"},
		{"1,2,3", "SGD", "commas that are not thousands"},
		{"1.234,56", "SGD", "a comma after the point"},
		{"99999999999999999999", "SGD", "overflow"},
		{"14.50", "XYZ", "an unsupported currency"},
	} {
		if got, err := ParseAmount(c.text, c.currency); err == nil {
			t.Errorf("ParseAmount(%q, %s) = %d, want an error (%s)", c.text, c.currency, got, c.why)
		} else if !errors.Is(err, ports.ErrFinanceInvalid) {
			t.Errorf("ParseAmount(%q, %s) error %v does not wrap ErrFinanceInvalid", c.text, c.currency, err)
		}
	}
}

func TestFormatAmountRoundTrips(t *testing.T) {
	for _, c := range []struct {
		minor    int64
		currency string
		want     string
	}{
		{1450, "SGD", "14.50"},
		{5, "USD", "0.05"},
		{100, "EUR", "1.00"},
		{123456, "SGD", "1234.56"},
		{1200, "JPY", "1200"},
	} {
		if got := FormatAmount(c.minor, c.currency); got != c.want {
			t.Errorf("FormatAmount(%d, %s) = %q, want %q", c.minor, c.currency, got, c.want)
		}
		back, err := ParseAmount(c.want, c.currency)
		if err != nil || back != c.minor {
			t.Errorf("ParseAmount(%q, %s) = %d, %v; want %d", c.want, c.currency, back, err, c.minor)
		}
	}
}

// config.FinanceCurrencies is the one list; this package owns each code's
// minor unit. A code added there without one must fail here, not at runtime.
func TestEverySupportedCurrencyHasAMinorUnit(t *testing.T) {
	for _, code := range config.FinanceCurrencies {
		if _, ok := exponents[code]; !ok {
			t.Errorf("%s is in config.FinanceCurrencies but has no exponent in money.go", code)
		}
	}
	if exponents["JPY"] != 0 {
		t.Error("JPY has no minor unit")
	}
	for _, code := range []string{"USD", "EUR", "GBP", "CNY", "AUD", "CAD", "CHF", "HKD", "SGD"} {
		if exponents[code] != 2 {
			t.Errorf("%s exponent = %d, want 2", code, exponents[code])
		}
	}
}
