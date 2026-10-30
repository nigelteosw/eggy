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

// ErrFinanceInvalid wraps every refusal of a finance request's contents -- an
// unreadable amount, an unsupported currency, a bad date -- so the panel can
// answer 400 for what the caller got wrong and 500 for what failed here.
var ErrFinanceInvalid = errors.New("invalid finance input")

// The kinds an entry may take. The kind gives the direction of money, so an
// amount is always positive: expense is money spent, income is money received,
// invest is money moved into investments, and divest is money taken back out
// of them (a sale, a redemption, a withdrawal).
const (
	FinanceKindExpense = "expense"
	FinanceKindIncome  = "income"
	FinanceKindInvest  = "invest"
	FinanceKindDivest  = "divest"
)

// FinanceKinds is every kind, in the order the panel offers them.
var FinanceKinds = []string{FinanceKindExpense, FinanceKindIncome, FinanceKindInvest, FinanceKindDivest}

// FinanceEntry is one logged movement of money. Every entry belongs to the
// account on the context it was written under; the owner is never a field
// here, because nothing downstream may name which account a record is for.
//
// AmountMinor is an integer count of the currency's minor unit (cents, or yen
// for JPY, which has none) and is always positive: Kind gives the direction.
type FinanceEntry struct {
	ID          string
	Kind        string
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

// The keys a listing may sort by. The store maps each to a fixed column and
// never builds an ORDER BY from caller text.
const (
	FinanceSortDate     = "date"
	FinanceSortAmount   = "amount"
	FinanceSortCategory = "category"
	FinanceSortMerchant = "merchant"
)

// FinanceFilter bounds and orders a listing. From and To are inclusive
// YYYY-MM-DD dates and either may be empty; Limit zero means no limit, and
// Offset applies only with a limit.
//
// Query is a case-insensitive text match against merchant, category and note.
// Sort is one of the FinanceSort keys and Order is "asc" or "desc"; the
// service resolves empty values to each key's default before the store sees
// them. Ties always fall back to newest first, so an order is never unstable.
type FinanceFilter struct {
	From     string
	To       string
	Kind     string
	Category string
	Query    string
	Sort     string
	Order    string
	Limit    int
	// Offset skips that many matching entries before Limit applies, for
	// paging through a long month. It counts in the filter's own order.
	Offset int
}

// FinanceCategoryTotal, FinanceDayTotal and FinanceMonthTotal are sums the
// store computes in SQL, so a month of entries is never loaded into memory to
// be added up. Sums are per currency and per kind because nothing here converts
// between currencies, and an investment is not spending.
type FinanceCategoryTotal struct {
	Currency    string
	Kind        string
	Category    string
	AmountMinor int64
	Count       int
}

type FinanceDayTotal struct {
	Currency    string
	Kind        string
	Day         string
	AmountMinor int64
}

type FinanceMonthTotal struct {
	Currency    string
	Kind        string
	Month       string // YYYY-MM
	AmountMinor int64
}

type FinanceTotals struct {
	ByCategory []FinanceCategoryTotal
	ByDay      []FinanceDayTotal
}

// FinanceCategory is a category the account has used, with the colour chosen
// for it. Color is empty until one is chosen; the service supplies a default.
type FinanceCategory struct {
	Name  string
	Count int
	Color string
}

// FinanceFlow is one currency's money over a period, in minor units. Inflow is
// income plus divest; Outflow is expense; Invested is invest. Divested is kept
// on its own so net invested can be shown, and DailyAverage is the spending
// per day over the days of the period that have happened.
type FinanceFlow struct {
	Currency     string
	Inflow       int64
	Outflow      int64
	Invested     int64
	Divested     int64
	Count        int
	DailyAverage int64
}

// Net is cash in minus cash out: what the period left over after spending and
// investing.
func (f FinanceFlow) Net() int64 { return f.Inflow - f.Outflow - f.Invested }

// NetInvested is what the period added to investments, after anything taken out.
func (f FinanceFlow) NetInvested() int64 { return f.Invested - f.Divested }

// FinanceTrendPoint is one month of one currency's flows, for the trend chart.
type FinanceTrendPoint struct {
	Month    string
	Currency string
	Inflow   int64
	Outflow  int64
	Invested int64
}

// FinanceCategoryLine is one spending category in the month, with what it cost
// the month before so the page can show the movement.
type FinanceCategoryLine struct {
	Currency string
	Category string
	Color    string
	Amount   int64
	Previous int64
	Count    int
}

// FinanceReport is everything the Finance page draws for one month. Money is
// minor units here; the panel formats it, and nothing downstream does arithmetic
// on it. Flows and Previous line up by currency, the owner's default first.
type FinanceReport struct {
	Month      string
	From       string
	To         string
	Flows      []FinanceFlow
	Previous   []FinanceFlow
	Trend      []FinanceTrendPoint
	Categories []FinanceCategoryLine
	Days       []FinanceDayTotal
	AllCats    []FinanceCategory
	Insights   []string
}

// FinanceInput is a new entry as a person or the model states it: every field
// is text, because "14.50" must be parsed against its currency rather than
// pass through a float on the way in. Blank Kind, Currency, Date and Source take
// "expense", the deployment default, today, and "chat".
type FinanceInput struct {
	Amount, Kind, Currency, Date, Category, Merchant, Note, Source string
}

// FinancePatch changes only the fields that are set. A nil field is left alone.
// Source is deliberately absent: how an entry arrived is history, not a field
// to edit.
type FinancePatch struct {
	Amount, Kind, Currency, Date, Category, Merchant, Note *string
}

// FinanceStore is the persistence the finance extension needs. Every method acts
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
	// FinanceMonths sums each month in from..to per currency and kind, for the trend.
	FinanceMonths(ctx context.Context, from, to string) ([]FinanceMonthTotal, error)
	// FinanceCategories lists the account's categories with their entry counts
	// and chosen colours.
	FinanceCategories(ctx context.Context) ([]FinanceCategory, error)
	// SetFinanceCategoryColor records a category's colour, creating or replacing it.
	SetFinanceCategoryColor(ctx context.Context, name, color string) error
	// RenameFinanceCategory moves every entry in one category to another. When
	// the target already exists the two merge, and the target's colour is kept.
	RenameFinanceCategory(ctx context.Context, from, to string) error
}
