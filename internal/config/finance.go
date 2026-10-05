package config

import (
	"fmt"
	"slices"
	"strings"
)

// FinanceConfig switches the finance plugin on and names the currency an entry
// takes when the owner does not say one. It is startup configuration: a change
// applies on restart, because a capability that is not configured registers no
// tool, mounts no route and costs no prompt bytes.
type FinanceConfig struct {
	// Enabled is a plain bool, not a pointer: unlike tracing, absent and false
	// mean the same thing, and the capability is off until chosen.
	Enabled  bool   `yaml:"enabled"`
	Currency string `yaml:"currency,omitempty"`
}

// FinanceCurrencies is the one list of currencies an entry may carry, in the
// order the panel offers them. Validation, the tool's schema enum, the panel's
// dropdown and the service all read this one value rather than keeping copies:
// config validates the default, and config may not import the plugin, so the
// list lives here and the plugin receives it from bootstrap.
//
// The plugin keeps each code's minor-unit exponent beside its parser. A code
// added here without one is caught by that package's test.
var FinanceCurrencies = []string{"USD", "EUR", "JPY", "GBP", "CNY", "AUD", "CAD", "CHF", "HKD", "SGD"}

// defaultFinanceCurrency is where a deployment starts. Entries still carry
// their own code, so a trip abroad can be logged in local currency.
const defaultFinanceCurrency = "SGD"

// normalizeFinanceCurrency canonicalizes once, in applyDefaults, for the same
// reason Google product names are: Validate accepts the owner's casing, and a
// second reader matching exactly would otherwise disagree with it.
func normalizeFinanceCurrency(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return defaultFinanceCurrency
	}
	return code
}

func (c Config) validateFinance() error {
	// Validate is reachable without applyDefaults (a freshly generated config
	// is checked before it is loaded back), so empty is the default rather
	// than a rejection.
	if c.Finance.Currency != "" && !slices.Contains(FinanceCurrencies, c.Finance.Currency) {
		return fmt.Errorf("finance.currency %q must be one of: %s", c.Finance.Currency, strings.Join(FinanceCurrencies, ", "))
	}
	return nil
}

// SetFinance saves the finance section under the config lock with the same
// validation as every other write. A blank currency asks for the default.
func SetFinance(path string, enabled bool, currency string) error {
	return mutate(path, func(cfg *Config) error {
		cfg.Finance.Enabled = enabled
		cfg.Finance.Currency = currency
		// applyDefaults canonicalizes the code before mutate's Validate
		// checks it, so what is written is what was checked.
		return cfg.applyDefaults()
	})
}
