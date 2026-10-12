package finance

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestListResolvesEachSortToItsDefaultOrder(t *testing.T) {
	svc, store := newService(t, time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC))
	ctx := asAccount("a")
	for _, c := range []struct {
		sort, order, wantSort, wantOrder string
	}{
		{"", "", "date", "desc"},
		{"date", "", "date", "desc"},
		{"amount", "", "amount", "desc"},
		{"category", "", "category", "asc"},
		{"merchant", "", "merchant", "asc"},
		{"merchant", "desc", "merchant", "desc"},
		// Sort keys and orders are case-insensitive, like categories and currencies.
		{" Amount ", "ASC", "amount", "asc"},
	} {
		if _, _, err := svc.List(ctx, ports.FinanceFilter{Sort: c.sort, Order: c.order}); err != nil {
			t.Fatalf("List(sort=%q order=%q): %v", c.sort, c.order, err)
		}
		if store.last.Sort != c.wantSort || store.last.Order != c.wantOrder {
			t.Errorf("sort=%q order=%q reached the store as %q %q, want %q %q", c.sort, c.order, store.last.Sort, store.last.Order, c.wantSort, c.wantOrder)
		}
	}
}

func TestListRefusesAnUnknownSortOrOrder(t *testing.T) {
	svc, _ := newService(t, time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC))
	ctx := asAccount("a")
	for _, filter := range []ports.FinanceFilter{
		{Sort: "total"},
		{Sort: "note"},
		{Sort: "date; DROP TABLE finance_entries"},
		{Sort: "date", Order: "up"},
		{Order: "descending"},
	} {
		_, _, err := svc.List(ctx, filter)
		if !errors.Is(err, ports.ErrFinanceInvalid) {
			t.Errorf("List(%+v) error = %v, want ErrFinanceInvalid", filter, err)
		}
	}
}

func TestListCleansTheSearchQuery(t *testing.T) {
	svc, store := newService(t, time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC))
	ctx := asAccount("a")
	if _, _, err := svc.List(ctx, ports.FinanceFilter{Query: "  starbucks  "}); err != nil {
		t.Fatal(err)
	}
	if store.last.Query != "starbucks" {
		t.Errorf("query reached the store as %q, want trimmed", store.last.Query)
	}
	// A blank query is no search at all, not an empty match.
	if _, _, err := svc.List(ctx, ports.FinanceFilter{Query: "   "}); err != nil {
		t.Fatal(err)
	}
	if store.last.Query != "" {
		t.Errorf("blank query reached the store as %q", store.last.Query)
	}
	_, _, err := svc.List(ctx, ports.FinanceFilter{Query: strings.Repeat("x", maxQuery+1)})
	if !errors.Is(err, ports.ErrFinanceInvalid) {
		t.Errorf("an overlong query error = %v, want ErrFinanceInvalid", err)
	}
}
