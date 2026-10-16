package finance

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// seed logs entries for account "a" through the service, so every test starts
// from what the write path would have stored.
func seed(t *testing.T, svc *Service, entries ...ports.FinanceInput) {
	t.Helper()
	for _, in := range entries {
		if _, err := svc.Log(asAccount("a"), in); err != nil {
			t.Fatalf("log %+v: %v", in, err)
		}
	}
}

// A month that has not started has no daily average: the whole month's outflow
// divided by zero days would read as a daily figure it is not.
func TestReportHasNoDailyAverageForAMonthThatHasNotStarted(t *testing.T) {
	now := time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC)
	svc, _ := newService(t, now)
	seed(t, svc, ports.FinanceInput{Amount: "1000", Category: "food", Date: "2026-11-03"})
	if got := flowFor(report(t, svc, "2026-11").Flows, "SGD").DailyAverage; got != 0 {
		t.Fatalf("daily average = %d for a future month, want 0", got)
	}
}

func report(t *testing.T, svc *Service, month string) ports.FinanceReport {
	t.Helper()
	got, err := svc.Report(asAccount("a"), month)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func flowFor(flows []ports.FinanceFlow, currency string) ports.FinanceFlow {
	for _, f := range flows {
		if f.Currency == currency {
			return f
		}
	}
	return ports.FinanceFlow{}
}

var october = time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC)

func TestLogRecordsTheKindAndDefaultsToSpending(t *testing.T) {
	svc, _ := newService(t, october)
	got, err := svc.Log(asAccount("a"), ports.FinanceInput{Amount: "5000", Kind: " Income ", Category: "salary"})
	if err != nil || got.Kind != ports.FinanceKindIncome {
		t.Fatalf("income = %+v err=%v", got, err)
	}
	got, err = svc.Log(asAccount("a"), ports.FinanceInput{Amount: "3", Category: "food"})
	if err != nil || got.Kind != ports.FinanceKindExpense {
		t.Fatalf("default = %+v err=%v, want expense", got, err)
	}
	if _, err := svc.Log(asAccount("a"), ports.FinanceInput{Amount: "3", Kind: "refund", Category: "food"}); !errors.Is(err, ports.ErrFinanceInvalid) {
		t.Fatalf("unknown kind err = %v, want invalid", err)
	}
}

func TestUpdateChangesTheKindAndRefusesUnknownOnes(t *testing.T) {
	svc, _ := newService(t, october)
	seed(t, svc, ports.FinanceInput{Amount: "100", Category: "stocks"})
	kind := ports.FinanceKindInvest
	got, err := svc.Update(asAccount("a"), "id1", ports.FinancePatch{Kind: &kind})
	if err != nil || got.Kind != ports.FinanceKindInvest {
		t.Fatalf("update = %+v err=%v", got, err)
	}
	bad := "gift"
	if _, err := svc.Update(asAccount("a"), "id1", ports.FinancePatch{Kind: &bad}); !errors.Is(err, ports.ErrFinanceInvalid) {
		t.Fatalf("unknown kind update err = %v", err)
	}
}

func TestListFiltersByKindAndRefusesUnknownOnes(t *testing.T) {
	svc, _ := newService(t, october)
	seed(t, svc,
		ports.FinanceInput{Amount: "10", Category: "food"},
		ports.FinanceInput{Amount: "500", Kind: "income", Category: "salary"},
	)
	entries, total, err := svc.List(asAccount("a"), ports.FinanceFilter{Kind: ports.FinanceKindIncome})
	if err != nil || total != 1 || len(entries) != 1 || entries[0].Category != "salary" {
		t.Fatalf("income list = %+v total=%d err=%v", entries, total, err)
	}
	if _, _, err := svc.List(asAccount("a"), ports.FinanceFilter{Kind: "gift"}); !errors.Is(err, ports.ErrFinanceInvalid) {
		t.Fatalf("unknown kind list err = %v", err)
	}
}

func TestReportSplitsMoneyByKind(t *testing.T) {
	svc, _ := newService(t, october)
	seed(t, svc,
		ports.FinanceInput{Amount: "10", Category: "food"},
		ports.FinanceInput{Amount: "50", Kind: "income", Category: "salary"},
		ports.FinanceInput{Amount: "20", Kind: "invest", Category: "stocks"},
		ports.FinanceInput{Amount: "5", Kind: "divest", Category: "stocks"},
	)
	got := report(t, svc, "2026-10")
	flow := flowFor(got.Flows, "SGD")
	if flow.Inflow != 5500 || flow.Outflow != 1000 || flow.Invested != 2000 || flow.Divested != 500 {
		t.Fatalf("flow = %+v", flow)
	}
	if flow.Net() != 2500 || flow.NetInvested() != 1500 {
		t.Fatalf("net = %d net invested = %d, want 2500 and 1500", flow.Net(), flow.NetInvested())
	}
	if len(got.Categories) != 1 || got.Categories[0].Category != "food" {
		t.Fatalf("spending categories = %+v, want food only", got.Categories)
	}
}

func TestReportComparesEachCategoryWithLastMonth(t *testing.T) {
	svc, _ := newService(t, october)
	seed(t, svc,
		ports.FinanceInput{Amount: "8", Category: "food", Date: "2026-09-12"},
		ports.FinanceInput{Amount: "10", Category: "food", Date: "2026-10-02"},
	)
	got := report(t, svc, "2026-10")
	if len(got.Categories) != 1 || got.Categories[0].Amount != 1000 || got.Categories[0].Previous != 800 {
		t.Fatalf("food = %+v, want 1000 against 800", got.Categories)
	}
	if flowFor(got.Previous, "SGD").Outflow != 800 {
		t.Fatalf("previous = %+v, want outflow 800", got.Previous)
	}
}

func TestReportTrendHasSixMonthsAndFillsGaps(t *testing.T) {
	svc, _ := newService(t, october)
	seed(t, svc,
		ports.FinanceInput{Amount: "3", Category: "food", Date: "2026-04-30"}, // outside the window
		ports.FinanceInput{Amount: "3", Category: "food", Date: "2026-05-01"},
		ports.FinanceInput{Amount: "1", Kind: "income", Category: "salary", Date: "2026-10-01"},
	)
	got := report(t, svc, "2026-10")
	var sgd []ports.FinanceTrendPoint
	for _, p := range got.Trend {
		if p.Currency == "SGD" {
			sgd = append(sgd, p)
		}
	}
	if len(sgd) != 6 {
		t.Fatalf("trend = %+v, want six months", sgd)
	}
	if sgd[0].Month != "2026-05" || sgd[0].Outflow != 300 || sgd[5].Month != "2026-10" || sgd[5].Inflow != 100 {
		t.Fatalf("trend ends = %+v ... %+v", sgd[0], sgd[5])
	}
	if sgd[1].Outflow != 0 || sgd[1].Inflow != 0 {
		t.Fatalf("gap month = %+v, want zeros", sgd[1])
	}
}

func TestReportDailyAverageDividesByDaysSoFar(t *testing.T) {
	now := time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC) // 12:00 in Singapore on the 10th
	svc, _ := newService(t, now)
	seed(t, svc, ports.FinanceInput{Amount: "1000", Category: "food", Date: "2026-10-03"})
	if got := flowFor(report(t, svc, "").Flows, "SGD").DailyAverage; got != 10000 {
		t.Fatalf("daily average = %d, want SGD 1000.00 over 10 days (10000 a day in cents)", got)
	}
}

func TestReportInsightsSpeakToTheDefaultCurrency(t *testing.T) {
	svc, _ := newService(t, october)
	seed(t, svc,
		ports.FinanceInput{Amount: "8", Category: "food", Date: "2026-09-12"},
		ports.FinanceInput{Amount: "10", Category: "food", Date: "2026-10-02"},
		ports.FinanceInput{Amount: "50", Kind: "income", Category: "salary", Date: "2026-10-01"},
	)
	insights := report(t, svc, "2026-10").Insights
	if len(insights) > 3 {
		t.Fatalf("insights = %v, want at most three", insights)
	}
	for _, want := range []string{"You kept 80% of what came in", "Food is up 25% on last month"} {
		if !slices.Contains(insights, want) {
			t.Fatalf("insights = %v, want %q", insights, want)
		}
	}
}

func TestReportInsightsSayWhenSpendingRanOverIncome(t *testing.T) {
	svc, _ := newService(t, october)
	seed(t, svc,
		ports.FinanceInput{Amount: "10", Kind: "income", Category: "salary", Date: "2026-10-01"},
		ports.FinanceInput{Amount: "15", Category: "food", Date: "2026-10-02"},
	)
	insights := report(t, svc, "2026-10").Insights
	if !slices.ContainsFunc(insights, func(s string) bool { return strings.Contains(s, "ran SGD 5.00 over income") }) {
		t.Fatalf("insights = %v, want the overspend named", insights)
	}
}

func TestCategoryColoursDefaultUntilChosen(t *testing.T) {
	svc, _ := newService(t, october)
	seed(t, svc, ports.FinanceInput{Amount: "1", Category: "food"}, ports.FinanceInput{Amount: "1", Category: "transport"})
	if err := svc.SetCategoryColor(asAccount("a"), "food", "rose"); err != nil {
		t.Fatal(err)
	}
	categories, err := svc.Categories(asAccount("a"))
	if err != nil {
		t.Fatal(err)
	}
	colors := map[string]string{}
	for _, c := range categories {
		colors[c.Name] = c.Color
	}
	if colors["food"] != "rose" {
		t.Fatalf("food = %q, want rose", colors["food"])
	}
	if !slices.Contains(palette, colors["transport"]) {
		t.Fatalf("transport default = %q, want a palette colour", colors["transport"])
	}
	if colors["transport"] != defaultColor("transport") {
		t.Fatal("default colour is not stable")
	}
}

func TestSetCategoryColorRefusesColoursOutsidePalette(t *testing.T) {
	svc, _ := newService(t, october)
	if err := svc.SetCategoryColor(asAccount("a"), "food", "#ff0000"); !errors.Is(err, ports.ErrFinanceInvalid) {
		t.Fatalf("hex colour err = %v, want invalid", err)
	}
}

func TestRenameCategoryMergesAndRefusesUnknownSources(t *testing.T) {
	svc, _ := newService(t, october)
	seed(t, svc, ports.FinanceInput{Amount: "1", Category: "food"})
	if err := svc.RenameCategory(asAccount("a"), "food", "Food"); !errors.Is(err, ports.ErrFinanceInvalid) {
		t.Fatalf("rename to itself err = %v", err)
	}
	if err := svc.RenameCategory(asAccount("a"), "travel", "trips"); !errors.Is(err, ports.ErrFinanceInvalid) {
		t.Fatalf("rename of an unused category err = %v", err)
	}
	if err := svc.RenameCategory(asAccount("a"), "food", "Eating Out"); err != nil {
		t.Fatal(err)
	}
	entries, _, _ := svc.List(asAccount("a"), ports.FinanceFilter{})
	if len(entries) != 1 || entries[0].Category != "eating out" {
		t.Fatalf("after rename = %+v", entries)
	}
}

func TestReportRefusesWithoutAnAccount(t *testing.T) {
	svc, _ := newService(t, october)
	if _, err := svc.Report(context.Background(), ""); err == nil {
		t.Fatal("Report without a principal succeeded")
	}
}
