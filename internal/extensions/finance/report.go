package finance

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nigelteosw/eggy/internal/ports"
)

// trendMonths is how many months the trend chart shows, the month on screen
// included.
const trendMonths = 6

// insightThreshold is how far a category must move, in percent, before it is
// worth a sentence. Smaller movements are noise on a page that already shows
// every number.
const insightThreshold = 20

// Report assembles everything the Finance page draws for one month: flows for
// the month and the one before, six months of trend, spending by category with
// last month beside it, the day strip, and up to three insights. Every amount is
// minor units, so the panel formats it and nothing here rounds money.
//
// An empty month is the owner's current one.
func (s *Service) Report(ctx context.Context, month string) (ports.FinanceReport, error) {
	var (
		from, to string
		err      error
	)
	if month == "" {
		from, to, err = s.Range("", "")
	} else {
		from, to, err = s.MonthBounds(month)
	}
	if err != nil {
		return ports.FinanceReport{}, err
	}
	start, err := time.Parse(dateLayout, from)
	if err != nil {
		return ports.FinanceReport{}, err
	}
	prevFrom, prevTo, err := s.MonthBounds(start.AddDate(0, -1, 0).Format("2006-01"))
	if err != nil {
		return ports.FinanceReport{}, err
	}
	trendFrom, _, err := s.MonthBounds(start.AddDate(0, -(trendMonths - 1), 0).Format("2006-01"))
	if err != nil {
		return ports.FinanceReport{}, err
	}
	totals, err := s.store.FinanceTotals(ctx, from, to)
	if err != nil {
		return ports.FinanceReport{}, err
	}
	previous, err := s.store.FinanceTotals(ctx, prevFrom, prevTo)
	if err != nil {
		return ports.FinanceReport{}, err
	}
	months, err := s.store.FinanceMonths(ctx, trendFrom, to)
	if err != nil {
		return ports.FinanceReport{}, err
	}
	categories, err := s.Categories(ctx)
	if err != nil {
		return ports.FinanceReport{}, err
	}
	colors := map[string]string{}
	for _, c := range categories {
		colors[c.Name] = c.Color
	}

	report := ports.FinanceReport{Month: from[:7], From: from, To: to, AllCats: categories}
	report.Flows = s.flowsOf(totals)
	report.Previous = s.flowsOf(previous)
	// A month that has not started has no days behind it, so it has no daily
	// average to report rather than the whole month's outflow.
	if elapsed := s.elapsedDays(from, to); elapsed > 0 {
		for i := range report.Flows {
			report.Flows[i].DailyAverage = report.Flows[i].Outflow / elapsed
		}
	}
	report.Trend = s.trendOf(months, start)
	report.Categories = s.categoryLines(totals, previous, colors)
	for _, row := range totals.ByDay {
		if row.Kind == ports.FinanceKindExpense {
			report.Days = append(report.Days, row)
		}
	}
	report.Insights = s.insights(report)
	return report, nil
}

// orderedCurrencies is the default first, then the configured order.
func (s *Service) orderedCurrencies() []string {
	order := []string{s.opts.Default}
	for _, code := range s.currencies {
		if code != s.opts.Default {
			order = append(order, code)
		}
	}
	return order
}

// flowsOf sums each currency's money by kind. Only currencies with entries
// appear, in orderedCurrencies' order.
func (s *Service) flowsOf(totals ports.FinanceTotals) []ports.FinanceFlow {
	byCode := map[string]ports.FinanceFlow{}
	for _, row := range totals.ByCategory {
		flow := byCode[row.Currency]
		flow.Currency = row.Currency
		flow.Count += row.Count
		switch row.Kind {
		case ports.FinanceKindIncome:
			flow.Inflow += row.AmountMinor
		case ports.FinanceKindDivest:
			flow.Inflow += row.AmountMinor
			flow.Divested += row.AmountMinor
		case ports.FinanceKindExpense:
			flow.Outflow += row.AmountMinor
		case ports.FinanceKindInvest:
			flow.Invested += row.AmountMinor
		}
		byCode[row.Currency] = flow
	}
	flows := []ports.FinanceFlow{}
	for _, code := range s.orderedCurrencies() {
		if flow, ok := byCode[code]; ok {
			flows = append(flows, flow)
		}
	}
	return flows
}

// elapsedDays is how many days of the period have happened, so a daily average
// in the middle of the month is not diluted by days still to come. It is never
// less than one, which keeps the division safe for a month not yet begun.
// elapsedDays counts the days of a month that have begun, up to today. It is 0
// for a month that has not started yet.
func (s *Service) elapsedDays(from, to string) int64 {
	start, _ := time.Parse(dateLayout, from)
	end := to
	if today := s.today(); today < end {
		end = today
	}
	last, _ := time.Parse(dateLayout, end)
	if last.Before(start) {
		return 0
	}
	return int64(last.Sub(start).Hours()/24) + 1
}

// trendOf lays out the trend window ending at the month on screen, oldest first,
// with a zero for every month that had nothing. Only currencies with entries in
// the window appear.
func (s *Service) trendOf(months []ports.FinanceMonthTotal, end time.Time) []ports.FinanceTrendPoint {
	type key struct{ currency, month string }
	sums := map[key]*ports.FinanceTrendPoint{}
	present := map[string]bool{}
	for _, row := range months {
		present[row.Currency] = true
		k := key{row.Currency, row.Month}
		point := sums[k]
		if point == nil {
			point = &ports.FinanceTrendPoint{Month: row.Month, Currency: row.Currency}
			sums[k] = point
		}
		switch row.Kind {
		case ports.FinanceKindIncome, ports.FinanceKindDivest:
			point.Inflow += row.AmountMinor
		case ports.FinanceKindExpense:
			point.Outflow += row.AmountMinor
		case ports.FinanceKindInvest:
			point.Invested += row.AmountMinor
		}
	}
	var points []ports.FinanceTrendPoint
	for _, code := range s.orderedCurrencies() {
		if !present[code] {
			continue
		}
		for i := trendMonths - 1; i >= 0; i-- {
			month := end.AddDate(0, -i, 0).Format("2006-01")
			if point := sums[key{code, month}]; point != nil {
				points = append(points, *point)
			} else {
				points = append(points, ports.FinanceTrendPoint{Month: month, Currency: code})
			}
		}
	}
	return points
}

// categoryLines is spending by category for the month, each beside what it cost
// the month before. The biggest spend in each currency comes first.
func (s *Service) categoryLines(totals, previous ports.FinanceTotals, colors map[string]string) []ports.FinanceCategoryLine {
	type key struct{ currency, category string }
	before := map[key]int64{}
	for _, row := range previous.ByCategory {
		if row.Kind == ports.FinanceKindExpense {
			before[key{row.Currency, row.Category}] += row.AmountMinor
		}
	}
	lines := []ports.FinanceCategoryLine{}
	for _, row := range totals.ByCategory {
		if row.Kind != ports.FinanceKindExpense {
			continue
		}
		color, ok := colors[row.Category]
		if !ok {
			color = defaultColor(row.Category)
		}
		lines = append(lines, ports.FinanceCategoryLine{
			Currency: row.Currency, Category: row.Category, Color: color,
			Amount: row.AmountMinor, Previous: before[key{row.Currency, row.Category}], Count: row.Count,
		})
	}
	order := s.orderedCurrencies()
	slices.SortStableFunc(lines, func(a, b ports.FinanceCategoryLine) int {
		if c := slices.Index(order, a.Currency) - slices.Index(order, b.Currency); c != 0 {
			return c
		}
		if a.Amount != b.Amount {
			if a.Amount > b.Amount {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Category, b.Category)
	})
	return lines
}

// insights are at most three plain sentences about the default currency: whether
// the owner kept money, which category moved most, and how much went into
// investments. Each is worked out from the numbers and says nothing it cannot
// show.
func (s *Service) insights(r ports.FinanceReport) []string {
	def := s.opts.Default
	var out []string
	flow, ok := findFlow(r.Flows, def)
	if !ok {
		return nil
	}
	if flow.Inflow > 0 {
		if net := flow.Net(); net >= 0 {
			out = append(out, fmt.Sprintf("You kept %d%% of what came in", net*100/flow.Inflow))
		} else {
			out = append(out, fmt.Sprintf("Spending and investing ran %s %s over income", def, s.Format(-net, def)))
		}
	}
	var mover *ports.FinanceCategoryLine
	var moved int64
	for i, line := range r.Categories {
		if line.Currency != def || line.Previous == 0 {
			continue
		}
		delta := line.Amount - line.Previous
		if abs(delta)*100 < insightThreshold*line.Previous {
			continue
		}
		if mover == nil || abs(delta) > abs(moved) {
			mover, moved = &r.Categories[i], delta
		}
	}
	if mover != nil {
		pct := int64(math.Round(float64(abs(moved)) * 100 / float64(mover.Previous)))
		direction := "up"
		if moved < 0 {
			direction = "down"
		}
		out = append(out, fmt.Sprintf("%s is %s %d%% on last month", capitalise(mover.Category), direction, pct))
	}
	if flow.Inflow > 0 && flow.Invested > 0 {
		out = append(out, fmt.Sprintf("You put %d%% of what came in into investments", flow.Invested*100/flow.Inflow))
	}
	return out[:min(len(out), 3)]
}

func findFlow(flows []ports.FinanceFlow, currency string) (ports.FinanceFlow, bool) {
	for _, flow := range flows {
		if flow.Currency == currency {
			return flow, true
		}
	}
	return ports.FinanceFlow{}, false
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func capitalise(word string) string {
	first, size := utf8.DecodeRuneInString(word)
	return string(unicode.ToUpper(first)) + word[size:]
}
