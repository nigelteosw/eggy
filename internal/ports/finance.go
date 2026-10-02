package ports

import (
	"context"
	"errors"
	"time"
)

// ErrFinanceEntryNotFound is returned for an id that does not exist for the
// acting account. Another account's entry is indistinguishable from a missing
// one on purpose: a caller must not be able to probe which ids exist.
var ErrFinanceEntryNotFound = errors.New("finance entry not found")

// FinanceEntry is one logged expense. Every entry belongs to the account on
// the context it was written under; the owner is never a field here, because
// nothing downstream may name which account a record is for.
//
// AmountMinor is an integer count of the currency's minor unit (cents, or yen
// for JPY, which has none) and is always positive: v1 tracks spending only.
type FinanceEntry struct {
	ID          string
	OccurredOn  string // YYYY-MM-DD, a calendar date in the owner's timezone
	AmountMinor int64
	Currency    string // upper-case ISO 4217
	Category    string // lower-case free text
	Merchant    string
	Note        string
	Source      string // "chat", "photo" or "panel"
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// FinanceFilter bounds a listing. From and To are inclusive YYYY-MM-DD dates
// and either may be empty; Limit zero means no limit.
type FinanceFilter struct {
	From     string
	To       string
	Category string
	Limit    int
}

// FinanceCategoryTotal and FinanceDayTotal are sums the store computes in SQL,
// so a month of entries is never loaded into memory to be added up. Sums are
// per currency because nothing here converts between them.
type FinanceCategoryTotal struct {
	Currency    string
	Category    string
	AmountMinor int64
	Count       int
}

type FinanceDayTotal struct {
	Currency    string
	Day         string
	AmountMinor int64
}

type FinanceTotals struct {
	ByCategory []FinanceCategoryTotal
	ByDay      []FinanceDayTotal
}

// FinanceStore is the persistence the finance plugin needs. Every method acts
// as the account on the context and fails with ErrNoPrincipal without one.
type FinanceStore interface {
	CreateFinanceEntry(ctx context.Context, entry FinanceEntry) error
	// UpdateFinanceEntry reads the entry, applies mutate, and writes the result
	// in one transaction, so two edits cannot interleave. It returns the entry
	// as written.
	UpdateFinanceEntry(ctx context.Context, id string, mutate func(*FinanceEntry) error) (FinanceEntry, error)
	// DeleteFinanceEntry removes the entry and returns it, so a reply can show
	// what was deleted.
	DeleteFinanceEntry(ctx context.Context, id string) (FinanceEntry, error)
	// ListFinanceEntries returns the newest entries first and the count of all
	// entries matching the filter before Limit applied.
	ListFinanceEntries(ctx context.Context, filter FinanceFilter) ([]FinanceEntry, int, error)
	FinanceTotals(ctx context.Context, from, to string) (FinanceTotals, error)
}
