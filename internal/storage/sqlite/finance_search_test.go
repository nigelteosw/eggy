package sqlite

import (
	"reflect"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// seedSearchEntries writes five entries with spread-out merchants, notes,
// categories and currencies, so each search and sort below has one clear answer.
func seedSearchEntries(t *testing.T, db *Store) {
	t.Helper()
	at := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	entries := []ports.FinanceEntry{
		{ID: "e1", OccurredOn: "2026-10-02", AmountMinor: 650, Currency: "SGD", Category: "food", Merchant: "Starbucks", Source: "chat"},
		{ID: "e2", OccurredOn: "2026-10-03", AmountMinor: 1200, Currency: "SGD", Category: "transport", Merchant: "Grab", Note: "coffee with Sam", Source: "chat"},
		{ID: "e3", OccurredOn: "2026-10-01", AmountMinor: 4520, Currency: "SGD", Category: "groceries", Merchant: "Cold Storage", Source: "chat"},
		{ID: "e4", OccurredOn: "2026-10-04", AmountMinor: 999, Currency: "SGD", Category: "shopping", Merchant: "100% Pure", Source: "chat"},
		{ID: "e5", OccurredOn: "2026-10-05", AmountMinor: 1800, Currency: "JPY", Category: "food", Merchant: "Ichiran", Source: "chat"},
	}
	for _, e := range entries {
		e.CreatedAt, e.UpdatedAt = at, at
		if err := db.CreateFinanceEntry(as("a"), e); err != nil {
			t.Fatal(err)
		}
	}
}

func listIDs(t *testing.T, db *Store, filter ports.FinanceFilter) ([]string, int) {
	t.Helper()
	entries, total, err := db.ListFinanceEntries(as("a"), filter)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return ids, total
}

func TestListSearchMatchesMerchantNoteAndCategoryIgnoringCase(t *testing.T) {
	db := newTestStore(t, 0)
	seedSearchEntries(t, db)
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"starbucks", []string{"e1"}},
		{"STARBUCKS", []string{"e1"}},
		{"coffee", []string{"e2"}},
		{"FOOD", []string{"e5", "e1"}},
		{"100%", []string{"e4"}},
		{"_", []string{}},
		{"nothing matches this", []string{}},
	} {
		got, total := listIDs(t, db, ports.FinanceFilter{Query: c.query, Sort: ports.FinanceSortMerchant, Order: "asc"})
		if !reflect.DeepEqual(got, c.want) || total != len(c.want) {
			t.Errorf("query %q = %v (total %d), want %v", c.query, got, total, c.want)
		}
	}
}

func TestListSortsByEachKeyInEachOrder(t *testing.T) {
	db := newTestStore(t, 0)
	seedSearchEntries(t, db)
	for _, c := range []struct {
		name   string
		filter ports.FinanceFilter
		want   []string
	}{
		// No sort is the long-standing order: newest first.
		{"default", ports.FinanceFilter{}, []string{"e5", "e4", "e2", "e1", "e3"}},
		{"date asc", ports.FinanceFilter{Sort: ports.FinanceSortDate, Order: "asc"}, []string{"e3", "e1", "e2", "e4", "e5"}},
		// Amounts group by currency first: JPY and SGD are never compared.
		{"amount desc", ports.FinanceFilter{Sort: ports.FinanceSortAmount, Order: "desc"}, []string{"e5", "e3", "e2", "e4", "e1"}},
		{"amount asc", ports.FinanceFilter{Sort: ports.FinanceSortAmount, Order: "asc"}, []string{"e5", "e1", "e4", "e2", "e3"}},
		// Merchant ignores case: "100% Pure" sorts before "Cold Storage".
		{"merchant asc", ports.FinanceFilter{Sort: ports.FinanceSortMerchant, Order: "asc"}, []string{"e4", "e3", "e2", "e5", "e1"}},
		{"merchant desc", ports.FinanceFilter{Sort: ports.FinanceSortMerchant, Order: "desc"}, []string{"e1", "e5", "e2", "e3", "e4"}},
		// Ties within a category fall back to newest first.
		{"category asc", ports.FinanceFilter{Sort: ports.FinanceSortCategory, Order: "asc"}, []string{"e5", "e1", "e3", "e4", "e2"}},
		{"category desc", ports.FinanceFilter{Sort: ports.FinanceSortCategory, Order: "desc"}, []string{"e2", "e4", "e3", "e5", "e1"}},
	} {
		got, total := listIDs(t, db, c.filter)
		if !reflect.DeepEqual(got, c.want) || total != 5 {
			t.Errorf("%s = %v (total %d), want %v", c.name, got, total, c.want)
		}
	}
}

func TestListSearchAndSortCombineWithDatesAndLimit(t *testing.T) {
	db := newTestStore(t, 0)
	seedSearchEntries(t, db)
	// "food" matches two entries; the limit trims the list but not the count.
	got, total := listIDs(t, db, ports.FinanceFilter{Query: "food", Sort: ports.FinanceSortAmount, Order: "asc", To: "2026-10-03", Limit: 1})
	if !reflect.DeepEqual(got, []string{"e1"}) || total != 1 {
		t.Fatalf("got %v total %d, want [e1] total 1 (e5 is after the to date)", got, total)
	}
	got, total = listIDs(t, db, ports.FinanceFilter{Query: "food", Sort: ports.FinanceSortAmount, Order: "asc", Limit: 1})
	if !reflect.DeepEqual(got, []string{"e5"}) || total != 2 {
		t.Fatalf("got %v total %d, want [e5] total 2", got, total)
	}
}
