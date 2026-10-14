package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func kindEntry(id, kind, day, currency, category string, minor int64) ports.FinanceEntry {
	entry := financeEntry(id, day, currency, category, minor)
	entry.Kind = kind
	return entry
}

func TestFinanceKindRoundTripsAndFilters(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	for _, entry := range []ports.FinanceEntry{
		kindEntry("e1", ports.FinanceKindExpense, "2026-10-02", "SGD", "food", 1000),
		kindEntry("e2", ports.FinanceKindIncome, "2026-10-03", "SGD", "salary", 500000),
		kindEntry("e3", ports.FinanceKindInvest, "2026-10-04", "SGD", "stocks", 200000),
	} {
		if err := db.CreateFinanceEntry(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	got, total, err := db.ListFinanceEntries(ctx, ports.FinanceFilter{Kind: ports.FinanceKindIncome})
	if err != nil || total != 1 || len(got) != 1 || got[0].ID != "e2" || got[0].Kind != ports.FinanceKindIncome {
		t.Fatalf("income filter = %+v total=%d err=%v", got, total, err)
	}
	if _, total, _ := db.ListFinanceEntries(ctx, ports.FinanceFilter{}); total != 3 {
		t.Fatalf("unfiltered total = %d, want 3", total)
	}
}

func TestEntryWithoutKindIsStoredAsExpense(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	if err := db.CreateFinanceEntry(ctx, financeEntry("e1", "2026-10-02", "SGD", "food", 1000)); err != nil {
		t.Fatal(err)
	}
	got, _, err := db.ListFinanceEntries(ctx, ports.FinanceFilter{})
	if err != nil || len(got) != 1 || got[0].Kind != ports.FinanceKindExpense {
		t.Fatalf("list = %+v err=%v, want one expense", got, err)
	}
}

func TestFinanceTotalsKeepKindsApart(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	for _, entry := range []ports.FinanceEntry{
		kindEntry("e1", ports.FinanceKindExpense, "2026-10-02", "SGD", "food", 1000),
		kindEntry("e2", ports.FinanceKindIncome, "2026-10-02", "SGD", "food", 300),
	} {
		if err := db.CreateFinanceEntry(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	totals, err := db.FinanceTotals(ctx, "2026-10-01", "2026-10-31")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int64{}
	for _, row := range totals.ByCategory {
		kinds[row.Kind] += row.AmountMinor
	}
	if kinds[ports.FinanceKindExpense] != 1000 || kinds[ports.FinanceKindIncome] != 300 {
		t.Fatalf("by kind = %v, want expense 1000 and income 300", kinds)
	}
	if len(totals.ByDay) != 2 {
		t.Fatalf("by day rows = %+v, want one per kind", totals.ByDay)
	}
}

func TestFinanceMonthsSumsEachMonthPerKind(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	for _, entry := range []ports.FinanceEntry{
		kindEntry("e1", ports.FinanceKindExpense, "2026-08-30", "SGD", "food", 1000),
		kindEntry("e2", ports.FinanceKindExpense, "2026-10-02", "SGD", "food", 400),
		kindEntry("e3", ports.FinanceKindExpense, "2026-10-09", "SGD", "food", 100),
		kindEntry("e4", ports.FinanceKindIncome, "2026-10-09", "SGD", "salary", 9000),
		kindEntry("e5", ports.FinanceKindExpense, "2026-11-01", "SGD", "food", 1), // outside the range
	} {
		if err := db.CreateFinanceEntry(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	months, err := db.FinanceMonths(ctx, "2026-08-01", "2026-10-31")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{
		"2026-08/expense": 1000,
		"2026-10/expense": 500,
		"2026-10/income":  9000,
	}
	if len(months) != len(want) {
		t.Fatalf("months = %+v, want %d rows", months, len(want))
	}
	for _, row := range months {
		if want[row.Month+"/"+row.Kind] != row.AmountMinor {
			t.Fatalf("row %+v, want %d", row, want[row.Month+"/"+row.Kind])
		}
	}
}

func TestFinanceCategoriesListCountsAndColours(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	for _, entry := range []ports.FinanceEntry{
		kindEntry("e1", ports.FinanceKindExpense, "2026-10-02", "SGD", "food", 1000),
		kindEntry("e2", ports.FinanceKindExpense, "2026-10-03", "SGD", "food", 1000),
		kindEntry("e3", ports.FinanceKindExpense, "2026-10-03", "SGD", "transport", 1000),
	} {
		if err := db.CreateFinanceEntry(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetFinanceCategoryColor(ctx, "food", "rose"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFinanceCategoryColor(ctx, "food", "sky"); err != nil {
		t.Fatal(err)
	}
	categories, err := db.FinanceCategories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.FinanceCategory{{Name: "food", Count: 2, Color: "sky"}, {Name: "transport", Count: 1}}
	if len(categories) != len(want) || categories[0] != want[0] || categories[1] != want[1] {
		t.Fatalf("categories = %+v, want %+v", categories, want)
	}
}

func TestRenameCategoryMergesIntoTarget(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	if err := db.CreateFinanceEntry(ctx, kindEntry("e1", ports.FinanceKindExpense, "2026-10-02", "SGD", "food", 1000)); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFinanceEntry(ctx, kindEntry("e2", ports.FinanceKindExpense, "2026-10-02", "SGD", "eating", 1000)); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFinanceCategoryColor(ctx, "food", "rose"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFinanceCategoryColor(ctx, "eating", "sky"); err != nil {
		t.Fatal(err)
	}
	if err := db.RenameFinanceCategory(ctx, "food", "eating"); err != nil {
		t.Fatal(err)
	}
	categories, err := db.FinanceCategories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(categories) != 1 || categories[0].Name != "eating" || categories[0].Count != 2 || categories[0].Color != "sky" {
		t.Fatalf("after merge = %+v, want one eating category with 2 entries in sky", categories)
	}
}

func TestRenameCategoryTakesSourceColourWhenTargetHasNone(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	if err := db.CreateFinanceEntry(ctx, kindEntry("e1", ports.FinanceKindExpense, "2026-10-02", "SGD", "food", 1000)); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFinanceCategoryColor(ctx, "food", "rose"); err != nil {
		t.Fatal(err)
	}
	if err := db.RenameFinanceCategory(ctx, "food", "groceries"); err != nil {
		t.Fatal(err)
	}
	categories, err := db.FinanceCategories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(categories) != 1 || categories[0].Name != "groceries" || categories[0].Color != "rose" {
		t.Fatalf("after rename = %+v, want groceries in rose", categories)
	}
}

func TestCategoryColoursAreAccountScoped(t *testing.T) {
	db := newTestStore(t, 0)
	a, b := as("a"), as("b")
	if err := db.CreateFinanceEntry(a, kindEntry("e1", ports.FinanceKindExpense, "2026-10-02", "SGD", "food", 1000)); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFinanceCategoryColor(a, "food", "rose"); err != nil {
		t.Fatal(err)
	}
	if categories, _ := db.FinanceCategories(b); len(categories) != 0 {
		t.Fatalf("b sees a's categories: %+v", categories)
	}
	if err := db.RenameFinanceCategory(b, "food", "x"); err != nil {
		t.Fatal(err)
	}
	categories, _ := db.FinanceCategories(a)
	if len(categories) != 1 || categories[0].Name != "food" || categories[0].Color != "rose" {
		t.Fatalf("b's rename touched a: %+v", categories)
	}
}

func TestOpeningOlderDatabaseAddsKindAsExpense(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eggy.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the table as version 10 left it: no kind column.
	ctx := as("a")
	if _, err := store.db.Exec(`ALTER TABLE finance_entries DROP COLUMN kind`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO finance_entries (id, account_id, occurred_on, amount_minor, currency, category, merchant, note, source, created_at, updated_at)
		VALUES ('old', 'a', '2026-10-02', 1000, 'SGD', 'food', '', '', 'chat', '2026-10-02T09:00:00Z', '2026-10-02T09:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, _, err := reopened.ListFinanceEntries(ctx, ports.FinanceFilter{})
	if err != nil || len(got) != 1 || got[0].Kind != ports.FinanceKindExpense {
		t.Fatalf("old row after upgrade = %+v err=%v, want one expense", got, err)
	}
}

func TestFinanceCategoriesRefuseNoPrincipal(t *testing.T) {
	db := newTestStore(t, 0)
	if _, err := db.FinanceCategories(context.Background()); err == nil {
		t.Fatal("FinanceCategories without a principal succeeded")
	}
}
