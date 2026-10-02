package finance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// fakeStore is an in-memory ports.FinanceStore. It scopes by principal the way
// the real store does, so a test that forgets to put an account on the context
// fails here as it would in production.
type fakeStore struct {
	entries map[string]map[string]ports.FinanceEntry // account -> id -> entry
	order   []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{entries: map[string]map[string]ports.FinanceEntry{}}
}

func (f *fakeStore) account(ctx context.Context) (map[string]ports.FinanceEntry, error) {
	p, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if f.entries[p.AccountID] == nil {
		f.entries[p.AccountID] = map[string]ports.FinanceEntry{}
	}
	return f.entries[p.AccountID], nil
}

func (f *fakeStore) CreateFinanceEntry(ctx context.Context, e ports.FinanceEntry) error {
	m, err := f.account(ctx)
	if err != nil {
		return err
	}
	m[e.ID] = e
	f.order = append(f.order, e.ID)
	return nil
}

func (f *fakeStore) UpdateFinanceEntry(ctx context.Context, id string, mutate func(*ports.FinanceEntry) error) (ports.FinanceEntry, error) {
	m, err := f.account(ctx)
	if err != nil {
		return ports.FinanceEntry{}, err
	}
	e, ok := m[id]
	if !ok {
		return ports.FinanceEntry{}, ports.ErrFinanceEntryNotFound
	}
	if err := mutate(&e); err != nil {
		return ports.FinanceEntry{}, err
	}
	m[id] = e
	return e, nil
}

func (f *fakeStore) DeleteFinanceEntry(ctx context.Context, id string) (ports.FinanceEntry, error) {
	m, err := f.account(ctx)
	if err != nil {
		return ports.FinanceEntry{}, err
	}
	e, ok := m[id]
	if !ok {
		return ports.FinanceEntry{}, ports.ErrFinanceEntryNotFound
	}
	delete(m, id)
	return e, nil
}

func (f *fakeStore) ListFinanceEntries(ctx context.Context, filter ports.FinanceFilter) ([]ports.FinanceEntry, int, error) {
	m, err := f.account(ctx)
	if err != nil {
		return nil, 0, err
	}
	var out []ports.FinanceEntry
	for _, id := range f.order {
		e, ok := m[id]
		if !ok {
			continue
		}
		if (filter.From != "" && e.OccurredOn < filter.From) || (filter.To != "" && e.OccurredOn > filter.To) || (filter.Category != "" && e.Category != filter.Category) {
			continue
		}
		out = append(out, e)
	}
	total := len(out)
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, total, nil
}

func (f *fakeStore) FinanceTotals(ctx context.Context, from, to string) (ports.FinanceTotals, error) {
	entries, _, err := f.ListFinanceEntries(ctx, ports.FinanceFilter{From: from, To: to})
	if err != nil {
		return ports.FinanceTotals{}, err
	}
	var totals ports.FinanceTotals
	for _, e := range entries {
		totals.ByCategory = append(totals.ByCategory, ports.FinanceCategoryTotal{Currency: e.Currency, Category: e.Category, AmountMinor: e.AmountMinor, Count: 1})
	}
	return totals, nil
}

func asAccount(id string) context.Context {
	return ports.WithPrincipal(context.Background(), ports.Principal{AccountID: id})
}

func singapore(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func newService(t *testing.T, now time.Time) (*Service, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	n := 0
	svc, err := New(store, Options{
		Currencies: config.FinanceCurrencies,
		Default:    "SGD",
		Location:   singapore(t),
		Now:        func() time.Time { return now },
		NewID:      func() string { n++; return fmt.Sprintf("id%d", n) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, store
}

func TestNewAcceptsEveryConfiguredCurrency(t *testing.T) {
	if _, err := New(newFakeStore(), Options{Currencies: config.FinanceCurrencies, Default: "SGD", Location: time.UTC, Now: time.Now}); err != nil {
		t.Fatal(err)
	}
}

func TestNewRefusesACurrencyItCannotCount(t *testing.T) {
	_, err := New(newFakeStore(), Options{Currencies: []string{"SGD", "XYZ"}, Default: "SGD", Location: time.UTC, Now: time.Now})
	if err == nil || !strings.Contains(err.Error(), "XYZ") {
		t.Fatalf("err = %v, want a refusal naming XYZ", err)
	}
	if _, err := New(newFakeStore(), Options{Currencies: []string{"SGD"}, Default: "USD", Location: time.UTC, Now: time.Now}); err == nil {
		t.Fatal("a default outside the supported list was accepted")
	}
}

func TestLogFillsDefaultsAndNormalizes(t *testing.T) {
	svc, _ := newService(t, time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC))
	got, err := svc.Log(asAccount("a"), ports.FinanceInput{Amount: "14.5", Category: "  Food ", Merchant: " Ya Kun ", Note: "kaya toast"})
	if err != nil {
		t.Fatal(err)
	}
	want := ports.FinanceEntry{
		ID: "id1", OccurredOn: "2026-10-02", AmountMinor: 1450, Currency: "SGD", Category: "food",
		Merchant: "Ya Kun", Note: "kaya toast", Source: "chat",
		CreatedAt: time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC),
	}
	if got != want {
		t.Fatalf("Log = %+v\nwant  %+v", got, want)
	}
}

// 23:30 UTC on the 2nd is already the 3rd in Singapore. A default date taken
// from UTC would file the entry under the wrong day.
func TestDefaultDateIsTodayInTheOwnersTimezone(t *testing.T) {
	svc, _ := newService(t, time.Date(2026, 10, 2, 23, 30, 0, 0, time.UTC))
	got, err := svc.Log(asAccount("a"), ports.FinanceInput{Amount: "3", Category: "food"})
	if err != nil {
		t.Fatal(err)
	}
	if got.OccurredOn != "2026-10-03" {
		t.Fatalf("date = %s, want 2026-10-03", got.OccurredOn)
	}
}

func TestLogKeepsTheGivenCurrencyDateAndSource(t *testing.T) {
	svc, _ := newService(t, time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC))
	got, err := svc.Log(asAccount("a"), ports.FinanceInput{Amount: "1200", Currency: "jpy", Date: "2026-09-30", Category: "travel", Source: "photo"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Currency != "JPY" || got.AmountMinor != 1200 || got.OccurredOn != "2026-09-30" || got.Source != "photo" {
		t.Fatalf("Log = %+v", got)
	}
}

func TestLogRefusesWhatItCannotTrust(t *testing.T) {
	svc, store := newService(t, time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC))
	for name, in := range map[string]ports.FinanceInput{
		"no amount":            {Category: "food"},
		"symbol amount":        {Amount: "$5", Category: "food"},
		"unsupported currency": {Amount: "5", Currency: "XYZ", Category: "food"},
		"currency symbol":      {Amount: "5", Currency: "$", Category: "food"},
		"jpy with decimals":    {Amount: "5.5", Currency: "JPY", Category: "food"},
		"no category":          {Amount: "5"},
		"blank category":       {Amount: "5", Category: "   "},
		"long category":        {Amount: "5", Category: strings.Repeat("x", 41)},
		"long merchant":        {Amount: "5", Category: "food", Merchant: strings.Repeat("x", 81)},
		"long note":            {Amount: "5", Category: "food", Note: strings.Repeat("x", 281)},
		"bad date":             {Amount: "5", Category: "food", Date: "02/10/2026"},
		"impossible date":      {Amount: "5", Category: "food", Date: "2026-02-30"},
		"unknown source":       {Amount: "5", Category: "food", Source: "telepathy"},
	} {
		if _, err := svc.Log(asAccount("a"), in); !errors.Is(err, ports.ErrFinanceInvalid) {
			t.Errorf("%s: err = %v, want ErrFinanceInvalid", name, err)
		}
	}
	if len(store.order) != 0 {
		t.Fatalf("a refused entry reached the store: %v", store.order)
	}
}

func TestUpdateChangesOnlyTheFieldsGivenAndAdvancesUpdatedAt(t *testing.T) {
	now := time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC)
	svc, _ := newService(t, now)
	ctx := asAccount("a")
	created, _ := svc.Log(ctx, ports.FinanceInput{Amount: "14.50", Category: "food", Merchant: "Ya Kun"})
	svc.opts.Now = func() time.Time { return now.Add(time.Hour) }
	amount := "15.40"
	got, err := svc.Update(ctx, created.ID, ports.FinancePatch{Amount: &amount})
	if err != nil {
		t.Fatal(err)
	}
	if got.AmountMinor != 1540 || got.Category != "food" || got.Merchant != "Ya Kun" {
		t.Fatalf("Update = %+v", got)
	}
	if !got.UpdatedAt.Equal(now.Add(time.Hour)) || !got.CreatedAt.Equal(now) {
		t.Fatalf("created_at=%v updated_at=%v", got.CreatedAt, got.UpdatedAt)
	}
}

func TestUpdateNeedsTheAmountRestatedWhenTheCurrencyChanges(t *testing.T) {
	svc, _ := newService(t, time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC))
	ctx := asAccount("a")
	created, _ := svc.Log(ctx, ports.FinanceInput{Amount: "14.50", Category: "food"})
	jpy := "JPY"
	// 1450 minor units are 14.50 SGD but 1450 yen: carrying the number across
	// would silently change what was spent.
	if _, err := svc.Update(ctx, created.ID, ports.FinancePatch{Currency: &jpy}); !errors.Is(err, ports.ErrFinanceInvalid) {
		t.Fatalf("currency-only change: err = %v, want ErrFinanceInvalid", err)
	}
	amount := "1450"
	got, err := svc.Update(ctx, created.ID, ports.FinancePatch{Currency: &jpy, Amount: &amount})
	if err != nil || got.Currency != "JPY" || got.AmountMinor != 1450 {
		t.Fatalf("restated change = %+v, %v", got, err)
	}
}

func TestUpdateValidatesAndReportsMissingEntries(t *testing.T) {
	svc, _ := newService(t, time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC))
	ctx := asAccount("a")
	created, _ := svc.Log(ctx, ports.FinanceInput{Amount: "1", Category: "food"})
	bad := "nope"
	if _, err := svc.Update(ctx, created.ID, ports.FinancePatch{Amount: &bad}); !errors.Is(err, ports.ErrFinanceInvalid) {
		t.Fatalf("bad amount: err = %v", err)
	}
	if _, err := svc.Update(ctx, created.ID, ports.FinancePatch{Date: &bad}); !errors.Is(err, ports.ErrFinanceInvalid) {
		t.Fatalf("bad date: err = %v", err)
	}
	empty := " "
	if _, err := svc.Update(ctx, created.ID, ports.FinancePatch{Category: &empty}); !errors.Is(err, ports.ErrFinanceInvalid) {
		t.Fatalf("blank category: err = %v", err)
	}
	amount := "2"
	if _, err := svc.Update(ctx, "missing", ports.FinancePatch{Amount: &amount}); !errors.Is(err, ports.ErrFinanceEntryNotFound) {
		t.Fatalf("missing id: err = %v", err)
	}
	if _, err := svc.Update(ctx, created.ID, ports.FinancePatch{}); !errors.Is(err, ports.ErrFinanceInvalid) {
		t.Fatalf("empty patch: err = %v, want a refusal rather than a silent no-op", err)
	}
}

func TestDeleteReturnsTheEntry(t *testing.T) {
	svc, _ := newService(t, time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC))
	ctx := asAccount("a")
	created, _ := svc.Log(ctx, ports.FinanceInput{Amount: "9.90", Category: "health"})
	got, err := svc.Delete(ctx, created.ID)
	if err != nil || got.ID != created.ID || got.AmountMinor != 990 {
		t.Fatalf("Delete = %+v, %v", got, err)
	}
	if _, err := svc.Delete(ctx, created.ID); !errors.Is(err, ports.ErrFinanceEntryNotFound) {
		t.Fatalf("second delete: err = %v", err)
	}
}

func TestEntriesBelongToTheCallingAccount(t *testing.T) {
	svc, _ := newService(t, time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC))
	created, _ := svc.Log(asAccount("a"), ports.FinanceInput{Amount: "5", Category: "food"})
	if _, _, err := svc.List(context.Background(), ports.FinanceFilter{}); !errors.Is(err, ports.ErrNoPrincipal) {
		t.Fatalf("List without an account: err = %v, want ErrNoPrincipal", err)
	}
	if got, total, _ := svc.List(asAccount("b"), ports.FinanceFilter{}); total != 0 || len(got) != 0 {
		t.Fatalf("b sees %v", got)
	}
	if _, err := svc.Delete(asAccount("b"), created.ID); !errors.Is(err, ports.ErrFinanceEntryNotFound) {
		t.Fatalf("b deleting a's entry: err = %v", err)
	}
}

func TestListDefaultsToTheCurrentMonthAndCapsTheLimit(t *testing.T) {
	svc, store := newService(t, time.Date(2026, 10, 15, 4, 0, 0, 0, time.UTC))
	ctx := asAccount("a")
	for _, day := range []string{"2026-09-30", "2026-10-01", "2026-10-31", "2026-11-01"} {
		svc.Log(ctx, ports.FinanceInput{Amount: "1", Category: "food", Date: day})
	}
	got, total, err := svc.List(ctx, ports.FinanceFilter{})
	if err != nil || total != 2 {
		t.Fatalf("default list total=%d err=%v, want October's 2", total, err)
	}
	if days := []string{got[0].OccurredOn, got[1].OccurredOn}; !slices.Equal(days, []string{"2026-10-01", "2026-10-31"}) {
		t.Fatalf("days = %v", days)
	}
	for i := 0; i < 300; i++ {
		store.CreateFinanceEntry(ctx, ports.FinanceEntry{ID: fmt.Sprintf("bulk%d", i), OccurredOn: "2026-10-05", AmountMinor: 1, Currency: "SGD", Category: "food"})
	}
	got, total, _ = svc.List(ctx, ports.FinanceFilter{Limit: 100000})
	if len(got) != 200 || total != 302 {
		t.Fatalf("len=%d total=%d, want the 200 cap of 302", len(got), total)
	}
}

func TestListNormalizesTheCategoryFilterAndValidatesDates(t *testing.T) {
	svc, _ := newService(t, time.Date(2026, 10, 15, 4, 0, 0, 0, time.UTC))
	ctx := asAccount("a")
	svc.Log(ctx, ports.FinanceInput{Amount: "1", Category: "food"})
	svc.Log(ctx, ports.FinanceInput{Amount: "1", Category: "bills"})
	if got, _, _ := svc.List(ctx, ports.FinanceFilter{Category: " Food "}); len(got) != 1 || got[0].Category != "food" {
		t.Fatalf("category filter = %+v", got)
	}
	for _, f := range []ports.FinanceFilter{{From: "yesterday"}, {To: "2026-13-01"}, {From: "2026-10-31", To: "2026-10-01"}} {
		if _, _, err := svc.List(ctx, f); !errors.Is(err, ports.ErrFinanceInvalid) {
			t.Errorf("List(%+v): err = %v, want ErrFinanceInvalid", f, err)
		}
	}
}

func TestMonthBoundsAndRange(t *testing.T) {
	svc, _ := newService(t, time.Date(2026, 2, 10, 4, 0, 0, 0, time.UTC))
	for month, want := range map[string][2]string{
		"2026-10": {"2026-10-01", "2026-10-31"},
		"2026-02": {"2026-02-01", "2026-02-28"},
		"2028-02": {"2028-02-01", "2028-02-29"},
		"2026-12": {"2026-12-01", "2026-12-31"},
	} {
		from, to, err := svc.MonthBounds(month)
		if err != nil || from != want[0] || to != want[1] {
			t.Errorf("MonthBounds(%s) = %s %s %v, want %v", month, from, to, err, want)
		}
	}
	for _, bad := range []string{"", "2026", "2026-13", "2026-1", "October"} {
		if _, _, err := svc.MonthBounds(bad); !errors.Is(err, ports.ErrFinanceInvalid) {
			t.Errorf("MonthBounds(%q): err = %v", bad, err)
		}
	}
	if from, to, err := svc.Range("", ""); err != nil || from != "2026-02-01" || to != "2026-02-28" {
		t.Errorf("Range(\"\", \"\") = %s %s %v, want the current month", from, to, err)
	}
	if from, to, _ := svc.Range("2026-01-05", ""); from != "2026-01-05" || to != "" {
		t.Errorf("Range with only from = %s %s, want it left open", from, to)
	}
}

func TestSummaryResolvesTheRangeBeforeAsking(t *testing.T) {
	svc, _ := newService(t, time.Date(2026, 10, 15, 4, 0, 0, 0, time.UTC))
	ctx := asAccount("a")
	svc.Log(ctx, ports.FinanceInput{Amount: "5", Category: "food", Date: "2026-10-03"})
	svc.Log(ctx, ports.FinanceInput{Amount: "7", Category: "food", Date: "2026-09-03"})
	totals, err := svc.Summary(ctx, "", "")
	if err != nil || len(totals.ByCategory) != 1 || totals.ByCategory[0].AmountMinor != 500 {
		t.Fatalf("Summary = %+v, %v", totals, err)
	}
}

func TestServiceExposesWhatTheUIAndToolNeed(t *testing.T) {
	svc, _ := newService(t, time.Now())
	if svc.DefaultCurrency() != "SGD" || !slices.Equal(svc.Currencies(), config.FinanceCurrencies) {
		t.Fatalf("default=%s currencies=%v", svc.DefaultCurrency(), svc.Currencies())
	}
	if svc.Format(1450, "SGD") != "14.50" || svc.Format(1200, "JPY") != "1200" {
		t.Fatal("Format disagrees with FormatAmount")
	}
	// The list is a copy: a caller sorting or editing it must not reorder the
	// dropdown for everyone.
	svc.Currencies()[0] = "ZZZ"
	if svc.Currencies()[0] == "ZZZ" {
		t.Fatal("Currencies returned the service's own slice")
	}
}
