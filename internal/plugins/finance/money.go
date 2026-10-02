package finance

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// exponents is each supported currency's minor-unit exponent: the number of
// decimal places in its smallest unit. config.FinanceCurrencies owns which
// currencies exist; this owns how each is counted, and a test fails when a
// code is added there without a row here.
var exponents = map[string]int{
	"USD": 2, "EUR": 2, "JPY": 0, "GBP": 2, "CNY": 2,
	"AUD": 2, "CAD": 2, "CHF": 2, "HKD": 2, "SGD": 2,
}

// maxMajorDigits keeps every amount, and any sum of them, far inside int64.
const maxMajorDigits = 13

var (
	plainDigits   = regexp.MustCompile(`^\d+$`)
	groupedDigits = regexp.MustCompile(`^\d{1,3}(,\d{3})+$`)
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ports.ErrFinanceInvalid, fmt.Sprintf(format, args...))
}

// ParseAmount reads text such as "14.50" or "1,234.56" as a count of the
// currency's minor unit. It parses digits and never a float, and it refuses
// anything it could misread rather than guessing: a currency symbol (the
// currency is its own field, because ¥ is both JPY and CNY and $ is five of the
// ten), a sign, zero, more decimals than the currency has, or an amount too
// large to add up safely.
func ParseAmount(text, currency string) (int64, error) {
	exponent, ok := exponents[currency]
	if !ok {
		return 0, invalid("currency %q is not supported", currency)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, invalid("amount is required")
	}
	if strings.HasPrefix(text, "-") {
		return 0, invalid("amount %q must be positive; entries record spending", text)
	}
	if strings.Trim(text, "0123456789,.") != "" {
		return 0, invalid("amount %q must be digits only, like 14.50; give the currency in the currency field", text)
	}
	major, fraction, hasPoint := strings.Cut(text, ".")
	if hasPoint && (major == "" || fraction == "" || strings.ContainsAny(fraction, ",.")) {
		return 0, invalid("amount %q is not a number like 14.50", text)
	}
	if hasPoint && len(fraction) > exponent {
		if exponent == 0 {
			return 0, invalid("%s has no decimal places, so %q is not an amount", currency, text)
		}
		return 0, invalid("%s has %d decimal places, so %q has too many", currency, exponent, text)
	}
	if !plainDigits.MatchString(major) && !groupedDigits.MatchString(major) {
		return 0, invalid("amount %q is not a number like 14.50", text)
	}
	major = strings.ReplaceAll(major, ",", "")
	if len(strings.TrimLeft(major, "0")) > maxMajorDigits {
		return 0, invalid("amount %q is too large", text)
	}
	whole, err := strconv.ParseInt(major, 10, 64)
	if err != nil {
		return 0, invalid("amount %q is too large", text)
	}
	minor := whole
	for range exponent {
		minor *= 10
	}
	if exponent > 0 && fraction != "" {
		padded := fraction + strings.Repeat("0", exponent-len(fraction))
		cents, err := strconv.ParseInt(padded, 10, 64)
		if err != nil {
			return 0, invalid("amount %q is not a number like 14.50", text)
		}
		minor += cents
	}
	if minor <= 0 {
		return 0, invalid("amount %q must be greater than zero", text)
	}
	return minor, nil
}

// FormatAmount is ParseAmount's inverse: 1450 SGD is "14.50", 1200 JPY is
// "1200". Callers never do arithmetic on minor units to show them.
func FormatAmount(minor int64, currency string) string {
	exponent := exponents[currency]
	if exponent == 0 {
		return strconv.FormatInt(minor, 10)
	}
	unit := int64(1)
	for range exponent {
		unit *= 10
	}
	return fmt.Sprintf("%d.%0*d", minor/unit, exponent, minor%unit)
}
