package panel

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestFinanceListPassesSearchAndSortThrough(t *testing.T) {
	finance := &fakeFinance{}
	handler, cookie := financeHandler(t, finance)
	response := call(t, handler, cookie, http.MethodGet, "/api/finance/entries?q=coffee&sort=amount&order=asc&limit=5", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", response.Code, response.Body.String())
	}
	want := ports.FinanceFilter{Query: "coffee", Sort: "amount", Order: "asc", Limit: 5}
	if finance.filter != want {
		t.Fatalf("filter = %+v, want %+v", finance.filter, want)
	}
}

func TestFinanceListReportsAnUnknownSortAsBadRequest(t *testing.T) {
	finance := &fakeFinance{err: fmt.Errorf("%w: sort %q must be one of date, amount, category, merchant", ports.ErrFinanceInvalid, "total")}
	handler, cookie := financeHandler(t, finance)
	response := call(t, handler, cookie, http.MethodGet, "/api/finance/entries?sort=total", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", response.Code, response.Body.String())
	}
}
