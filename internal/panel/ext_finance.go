package panel

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// FinanceService is the finance extension as the panel sees it. The panel declares
// the interface and never imports the extension: bootstrap hands over the one
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
	// Report is everything the Finance page draws for one month, in minor units.
	Report(ctx context.Context, month string) (ports.FinanceReport, error)
	SetCategoryColor(ctx context.Context, name, color string) error
	RenameCategory(ctx context.Context, from, to string) error
	Palette() []string
	// Format renders a count of minor units for display. The browser never
	// does arithmetic on money; every amount it shows arrives as text.
	Format(minor int64, currency string) string
	FormatSigned(minor int64, currency string) string
	Currencies() []string
	DefaultCurrency() string
}

type financeEntryJSON struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
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
		ID: e.ID, Kind: e.Kind, Date: e.OccurredOn, Amount: svc.Format(e.AmountMinor, e.Currency), Currency: e.Currency,
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

func newFinanceListHandler(svc FinanceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		filter := ports.FinanceFilter{
			From: query.Get("from"), To: query.Get("to"), Kind: query.Get("kind"), Category: query.Get("category"),
			Query: query.Get("q"), Sort: query.Get("sort"), Order: query.Get("order"),
		}
		if raw := query.Get("limit"); raw != "" {
			limit, err := strconv.Atoi(raw)
			if err != nil || limit < 0 {
				writeWebError(w, http.StatusBadRequest, "limit must be a whole number")
				return
			}
			filter.Limit = limit
		}
		if raw := query.Get("offset"); raw != "" {
			offset, err := strconv.Atoi(raw)
			if err != nil || offset < 0 {
				writeWebError(w, http.StatusBadRequest, "offset must be a whole number")
				return
			}
			filter.Offset = offset
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
			Kind     string `json:"kind"`
			Currency string `json:"currency"`
			Date     string `json:"date"`
			Category string `json:"category"`
			Merchant string `json:"merchant"`
			Note     string `json:"note"`
		}
		if err := decodeStrictJSONBody(w, r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		// An entry typed into the panel is from the panel; the body has no say.
		entry, err := svc.Log(r.Context(), ports.FinanceInput{
			Amount: body.Amount, Kind: body.Kind, Currency: body.Currency, Date: body.Date, Category: body.Category,
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
			Kind     *string `json:"kind"`
			Currency *string `json:"currency"`
			Date     *string `json:"date"`
			Category *string `json:"category"`
			Merchant *string `json:"merchant"`
			Note     *string `json:"note"`
		}
		if err := decodeStrictJSONBody(w, r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		entry, err := svc.Update(r.Context(), r.PathValue("id"), ports.FinancePatch{
			Amount: body.Amount, Kind: body.Kind, Currency: body.Currency, Date: body.Date,
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

// percentChange is how far now has moved from before, to one place, or nil when
// there is nothing before to measure from. Movement is measured against the size
// of before, so a net that goes from -100 to 100 reads as up, not as a fall.
func percentChange(now, before int64) *float64 {
	if before == 0 {
		return nil
	}
	denominator := before
	if denominator < 0 {
		denominator = -denominator
	}
	pct := math.Round(float64(now-before)*1000/float64(denominator)) / 10
	return &pct
}

// ratePercent is a share of a whole as a percentage to one place, or nil when the
// whole is nothing.
func ratePercent(part, whole int64) *float64 {
	if whole <= 0 {
		return nil
	}
	pct := math.Round(float64(part)*1000/float64(whole)) / 10
	return &pct
}

type financeFlowJSON struct {
	Currency     string   `json:"currency"`
	Inflow       string   `json:"inflow"`
	Outflow      string   `json:"outflow"`
	Invested     string   `json:"invested"`
	Net          string   `json:"net"`
	NetInvested  string   `json:"net_invested"`
	Count        int      `json:"count"`
	AverageDay   string   `json:"average_day"`
	AverageShare float64  `json:"average_share"`
	SavingsRate  *float64 `json:"savings_rate"`
	Change       struct {
		Inflow   *float64 `json:"inflow"`
		Outflow  *float64 `json:"outflow"`
		Invested *float64 `json:"invested"`
		Net      *float64 `json:"net"`
	} `json:"change"`
}

// newFinanceSummaryHandler answers the Finance page's month. The service does the
// money; this only says it in text, sizes the bars, and works out how far each
// figure moved from the month before.
func newFinanceSummaryHandler(svc FinanceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		report, err := svc.Report(r.Context(), r.URL.Query().Get("month"))
		if err != nil {
			writeFinanceFailure(w, err)
			return
		}
		previous := map[string]ports.FinanceFlow{}
		for _, flow := range report.Previous {
			previous[flow.Currency] = flow
		}
		flows := make([]financeFlowJSON, 0, len(report.Flows))
		averageOf := map[string]int64{}
		largestDay := map[string]int64{}
		for _, row := range report.Days {
			largestDay[row.Currency] = max(largestDay[row.Currency], row.AmountMinor)
		}
		for _, flow := range report.Flows {
			before := previous[flow.Currency]
			out := financeFlowJSON{
				Currency: flow.Currency, Inflow: svc.Format(flow.Inflow, flow.Currency), Outflow: svc.Format(flow.Outflow, flow.Currency),
				Invested: svc.Format(flow.Invested, flow.Currency), Net: svc.FormatSigned(flow.Net(), flow.Currency),
				NetInvested: svc.FormatSigned(flow.NetInvested(), flow.Currency), Count: flow.Count,
				AverageDay: svc.Format(flow.DailyAverage, flow.Currency), SavingsRate: ratePercent(flow.Net(), flow.Inflow),
			}
			out.Change.Inflow = percentChange(flow.Inflow, before.Inflow)
			out.Change.Outflow = percentChange(flow.Outflow, before.Outflow)
			out.Change.Invested = percentChange(flow.Invested, before.Invested)
			out.Change.Net = percentChange(flow.Net(), before.Net())
			averageOf[flow.Currency] = flow.DailyAverage
			flows = append(flows, out)
		}
		// The strip and the average share one scale per currency: the largest day
		// or the average, whichever is bigger, so the average line never floats
		// above the bars.
		scale := map[string]int64{}
		for code := range averageOf {
			scale[code] = max(largestDay[code], averageOf[code])
		}
		for i := range flows {
			flows[i].AverageShare = shareOf(averageOf[flows[i].Currency], scale[flows[i].Currency])
		}

		spendingTotal := map[string]int64{}
		for _, line := range report.Categories {
			spendingTotal[line.Currency] += line.Amount
		}
		spending := make([]map[string]any, 0, len(report.Categories))
		for _, line := range report.Categories {
			spending = append(spending, map[string]any{
				"currency": line.Currency, "category": line.Category, "color": line.Color,
				"amount": svc.Format(line.Amount, line.Currency), "count": line.Count,
				"share":    shareOf(line.Amount, spendingTotal[line.Currency]),
				"previous": svc.Format(line.Previous, line.Currency), "change": percentChange(line.Amount, line.Previous),
			})
		}

		days := make([]map[string]any, 0, len(report.Days))
		for _, row := range report.Days {
			days = append(days, map[string]any{
				"currency": row.Currency, "day": row.Day, "amount": svc.Format(row.AmountMinor, row.Currency),
				"share": shareOf(row.AmountMinor, scale[row.Currency]),
			})
		}

		// A trend bar is sized against the largest inflow or outflow in its own
		// currency, so money in and money out share one scale and read as a pair.
		trendScale := map[string]int64{}
		for _, point := range report.Trend {
			trendScale[point.Currency] = max(trendScale[point.Currency], point.Inflow, point.Outflow)
		}
		trend := make([]map[string]any, 0, len(report.Trend))
		for _, point := range report.Trend {
			trend = append(trend, map[string]any{
				"month": point.Month, "currency": point.Currency,
				"inflow":        svc.Format(point.Inflow, point.Currency),
				"outflow":       svc.Format(point.Outflow, point.Currency),
				"invested":      svc.Format(point.Invested, point.Currency),
				"net":           svc.FormatSigned(ports.FinanceFlow{Inflow: point.Inflow, Outflow: point.Outflow, Invested: point.Invested}.Net(), point.Currency),
				"inflow_share":  shareOf(point.Inflow, trendScale[point.Currency]),
				"outflow_share": shareOf(point.Outflow, trendScale[point.Currency]),
			})
		}

		categories := make([]map[string]any, 0, len(report.AllCats))
		for _, category := range report.AllCats {
			categories = append(categories, map[string]any{"name": category.Name, "count": category.Count, "color": category.Color})
		}

		writeJSON(w, map[string]any{
			"month": report.Month, "from": report.From, "to": report.To,
			"currencies": svc.Currencies(), "default_currency": svc.DefaultCurrency(), "palette": svc.Palette(),
			"flows": flows, "trend": trend, "spending": spending, "days": days,
			"categories": categories, "insights": financeList(report.Insights),
		})
	}
}

// newFinanceCategoryHandler colours a category, renames it, or both. A rename
// that names an existing category merges the two, which is why the colour a
// rename carries goes to the name the category ends up with.
func newFinanceCategoryHandler(svc FinanceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Color  *string `json:"color"`
			Rename *string `json:"rename"`
		}
		if err := decodeStrictJSONBody(w, r, &body); err != nil || (body.Color == nil && body.Rename == nil) {
			writeWebError(w, http.StatusBadRequest, "send a colour, a new name, or both")
			return
		}
		name := r.PathValue("name")
		if body.Rename != nil {
			if err := svc.RenameCategory(r.Context(), name, *body.Rename); err != nil {
				writeFinanceFailure(w, err)
				return
			}
			name = *body.Rename
		}
		if body.Color != nil {
			if err := svc.SetCategoryColor(r.Context(), name, *body.Color); err != nil {
				writeFinanceFailure(w, err)
				return
			}
		}
		writeJSON(w, map[string]string{"name": name})
	}
}

// financeList keeps an empty list an empty array in the JSON rather than null.
func financeList[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
