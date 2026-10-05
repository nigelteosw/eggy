// Package finance is Eggy's expense tracker: a Service that owns validation and
// the one write path, and a tool that lets the model use it. The web panel is a
// second caller of the same Service, so there is one place that decides what a
// valid entry is.
//
// It is the first feature plugin. It is wired at compile time by
// internal/bootstrap, only when finance.enabled is set; when it is not, none of
// this is constructed and nothing about it reaches a prompt, a route or a
// goroutine.
package finance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nigelteosw/eggy/internal/ports"
)

const (
	dateLayout = "2006-01-02"
	// maxList bounds a listing whatever the caller asks for; the tool asks for
	// less. The panel pages by month, which is well inside it.
	maxList = 200

	maxCategory = 40
	maxMerchant = 80
	maxNote     = 280
)

var monthPattern = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

var sources = []string{"chat", "photo", "panel"}

// Options configures a Service. Currencies is the list bootstrap takes from
// config.FinanceCurrencies; this package never keeps a second copy, it only
// knows how to count each code (money.go).
type Options struct {
	Currencies []string
	Default    string
	// Location is the owner's timezone: "today" is a calendar date there, not
	// in UTC, or an entry made after midnight local time lands on yesterday.
	Location *time.Location
	Now      func() time.Time
	// NewID is overridable for tests; production uses random hex.
	NewID func() string
}

// Service is the one write path to the finance store. The acting account is
// always on the context, set at a trusted ingress; nothing here accepts an
// account from a caller.
type Service struct {
	store      ports.FinanceStore
	opts       Options
	currencies []string
}

// New checks its options up front, so a currency the plugin cannot count fails
// at startup rather than at someone's first entry.
func New(store ports.FinanceStore, opts Options) (*Service, error) {
	if store == nil {
		return nil, errors.New("finance: a store is required")
	}
	if opts.Location == nil || opts.Now == nil {
		return nil, errors.New("finance: a location and a clock are required")
	}
	for _, code := range opts.Currencies {
		if _, ok := exponents[code]; !ok {
			return nil, fmt.Errorf("finance: currency %s is supported by config but has no minor unit in money.go", code)
		}
	}
	if !slices.Contains(opts.Currencies, opts.Default) {
		return nil, fmt.Errorf("finance: default currency %q is not in the supported list", opts.Default)
	}
	if opts.NewID == nil {
		opts.NewID = randomID
	}
	return &Service{store: store, opts: opts, currencies: slices.Clone(opts.Currencies)}, nil
}

func randomID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand failing means the platform is broken; an id that is
		// guessable or repeated is worse than stopping.
		panic(fmt.Sprintf("finance: reading random bytes: %v", err))
	}
	return hex.EncodeToString(raw[:])
}

// Currencies is a copy, so a caller cannot reorder the list for everyone.
func (s *Service) Currencies() []string { return slices.Clone(s.currencies) }

func (s *Service) DefaultCurrency() string { return s.opts.Default }

// Format renders an amount the way ParseAmount reads it.
func (s *Service) Format(minor int64, currency string) string { return FormatAmount(minor, currency) }

func (s *Service) today() string { return s.opts.Now().In(s.opts.Location).Format(dateLayout) }

func (s *Service) currency(code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return s.opts.Default, nil
	}
	if !slices.Contains(s.currencies, code) {
		return "", invalid("currency %q is not supported; use one of %s", code, strings.Join(s.currencies, ", "))
	}
	return code, nil
}

func parseDate(text string) (string, error) {
	text = strings.TrimSpace(text)
	if _, err := time.Parse(dateLayout, text); err != nil {
		return "", invalid("date %q must be a real date like 2026-10-02", text)
	}
	return text, nil
}

func cleanText(field, value string, limit int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", invalid("%s is required", field)
	}
	if utf8.RuneCountInString(value) > limit {
		return "", invalid("%s is longer than %d characters", field, limit)
	}
	return value, nil
}

func cleanCategory(value string) (string, error) {
	value, err := cleanText("category", value, maxCategory, true)
	return strings.ToLower(value), err
}

// Log validates and records a new entry for the acting account.
func (s *Service) Log(ctx context.Context, in ports.FinanceInput) (ports.FinanceEntry, error) {
	currency, err := s.currency(in.Currency)
	if err != nil {
		return ports.FinanceEntry{}, err
	}
	minor, err := ParseAmount(in.Amount, currency)
	if err != nil {
		return ports.FinanceEntry{}, err
	}
	date := s.today()
	if strings.TrimSpace(in.Date) != "" {
		if date, err = parseDate(in.Date); err != nil {
			return ports.FinanceEntry{}, err
		}
	}
	category, err := cleanCategory(in.Category)
	if err != nil {
		return ports.FinanceEntry{}, err
	}
	merchant, err := cleanText("merchant", in.Merchant, maxMerchant, false)
	if err != nil {
		return ports.FinanceEntry{}, err
	}
	note, err := cleanText("note", in.Note, maxNote, false)
	if err != nil {
		return ports.FinanceEntry{}, err
	}
	source := strings.ToLower(strings.TrimSpace(in.Source))
	if source == "" {
		source = "chat"
	}
	if !slices.Contains(sources, source) {
		return ports.FinanceEntry{}, invalid("source %q must be one of %s", source, strings.Join(sources, ", "))
	}
	now := s.opts.Now()
	entry := ports.FinanceEntry{
		ID: s.opts.NewID(), OccurredOn: date, AmountMinor: minor, Currency: currency,
		Category: category, Merchant: merchant, Note: note, Source: source,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateFinanceEntry(ctx, entry); err != nil {
		return ports.FinanceEntry{}, err
	}
	return entry, nil
}

// Update changes only the fields set in the patch.
//
// Changing the currency requires restating the amount. The stored number is a
// count of minor units, and 1450 is 14.50 in SGD but 1450 yen: carrying it
// across would silently change what was spent.
func (s *Service) Update(ctx context.Context, id string, patch ports.FinancePatch) (ports.FinanceEntry, error) {
	if patch == (ports.FinancePatch{}) {
		return ports.FinanceEntry{}, invalid("nothing to change; give at least one field")
	}
	return s.store.UpdateFinanceEntry(ctx, strings.TrimSpace(id), func(entry *ports.FinanceEntry) error {
		currency := entry.Currency
		if patch.Currency != nil {
			code, err := s.currency(*patch.Currency)
			if err != nil {
				return err
			}
			if code != entry.Currency && patch.Amount == nil {
				return invalid("changing the currency from %s to %s needs the amount restated in %s", entry.Currency, code, code)
			}
			currency = code
		}
		if patch.Amount != nil {
			minor, err := ParseAmount(*patch.Amount, currency)
			if err != nil {
				return err
			}
			entry.AmountMinor = minor
		}
		entry.Currency = currency
		if patch.Date != nil {
			date, err := parseDate(*patch.Date)
			if err != nil {
				return err
			}
			entry.OccurredOn = date
		}
		if patch.Category != nil {
			category, err := cleanCategory(*patch.Category)
			if err != nil {
				return err
			}
			entry.Category = category
		}
		if patch.Merchant != nil {
			merchant, err := cleanText("merchant", *patch.Merchant, maxMerchant, false)
			if err != nil {
				return err
			}
			entry.Merchant = merchant
		}
		if patch.Note != nil {
			note, err := cleanText("note", *patch.Note, maxNote, false)
			if err != nil {
				return err
			}
			entry.Note = note
		}
		entry.UpdatedAt = s.opts.Now()
		return nil
	})
}

// Delete removes an entry and returns it, so the reply can show what went.
func (s *Service) Delete(ctx context.Context, id string) (ports.FinanceEntry, error) {
	return s.store.DeleteFinanceEntry(ctx, strings.TrimSpace(id))
}

// List returns the newest entries first and the count before the limit. With
// no dates it covers the current month.
func (s *Service) List(ctx context.Context, filter ports.FinanceFilter) ([]ports.FinanceEntry, int, error) {
	from, to, err := s.Range(filter.From, filter.To)
	if err != nil {
		return nil, 0, err
	}
	filter.From, filter.To = from, to
	filter.Category = strings.ToLower(strings.TrimSpace(filter.Category))
	if filter.Limit <= 0 || filter.Limit > maxList {
		filter.Limit = maxList
	}
	return s.store.ListFinanceEntries(ctx, filter)
}

// Summary sums the range per currency, by category and by day.
func (s *Service) Summary(ctx context.Context, from, to string) (ports.FinanceTotals, error) {
	from, to, err := s.Range(from, to)
	if err != nil {
		return ports.FinanceTotals{}, err
	}
	return s.store.FinanceTotals(ctx, from, to)
}

// Range validates an inclusive date range. With neither end it is the current
// month; with one end the other is left open.
func (s *Service) Range(from, to string) (string, string, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" && to == "" {
		return s.MonthBounds(s.today()[:7])
	}
	var err error
	if from != "" {
		if from, err = parseDate(from); err != nil {
			return "", "", err
		}
	}
	if to != "" {
		if to, err = parseDate(to); err != nil {
			return "", "", err
		}
	}
	if from != "" && to != "" && from > to {
		return "", "", invalid("from %s is after to %s", from, to)
	}
	return from, to, nil
}

// MonthBounds returns the first and last day of a YYYY-MM month.
func (s *Service) MonthBounds(month string) (string, string, error) {
	month = strings.TrimSpace(month)
	if !monthPattern.MatchString(month) {
		return "", "", invalid("month %q must look like 2026-10", month)
	}
	first, err := time.Parse(dateLayout, month+"-01")
	if err != nil {
		return "", "", invalid("month %q must look like 2026-10", month)
	}
	return first.Format(dateLayout), first.AddDate(0, 1, -1).Format(dateLayout), nil
}
