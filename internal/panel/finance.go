package panel

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// FinanceService is the finance plugin as the panel sees it. The panel declares
// the interface and never imports the plugin: bootstrap hands over the one
// service the model's tool also writes through, so there is a single place that
// decides what a valid entry is, and nothing here can write around it.
//
// Every method acts as the account on the context, which the session guard put
// there. No route reads an account from a body, a query or a path.
type FinanceService interface {
	Log(ctx context.Context, in ports.FinanceInput) (ports.FinanceEntry, error)
	Update(ctx context.Context, id string, patch ports.FinancePatch) (ports.FinanceEntry, error)
	Delete(ctx context.Context, id string) (ports.FinanceEntry, error)
	List(ctx context.Context, filter ports.FinanceFilter) ([]ports.FinanceEntry, int, error)
	Summary(ctx context.Context, from, to string) (ports.FinanceTotals, error)
	MonthBounds(month string) (from, to string, err error)
	Range(from, to string) (string, string, error)
	// Format renders a count of minor units for display. The browser never
	// does arithmetic on money; every amount it shows arrives as text.
	Format(minor int64, currency string) string
	Currencies() []string
	DefaultCurrency() string
}

type financeEntryJSON struct {
	ID       string `json:"id"`
	Date     string `json:"date"`
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
	Category string `json:"category"`
	Merchant string `json:"merchant"`
	Note     string `json:"note"`
	Source   string `json:"source"`
}

func financeEntry(svc FinanceService, e ports.FinanceEntry) financeEntryJSON {
	return financeEntryJSON{
		ID: e.ID, Date: e.OccurredOn, Amount: svc.Format(e.AmountMinor, e.Currency), Currency: e.Currency,
		Category: e.Category, Merchant: e.Merchant, Note: e.Note, Source: e.Source,
	}
}

// writeFinanceFailure says whose fault a refusal was: what the caller got wrong
// is a 400 carrying the reason, an id that is not theirs is a 404 (it cannot be
// told apart from one that does not exist, on purpose), and anything else is a
// 500 that does not repeat the error, which may name a path or a statement.
func writeFinanceFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrFinanceInvalid):
		writeWebError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), ports.ErrFinanceInvalid.Error()+": "))
	case errors.Is(err, ports.ErrFinanceEntryNotFound):
		writeWebError(w, http.StatusNotFound, "entry not found")
	default:
		writeWebError(w, http.StatusInternalServerError, "could not complete the finance request")
	}
}

// decodeFinanceBody refuses any field it does not know. That is what keeps an
// account, an id or a source out of a request body: the struct has no place
// for them, so sending one is an error rather than something quietly ignored.
func decodeFinanceBody(r *http.Request, into any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(into)
}

func newFinanceListHandler(svc FinanceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		filter := ports.FinanceFilter{From: query.Get("from"), To: query.Get("to"), Category: query.Get("category")}
		if raw := query.Get("limit"); raw != "" {
			limit, err := strconv.Atoi(raw)
			if err != nil || limit < 0 {
				writeWebError(w, http.StatusBadRequest, "limit must be a whole number")
				return
			}
			filter.Limit = limit
		}
		entries, total, err := svc.List(r.Context(), filter)
		if err != nil {
			writeFinanceFailure(w, err)
			return
		}
		out := make([]financeEntryJSON, 0, len(entries))
		for _, e := range entries {
			out = append(out, financeEntry(svc, e))
		}
		writeJSON(w, map[string]any{"entries": out, "total": total})
	}
}

func newFinanceCreateHandler(svc FinanceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
			Date     string `json:"date"`
			Category string `json:"category"`
			Merchant string `json:"merchant"`
			Note     string `json:"note"`
		}
		if err := decodeFinanceBody(r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		// An entry typed into the panel is from the panel; the body has no say.
		entry, err := svc.Log(r.Context(), ports.FinanceInput{
			Amount: body.Amount, Currency: body.Currency, Date: body.Date, Category: body.Category,
			Merchant: body.Merchant, Note: body.Note, Source: "panel",
		})
		if err != nil {
			writeFinanceFailure(w, err)
			return
		}
		writeJSON(w, financeEntry(svc, entry))
	}
}

func newFinanceUpdateHandler(svc FinanceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Pointers, so a field the body leaves out is left alone while one it
		// sends empty is an explicit request to clear it.
		var body struct {
			Amount   *string `json:"amount"`
			Currency *string `json:"currency"`
			Date     *string `json:"date"`
			Category *string `json:"category"`
			Merchant *string `json:"merchant"`
			Note     *string `json:"note"`
		}
		if err := decodeFinanceBody(r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		entry, err := svc.Update(r.Context(), r.PathValue("id"), ports.FinancePatch{
			Amount: body.Amount, Currency: body.Currency, Date: body.Date,
			Category: body.Category, Merchant: body.Merchant, Note: body.Note,
		})
		if err != nil {
			writeFinanceFailure(w, err)
			return
		}
		writeJSON(w, financeEntry(svc, entry))
	}
}

func newFinanceDeleteHandler(svc FinanceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entry, err := svc.Delete(r.Context(), r.PathValue("id"))
		if err != nil {
			writeFinanceFailure(w, err)
			return
		}
		writeJSON(w, financeEntry(svc, entry))
	}
}

// shareOf sizes a bar against the largest in its own currency, to four places.
// It is computed here so the page draws bars without doing money arithmetic.
func shareOf(minor, largest int64) float64 {
	if largest <= 0 {
		return 0
	}
	return math.Round(float64(minor)/float64(largest)*1e4) / 1e4
}

func newFinanceSummaryHandler(svc FinanceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		month := r.URL.Query().Get("month")
		var (
			from, to string
			err      error
		)
		if month == "" {
			from, to, err = svc.Range("", "")
		} else {
			from, to, err = svc.MonthBounds(month)
		}
		if err != nil {
			writeFinanceFailure(w, err)
			return
		}
		if month == "" && len(from) >= 7 {
			month = from[:7]
		}
		totals, err := svc.Summary(r.Context(), from, to)
		if err != nil {
			writeFinanceFailure(w, err)
			return
		}

		type sum struct {
			minor int64
			count int
		}
		perCurrency := map[string]*sum{}
		var order []string
		largestCategory := map[string]int64{}
		for _, row := range totals.ByCategory {
			s, ok := perCurrency[row.Currency]
			if !ok {
				s = &sum{}
				perCurrency[row.Currency] = s
				order = append(order, row.Currency)
			}
			s.minor += row.AmountMinor
			s.count += row.Count
			largestCategory[row.Currency] = max(largestCategory[row.Currency], row.AmountMinor)
		}
		largestDay := map[string]int64{}
		for _, row := range totals.ByDay {
			largestDay[row.Currency] = max(largestDay[row.Currency], row.AmountMinor)
		}
		// The deployment's default currency leads: it is the one the page
		// draws its daily strip in, and the one most spending is in.
		def := svc.DefaultCurrency()
		slices.SortStableFunc(order, func(a, b string) int {
			switch {
			case a == def && b != def:
				return -1
			case b == def && a != def:
				return 1
			}
			return 0
		})

		totalsOut := make([]map[string]any, 0, len(order))
		for _, code := range order {
			totalsOut = append(totalsOut, map[string]any{"currency": code, "amount": svc.Format(perCurrency[code].minor, code), "count": perCurrency[code].count})
		}
		categories := make([]map[string]any, 0, len(totals.ByCategory))
		for _, row := range totals.ByCategory {
			categories = append(categories, map[string]any{
				"currency": row.Currency, "category": row.Category, "amount": svc.Format(row.AmountMinor, row.Currency),
				"count": row.Count, "share": shareOf(row.AmountMinor, largestCategory[row.Currency]),
			})
		}
		days := make([]map[string]any, 0, len(totals.ByDay))
		for _, row := range totals.ByDay {
			days = append(days, map[string]any{
				"currency": row.Currency, "day": row.Day, "amount": svc.Format(row.AmountMinor, row.Currency),
				"share": shareOf(row.AmountMinor, largestDay[row.Currency]),
			})
		}
		writeJSON(w, map[string]any{
			"month": month, "from": from, "to": to,
			"currencies": svc.Currencies(), "default_currency": def,
			"totals": totalsOut, "by_category": categories, "by_day": days,
		})
	}
}
