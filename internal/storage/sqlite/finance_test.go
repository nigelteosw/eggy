package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

var _ ports.FinanceStore = (*Store)(nil)

func financeEntry(id, day, currency, category string, minor int64) ports.FinanceEntry {
	at := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	return ports.FinanceEntry{
		ID: id, OccurredOn: day, AmountMinor: minor, Currency: currency,
		Category: category, Merchant: "m-" + id, Note: "n-" + id, Source: "chat",
		CreatedAt: at, UpdatedAt: at,
	}
}

func TestFinanceEntryRoundTrips(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	want := financeEntry("e1", "2026-10-02", "SGD", "food", 1450)
	if err := db.CreateFinanceEntry(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, total, err := db.ListFinanceEntries(ctx, ports.FinanceFilter{})
	if err != nil || total != 1 || len(got) != 1 {
		t.Fatalf("list = %v total=%d err=%v", got, total, err)
	}
	if got[0] != want {
		t.Fatalf("round trip = %+v, want %+v", got[0], want)
	}
}

func TestUpdateChangesOnlyWhatTheMutatorTouches(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	if err := db.CreateFinanceEntry(ctx, financeEntry("e1", "2026-10-02", "SGD", "food", 1450)); err != nil {
		t.Fatal(err)
	}
	later := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	updated, err := db.UpdateFinanceEntry(ctx, "e1", func(e *ports.FinanceEntry) error {
		e.AmountMinor = 1540
		e.UpdatedAt = later
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AmountMinor != 1540 || updated.Category != "food" || updated.Merchant != "m-e1" || !updated.UpdatedAt.Equal(later) {
		t.Fatalf("updated = %+v", updated)
	}
	if !updated.CreatedAt.Equal(time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("created_at moved: %v", updated.CreatedAt)
	}
	stored, _, _ := db.ListFinanceEntries(ctx, ports.FinanceFilter{})
	if stored[0] != updated {
		t.Fatalf("stored %+v differs from returned %+v", stored[0], updated)
	}
}

func TestUpdateRollsBackWhenTheMutatorFails(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	db.CreateFinanceEntry(ctx, financeEntry("e1", "2026-10-02", "SGD", "food", 1450))
	boom := errors.New("refused")
	if _, err := db.UpdateFinanceEntry(ctx, "e1", func(e *ports.FinanceEntry) error {
		e.AmountMinor = 1
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the mutator's error", err)
	}
	stored, _, _ := db.ListFinanceEntries(ctx, ports.FinanceFilter{})
	if stored[0].AmountMinor != 1450 {
		t.Fatalf("a failed mutation was written: %+v", stored[0])
	}
}

func TestDeleteReturnsTheEntryItRemoved(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	want := financeEntry("e1", "2026-10-02", "JPY", "travel", 1200)
	db.CreateFinanceEntry(ctx, want)
	got, err := db.DeleteFinanceEntry(ctx, "e1")
	if err != nil || got != want {
		t.Fatalf("delete = %+v err=%v", got, err)
	}
	if _, total, _ := db.ListFinanceEntries(ctx, ports.FinanceFilter{}); total != 0 {
		t.Fatalf("entry survived delete, total=%d", total)
	}
	if _, err := db.DeleteFinanceEntry(ctx, "e1"); !errors.Is(err, ports.ErrFinanceEntryNotFound) {
		t.Fatalf("second delete err = %v, want not found", err)
	}
}

func TestListIsNewestFirstWithLimitAndTotal(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	db.CreateFinanceEntry(ctx, financeEntry("old", "2026-09-30", "SGD", "food", 100))
	db.CreateFinanceEntry(ctx, financeEntry("new", "2026-10-02", "SGD", "food", 100))
	mid := financeEntry("mid", "2026-10-01", "SGD", "food", 100)
	db.CreateFinanceEntry(ctx, mid)
	got, total, err := db.ListFinanceEntries(ctx, ports.FinanceFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(got) != 2 || got[0].ID != "new" || got[1].ID != "mid" {
		t.Fatalf("got %v total=%d, want [new mid] of 3", ids(got), total)
	}
}

func TestListDateBoundsAreInclusiveAndCategoryFilters(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	db.CreateFinanceEntry(ctx, financeEntry("a", "2026-09-30", "SGD", "food", 100))
	db.CreateFinanceEntry(ctx, financeEntry("b", "2026-10-01", "SGD", "food", 100))
	db.CreateFinanceEntry(ctx, financeEntry("c", "2026-10-31", "SGD", "transport", 100))
	db.CreateFinanceEntry(ctx, financeEntry("d", "2026-11-01", "SGD", "food", 100))
	got, total, _ := db.ListFinanceEntries(ctx, ports.FinanceFilter{From: "2026-10-01", To: "2026-10-31"})
	if total != 2 || len(got) != 2 {
		t.Fatalf("october = %v, want b and c", ids(got))
	}
	got, _, _ = db.ListFinanceEntries(ctx, ports.FinanceFilter{From: "2026-10-01", To: "2026-10-31", Category: "food"})
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("october food = %v, want b", ids(got))
	}
}

func TestTotalsGroupByCurrencyCategoryAndDay(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	db.CreateFinanceEntry(ctx, financeEntry("1", "2026-10-01", "SGD", "food", 500))
	db.CreateFinanceEntry(ctx, financeEntry("2", "2026-10-01", "SGD", "food", 250))
	db.CreateFinanceEntry(ctx, financeEntry("3", "2026-10-02", "SGD", "transport", 300))
	db.CreateFinanceEntry(ctx, financeEntry("4", "2026-10-02", "JPY", "food", 1200))
	db.CreateFinanceEntry(ctx, financeEntry("5", "2026-11-01", "SGD", "food", 999))
	totals, err := db.FinanceTotals(ctx, "2026-10-01", "2026-10-31")
	if err != nil {
		t.Fatal(err)
	}
	wantCat := map[string]ports.FinanceCategoryTotal{
		"SGD/food":      {Currency: "SGD", Category: "food", AmountMinor: 750, Count: 2},
		"SGD/transport": {Currency: "SGD", Category: "transport", AmountMinor: 300, Count: 1},
		"JPY/food":      {Currency: "JPY", Category: "food", AmountMinor: 1200, Count: 1},
	}
	if len(totals.ByCategory) != len(wantCat) {
		t.Fatalf("by category = %+v", totals.ByCategory)
	}
	for _, got := range totals.ByCategory {
		if want := wantCat[got.Currency+"/"+got.Category]; got != want {
			t.Fatalf("category total %+v, want %+v", got, want)
		}
	}
	wantDay := map[string]int64{"SGD/2026-10-01": 750, "SGD/2026-10-02": 300, "JPY/2026-10-02": 1200}
	if len(totals.ByDay) != len(wantDay) {
		t.Fatalf("by day = %+v", totals.ByDay)
	}
	for _, got := range totals.ByDay {
		if wantDay[got.Currency+"/"+got.Day] != got.AmountMinor {
			t.Fatalf("day total %+v", got)
		}
	}
}

func TestAccountCannotSeeChangeOrDeleteAnotherAccountsEntries(t *testing.T) {
	db := newTestStore(t, 0)
	a, b := as("a"), as("b")
	db.CreateFinanceEntry(a, financeEntry("secret", "2026-10-02", "SGD", "health", 9900))
	if got, total, err := db.ListFinanceEntries(b, ports.FinanceFilter{}); err != nil || total != 0 || len(got) != 0 {
		t.Fatalf("b listed a's entries: %v total=%d err=%v", got, total, err)
	}
	if totals, err := db.FinanceTotals(b, "2026-01-01", "2026-12-31"); err != nil || len(totals.ByCategory) != 0 || len(totals.ByDay) != 0 {
		t.Fatalf("b summed a's entries: %+v err=%v", totals, err)
	}
	_, err := db.UpdateFinanceEntry(b, "secret", func(e *ports.FinanceEntry) error { e.AmountMinor = 1; return nil })
	if !errors.Is(err, ports.ErrFinanceEntryNotFound) {
		t.Fatalf("b updating a's entry: err = %v, want not found", err)
	}
	if _, err := db.DeleteFinanceEntry(b, "secret"); !errors.Is(err, ports.ErrFinanceEntryNotFound) {
		t.Fatalf("b deleting a's entry: err = %v, want not found", err)
	}
	got, _, _ := db.ListFinanceEntries(a, ports.FinanceFilter{})
	if len(got) != 1 || got[0].AmountMinor != 9900 {
		t.Fatalf("a's entry was disturbed: %+v", got)
	}
}

func TestFinanceRejectsADuplicateIDAndAZeroAmount(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	if err := db.CreateFinanceEntry(ctx, financeEntry("e1", "2026-10-02", "SGD", "food", 100)); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFinanceEntry(ctx, financeEntry("e1", "2026-10-02", "SGD", "food", 100)); err == nil {
		t.Fatal("a duplicate id was accepted")
	}
	if err := db.CreateFinanceEntry(ctx, financeEntry("e2", "2026-10-02", "SGD", "food", 0)); err == nil {
		t.Fatal("a zero amount reached the table")
	}
}

func TestFinanceStoreFailsClosedWithoutAPrincipal(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	checks := map[string]func() error{
		"Create": func() error { return db.CreateFinanceEntry(ctx, financeEntry("e", "2026-10-02", "SGD", "food", 1)) },
		"Update": func() error {
			_, err := db.UpdateFinanceEntry(ctx, "e", func(*ports.FinanceEntry) error { return nil })
			return err
		},
		"Delete": func() error { _, err := db.DeleteFinanceEntry(ctx, "e"); return err },
		"List":   func() error { _, _, err := db.ListFinanceEntries(ctx, ports.FinanceFilter{}); return err },
		"Totals": func() error { _, err := db.FinanceTotals(ctx, "", ""); return err },
	}
	for name, call := range checks {
		if err := call(); !errors.Is(err, ports.ErrNoPrincipal) {
			t.Errorf("%s without a principal: err = %v, want ErrNoPrincipal", name, err)
		}
	}
}

// The table is additive, so an existing home gains it without a migration and
// without a new machine-state version: an older binary opens the same file and
// ignores it, which keeps a rollback possible.
func TestOpeningAnExistingHomeAddsTheFinanceTableWithoutBumpingTheVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eggy.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DROP TABLE finance_entries`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateFinanceEntry(as("a"), financeEntry("e1", "2026-10-02", "SGD", "food", 100)); err != nil {
		t.Fatalf("table not recreated on open: %v", err)
	}
	var version string
	if err := store.db.QueryRow(`SELECT value FROM schema_meta WHERE key = ?`, machineStateVersionKey).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != strconv.Itoa(localAuthVersion) {
		t.Fatalf("machine state version = %s, want it unchanged at %d", version, localAuthVersion)
	}
}

func ids(entries []ports.FinanceEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}
