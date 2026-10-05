package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// fakeFinance is the panel's view of the finance service, with canned answers
// and a record of what the routes passed in. It captures the account from the
// context because that is the only way a route may say who is asking.
type fakeFinance struct {
	entries []ports.FinanceEntry
	total   int
	totals  ports.FinanceTotals
	err     error

	account   string
	filter    ports.FinanceFilter
	logged    ports.FinanceInput
	patchedID string
	patch     ports.FinancePatch
	deletedID string
	from, to  string
}

func (f *fakeFinance) note(ctx context.Context) {
	if p, err := ports.PrincipalFromContext(ctx); err == nil {
		f.account = p.AccountID
	}
}

func (f *fakeFinance) Log(ctx context.Context, in ports.FinanceInput) (ports.FinanceEntry, error) {
	f.note(ctx)
	f.logged = in
	if f.err != nil {
		return ports.FinanceEntry{}, f.err
	}
	return ports.FinanceEntry{ID: "new1", OccurredOn: "2026-10-02", AmountMinor: 1450, Currency: "SGD", Category: in.Category, Source: in.Source}, nil
}

func (f *fakeFinance) Update(ctx context.Context, id string, patch ports.FinancePatch) (ports.FinanceEntry, error) {
	f.note(ctx)
	f.patchedID, f.patch = id, patch
	if f.err != nil {
		return ports.FinanceEntry{}, f.err
	}
	return ports.FinanceEntry{ID: id, OccurredOn: "2026-10-02", AmountMinor: 1540, Currency: "SGD", Category: "food"}, nil
}

func (f *fakeFinance) Delete(ctx context.Context, id string) (ports.FinanceEntry, error) {
	f.note(ctx)
	f.deletedID = id
	if f.err != nil {
		return ports.FinanceEntry{}, f.err
	}
	return ports.FinanceEntry{ID: id, OccurredOn: "2026-10-02", AmountMinor: 990, Currency: "SGD", Category: "health"}, nil
}

func (f *fakeFinance) List(ctx context.Context, filter ports.FinanceFilter) ([]ports.FinanceEntry, int, error) {
	f.note(ctx)
	f.filter = filter
	return f.entries, f.total, f.err
}

func (f *fakeFinance) Summary(ctx context.Context, from, to string) (ports.FinanceTotals, error) {
	f.note(ctx)
	f.from, f.to = from, to
	return f.totals, f.err
}

func (f *fakeFinance) MonthBounds(month string) (string, string, error) {
	if month == "2026-10" {
		return "2026-10-01", "2026-10-31", nil
	}
	return "", "", fmt.Errorf("%w: month %q must look like 2026-10", ports.ErrFinanceInvalid, month)
}

func (f *fakeFinance) Range(from, to string) (string, string, error) {
	if from == "" && to == "" {
		return "2026-10-01", "2026-10-31", nil
	}
	return from, to, nil
}

func (f *fakeFinance) Format(minor int64, currency string) string {
	if currency == "JPY" {
		return fmt.Sprint(minor)
	}
	return fmt.Sprintf("%d.%02d", minor/100, minor%100)
}

func (f *fakeFinance) Currencies() []string    { return []string{"SGD", "JPY", "USD"} }
func (f *fakeFinance) DefaultCurrency() string { return "SGD" }

func financeHandler(t *testing.T, finance FinanceService) (http.Handler, *http.Cookie) {
	t.Helper()
	webConfig := testWebConfig(t, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	if finance != nil {
		webConfig.Finance = finance
	}
	handler := NewWebHandler("", webConfig)
	return handler, webLoginCookie(t, handler)
}

func call(t *testing.T, handler http.Handler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	attachSession(request, cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decode(t *testing.T, response *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), into); err != nil {
		t.Fatalf("decode %q: %v", response.Body.String(), err)
	}
}

func TestSessionReportsTheFinanceFeatureOnlyWhenWired(t *testing.T) {
	for _, c := range []struct {
		name    string
		finance FinanceService
		want    []string
	}{{"disabled", nil, []string{}}, {"enabled", &fakeFinance{}, []string{"finance"}}} {
		handler, cookie := financeHandler(t, c.finance)
		response := call(t, handler, cookie, http.MethodGet, "/api/session", "")
		var body struct {
			Features []string `json:"features"`
		}
		decode(t, response, &body)
		if body.Features == nil || !slices.Equal(body.Features, c.want) {
			t.Errorf("%s: features = %#v, want %v (an empty list, never null)", c.name, body.Features, c.want)
		}
	}
}

func TestFinanceRoutesAreAbsentWhenDisabled(t *testing.T) {
	handler, cookie := financeHandler(t, nil)
	for _, c := range [][2]string{
		{http.MethodGet, "/api/finance/entries"}, {http.MethodPost, "/api/finance/entries"},
		{http.MethodPatch, "/api/finance/entries/x"}, {http.MethodDelete, "/api/finance/entries/x"},
		{http.MethodGet, "/api/finance/summary"},
	} {
		if response := call(t, handler, cookie, c[0], c[1], `{}`); response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want it unmounted", c[0], c[1], response.Code)
		}
	}
}

func TestFinanceRoutesRequireASession(t *testing.T) {
	handler, _ := financeHandler(t, &fakeFinance{})
	for _, c := range [][2]string{
		{http.MethodGet, "/api/finance/entries"}, {http.MethodPost, "/api/finance/entries"},
		{http.MethodPatch, "/api/finance/entries/x"}, {http.MethodDelete, "/api/finance/entries/x"},
		{http.MethodGet, "/api/finance/summary"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(c[0], c[1], strings.NewReader(`{}`)))
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d, want 401", c[0], c[1], response.Code)
		}
	}
}

func TestFinanceWritesNeedTheCSRFToken(t *testing.T) {
	fake := &fakeFinance{}
	handler, cookie := financeHandler(t, fake)
	for _, c := range [][3]string{
		{http.MethodPost, "/api/finance/entries", `{"amount":"1","category":"food"}`},
		{http.MethodPatch, "/api/finance/entries/x", `{"amount":"1"}`},
		{http.MethodDelete, "/api/finance/entries/x", ``},
	} {
		request := httptest.NewRequest(c[0], c[1], strings.NewReader(c[2]))
		request.AddCookie(cookie) // the cookie alone, without the header the panel's script adds
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s without CSRF = %d, want 403", c[0], c[1], response.Code)
		}
	}
	if fake.logged != (ports.FinanceInput{}) || fake.patchedID != "" || fake.deletedID != "" {
		t.Fatal("a request without the CSRF token reached the service")
	}
}

func TestListPassesTheFilterAndFormatsAmounts(t *testing.T) {
	fake := &fakeFinance{total: 3, entries: []ports.FinanceEntry{
		{ID: "e1", OccurredOn: "2026-10-02", AmountMinor: 1450, Currency: "SGD", Category: "food", Merchant: "Ya Kun", Note: "toast", Source: "photo"},
		{ID: "e2", OccurredOn: "2026-10-01", AmountMinor: 1200, Currency: "JPY", Category: "travel", Source: "chat"},
	}}
	handler, cookie := financeHandler(t, fake)
	response := call(t, handler, cookie, http.MethodGet, "/api/finance/entries?from=2026-10-01&to=2026-10-31&category=food&limit=25", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.filter != (ports.FinanceFilter{From: "2026-10-01", To: "2026-10-31", Category: "food", Limit: 25}) {
		t.Fatalf("filter = %+v", fake.filter)
	}
	if fake.account != "42" {
		t.Fatalf("service saw account %q, want the session's 42", fake.account)
	}
	var body struct {
		Entries []map[string]string `json:"entries"`
		Total   int                 `json:"total"`
	}
	decode(t, response, &body)
	if body.Total != 3 || len(body.Entries) != 2 {
		t.Fatalf("body = %+v", body)
	}
	first := body.Entries[0]
	want := map[string]string{"id": "e1", "date": "2026-10-02", "amount": "14.50", "currency": "SGD", "category": "food", "merchant": "Ya Kun", "note": "toast", "source": "photo"}
	for k, v := range want {
		if first[k] != v {
			t.Errorf("entry[%s] = %q, want %q", k, first[k], v)
		}
	}
	if body.Entries[1]["amount"] != "1200" {
		t.Errorf("a JPY amount = %q, want no decimals", body.Entries[1]["amount"])
	}
}

func TestListRefusesAnUnreadableLimit(t *testing.T) {
	handler, cookie := financeHandler(t, &fakeFinance{})
	for _, limit := range []string{"abc", "-1", "1.5"} {
		if response := call(t, handler, cookie, http.MethodGet, "/api/finance/entries?limit="+limit, ""); response.Code != http.StatusBadRequest {
			t.Errorf("limit=%s status = %d, want 400", limit, response.Code)
		}
	}
}

func TestListWithNoEntriesIsAnEmptyArray(t *testing.T) {
	handler, cookie := financeHandler(t, &fakeFinance{})
	response := call(t, handler, cookie, http.MethodGet, "/api/finance/entries", "")
	if !strings.Contains(response.Body.String(), `"entries":[]`) {
		t.Fatalf("body = %s, want an empty array rather than null", response.Body.String())
	}
}

func TestCreateRecordsAsThePanelForTheSessionAccount(t *testing.T) {
	fake := &fakeFinance{}
	handler, cookie := financeHandler(t, fake)
	response := call(t, handler, cookie, http.MethodPost, "/api/finance/entries",
		`{"amount":"14.50","currency":"SGD","date":"2026-10-02","category":"food","merchant":"Ya Kun","note":"toast"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	want := ports.FinanceInput{Amount: "14.50", Currency: "SGD", Date: "2026-10-02", Category: "food", Merchant: "Ya Kun", Note: "toast", Source: "panel"}
	if fake.logged != want {
		t.Fatalf("logged %+v, want %+v", fake.logged, want)
	}
	if fake.account != "42" {
		t.Fatalf("account = %q", fake.account)
	}
	var body map[string]string
	decode(t, response, &body)
	if body["id"] != "new1" || body["amount"] != "14.50" {
		t.Fatalf("body = %v", body)
	}
}

// Neither the account nor the source is the body's to name: the account is the
// session's, and an entry typed into the panel is from the panel.
func TestCreateRefusesFieldsAClientMustNotChoose(t *testing.T) {
	fake := &fakeFinance{}
	handler, cookie := financeHandler(t, fake)
	for _, body := range []string{
		`{"amount":"1","category":"food","account_id":"someone-else"}`,
		`{"amount":"1","category":"food","source":"photo"}`,
		`{"amount":"1","category":"food","id":"chosen"}`,
		`not json`,
		``,
	} {
		if response := call(t, handler, cookie, http.MethodPost, "/api/finance/entries", body); response.Code != http.StatusBadRequest {
			t.Errorf("body %q status = %d, want 400", body, response.Code)
		}
	}
	if fake.logged != (ports.FinanceInput{}) {
		t.Fatalf("a refused request reached the service: %+v", fake.logged)
	}
}

func TestPatchSendsOnlyTheFieldsPresent(t *testing.T) {
	fake := &fakeFinance{}
	handler, cookie := financeHandler(t, fake)
	response := call(t, handler, cookie, http.MethodPatch, "/api/finance/entries/e1", `{"amount":"15.40","note":""}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.patchedID != "e1" {
		t.Fatalf("id = %q", fake.patchedID)
	}
	p := fake.patch
	if p.Amount == nil || *p.Amount != "15.40" || p.Note == nil || *p.Note != "" {
		t.Fatalf("patch = %+v, want amount and an explicit empty note", p)
	}
	if p.Currency != nil || p.Date != nil || p.Category != nil || p.Merchant != nil {
		t.Fatalf("patch set fields the body did not send: %+v", p)
	}
	for _, body := range []string{`{"source":"panel"}`, `{"account_id":"b"}`, `{"id":"other"}`} {
		if response := call(t, handler, cookie, http.MethodPatch, "/api/finance/entries/e1", body); response.Code != http.StatusBadRequest {
			t.Errorf("patch body %q status = %d, want 400", body, response.Code)
		}
	}
}

func TestDeleteReturnsWhatItRemoved(t *testing.T) {
	fake := &fakeFinance{}
	handler, cookie := financeHandler(t, fake)
	response := call(t, handler, cookie, http.MethodDelete, "/api/finance/entries/e9", "")
	if response.Code != http.StatusOK || fake.deletedID != "e9" {
		t.Fatalf("status=%d deleted=%q", response.Code, fake.deletedID)
	}
	var body map[string]string
	decode(t, response, &body)
	if body["id"] != "e9" || body["amount"] != "9.90" {
		t.Fatalf("body = %v", body)
	}
}

func TestErrorsMapToTheStatusThatSaysWhoseFaultItWas(t *testing.T) {
	for _, c := range []struct {
		err        error
		status     int
		wantTitle  string
		notInTitle string
	}{
		{fmt.Errorf("%w: amount %q must be digits only", ports.ErrFinanceInvalid, "$5"), http.StatusBadRequest, `amount "$5" must be digits only`, "invalid finance input"},
		{ports.ErrFinanceEntryNotFound, http.StatusNotFound, "not found", ""},
		{fmt.Errorf("disk on fire: /secret/path"), http.StatusInternalServerError, "could not", "secret"},
	} {
		fake := &fakeFinance{err: c.err}
		handler, cookie := financeHandler(t, fake)
		for _, r := range [][3]string{
			{http.MethodPost, "/api/finance/entries", `{"amount":"1","category":"food"}`},
			{http.MethodPatch, "/api/finance/entries/e1", `{"amount":"1"}`},
			{http.MethodDelete, "/api/finance/entries/e1", ``},
		} {
			response := call(t, handler, cookie, r[0], r[1], r[2])
			if response.Code != c.status {
				t.Errorf("%v on %s: status = %d, want %d", c.err, r[0], response.Code, c.status)
			}
			var body struct {
				Title string `json:"title"`
			}
			decode(t, response, &body)
			if !strings.Contains(body.Title, c.wantTitle) || (c.notInTitle != "" && strings.Contains(body.Title, c.notInTitle)) {
				t.Errorf("%v: title = %q, want it to contain %q and not %q", c.err, body.Title, c.wantTitle, c.notInTitle)
			}
		}
	}
}

func summaryFake() *fakeFinance {
	return &fakeFinance{totals: ports.FinanceTotals{
		ByCategory: []ports.FinanceCategoryTotal{
			{Currency: "JPY", Category: "travel", AmountMinor: 1200, Count: 1},
			{Currency: "SGD", Category: "food", AmountMinor: 3000, Count: 2},
			{Currency: "SGD", Category: "transport", AmountMinor: 1520, Count: 1},
		},
		ByDay: []ports.FinanceDayTotal{
			{Currency: "JPY", Day: "2026-10-02", AmountMinor: 1200},
			{Currency: "SGD", Day: "2026-10-01", AmountMinor: 3000},
			{Currency: "SGD", Day: "2026-10-02", AmountMinor: 1520},
		},
	}}
}

type summaryBody struct {
	Month           string   `json:"month"`
	From            string   `json:"from"`
	To              string   `json:"to"`
	Currencies      []string `json:"currencies"`
	DefaultCurrency string   `json:"default_currency"`
	Totals          []struct {
		Currency string `json:"currency"`
		Amount   string `json:"amount"`
		Count    int    `json:"count"`
	} `json:"totals"`
	ByCategory []struct {
		Currency string  `json:"currency"`
		Category string  `json:"category"`
		Amount   string  `json:"amount"`
		Count    int     `json:"count"`
		Share    float64 `json:"share"`
	} `json:"by_category"`
	ByDay []struct {
		Currency string  `json:"currency"`
		Day      string  `json:"day"`
		Amount   string  `json:"amount"`
		Share    float64 `json:"share"`
	} `json:"by_day"`
}

func TestSummaryTotalsPerCurrencyWithTheDefaultFirst(t *testing.T) {
	fake := summaryFake()
	handler, cookie := financeHandler(t, fake)
	response := call(t, handler, cookie, http.MethodGet, "/api/finance/summary?month=2026-10", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.from != "2026-10-01" || fake.to != "2026-10-31" || fake.account != "42" {
		t.Fatalf("summary asked %s..%s as %q", fake.from, fake.to, fake.account)
	}
	var body summaryBody
	decode(t, response, &body)
	if body.Month != "2026-10" || body.From != "2026-10-01" || body.To != "2026-10-31" || body.DefaultCurrency != "SGD" || !slices.Equal(body.Currencies, []string{"SGD", "JPY", "USD"}) {
		t.Fatalf("header = %+v", body)
	}
	if len(body.Totals) != 2 || body.Totals[0].Currency != "SGD" || body.Totals[0].Amount != "45.20" || body.Totals[0].Count != 3 ||
		body.Totals[1].Currency != "JPY" || body.Totals[1].Amount != "1200" || body.Totals[1].Count != 1 {
		t.Fatalf("totals = %+v, want SGD 45.20 (3) then JPY 1200 (1)", body.Totals)
	}
}

// The page draws bars from share, so no money arithmetic happens in the
// browser: each bar is sized against the largest in its own currency.
func TestSummaryCarriesBarSharesPerCurrency(t *testing.T) {
	handler, cookie := financeHandler(t, summaryFake())
	response := call(t, handler, cookie, http.MethodGet, "/api/finance/summary?month=2026-10", "")
	var body summaryBody
	decode(t, response, &body)
	shares := map[string]float64{}
	for _, row := range body.ByCategory {
		shares[row.Currency+"/"+row.Category] = row.Share
	}
	if shares["SGD/food"] != 1 || shares["SGD/transport"] != 0.5067 || shares["JPY/travel"] != 1 {
		t.Fatalf("category shares = %v", shares)
	}
	days := map[string]float64{}
	for _, row := range body.ByDay {
		days[row.Currency+"/"+row.Day] = row.Share
	}
	if days["SGD/2026-10-01"] != 1 || days["SGD/2026-10-02"] != 0.5067 || days["JPY/2026-10-02"] != 1 {
		t.Fatalf("day shares = %v", days)
	}
	if body.ByCategory[0].Amount != "45.20" && body.ByCategory[0].Amount != "30.00" && body.ByCategory[0].Amount != "1200" {
		t.Fatalf("by_category amounts are not formatted: %+v", body.ByCategory[0])
	}
}

func TestSummaryDefaultsToTheCurrentMonthAndRefusesABadOne(t *testing.T) {
	fake := summaryFake()
	handler, cookie := financeHandler(t, fake)
	if response := call(t, handler, cookie, http.MethodGet, "/api/finance/summary", ""); response.Code != http.StatusOK {
		t.Fatalf("no month: status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.from != "2026-10-01" || fake.to != "2026-10-31" {
		t.Fatalf("default range = %s..%s", fake.from, fake.to)
	}
	if response := call(t, handler, cookie, http.MethodGet, "/api/finance/summary?month=October", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("bad month status = %d, want 400", response.Code)
	}
}

func TestSummaryOfNothingIsEmptyArrays(t *testing.T) {
	handler, cookie := financeHandler(t, &fakeFinance{})
	body := call(t, handler, cookie, http.MethodGet, "/api/finance/summary?month=2026-10", "").Body.String()
	for _, want := range []string{`"totals":[]`, `"by_category":[]`, `"by_day":[]`} {
		if !strings.Contains(body, want) {
			t.Errorf("body %s lacks %s", body, want)
		}
	}
}

func TestTheFinancePageIsAnApplicationRoute(t *testing.T) {
	handler := NewWebHandler("", WebUIConfig{})
	for _, path := range []string{"/finance", "/finance/"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Errorf("%s status = %d, want the SPA entrypoint", path, response.Code)
		}
	}
}

func TestFinanceConfigSectionRoundTrips(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	handler := NewWebHandler(path, testWebConfig(t, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)))
	cookie := webLoginCookie(t, handler)

	get := func() map[string]string {
		response := call(t, handler, cookie, http.MethodGet, "/api/config/finance", "")
		if response.Code != http.StatusOK {
			t.Fatalf("get status=%d body=%s", response.Code, response.Body.String())
		}
		var body struct {
			Fields []struct{ Label, Value string } `json:"fields"`
		}
		decode(t, response, &body)
		fields := map[string]string{}
		for _, f := range body.Fields {
			fields[strings.ToLower(f.Label)] = f.Value
		}
		return fields
	}

	fields := get()
	if fields["enabled"] != "false" || fields["currency"] != "SGD" || fields["currencies"] != strings.Join(config.FinanceCurrencies, ",") {
		t.Fatalf("defaults = %v", fields)
	}

	response := call(t, handler, cookie, http.MethodPost, "/api/config/finance", `{"enabled":"true","currency":"usd"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("set status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "estart") {
		t.Fatalf("a finance save must say it applies on restart: %s", response.Body.String())
	}
	fields = get()
	if fields["enabled"] != "true" || fields["currency"] != "USD" {
		t.Fatalf("after save = %v", fields)
	}
	saved, _ := os.ReadFile(path)
	if !strings.Contains(string(saved), "currency: USD") {
		t.Fatalf("config.yaml lacks the saved currency:\n%s", saved)
	}
}

func TestFinanceConfigRefusesAnUnsupportedCurrencyAndLeavesTheFile(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	before, _ := os.ReadFile(path)
	handler := NewWebHandler(path, testWebConfig(t, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)))
	cookie := webLoginCookie(t, handler)
	response := call(t, handler, cookie, http.MethodPost, "/api/config/finance", `{"enabled":"true","currency":"XYZ"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a refused save changed config.yaml")
	}
}
