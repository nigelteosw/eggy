package finance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nigelteosw/eggy/internal/core/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

// listLimit is how many entries one list call returns. The remainder is
// counted, not shown: a model reading fifty rows has what it needs, and a
// year of entries in one tool result would crowd out the turn.
const listLimit = 50

type tool struct {
	svc        *Service
	definition ports.ToolDefinition
}

// NewTool returns the one tool this plugin adds. Five actions share one schema
// because every core tool ships its schema on every model call: enabling
// finance costs one definition, not five.
//
// The effect is InternalTool, so normal mode does not ask before a write. That
// is the recorded exception in AGENTS.md: entries land only in the calling
// account's own records and nothing outside Eggy can observe them, and asking
// per logged coffee trains the owner to approve without reading. Strict still
// gates every call. Unprompted turns run on an explicit read-only allowlist
// that does not name this tool, so a heartbeat or schedule can never log.
func NewTool(svc *Service) ports.Tool {
	currencies, _ := json.Marshal(svc.Currencies())
	return tool{svc: svc, definition: ports.ToolDefinition{
		Name: "finance",
		Description: "Track the owner's spending. " +
			"log: record one expense (amount and category; currency defaults to " + svc.DefaultCurrency() + ", date to today). " +
			"update/delete: change or remove an entry by id. " +
			"list/summary: entries or totals for from..to (default this month). " +
			"Log a receipt's total, not each line, unless asked; set source=photo when reading an image; ask if the total, currency or date is unclear. " +
			"Always tell the owner what you logged, with its id, so a correction is an update. " +
			"Categories: food, groceries, transport, shopping, bills, entertainment, health, travel, other.",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"action":{"type":"string","enum":["log","update","delete","list","summary"]},` +
			`"id":{"type":"string"},` +
			`"amount":{"type":"string","description":"digits only, like 14.50"},` +
			`"currency":{"type":"string","enum":` + string(currencies) + `},` +
			`"date":{"type":"string","description":"YYYY-MM-DD"},` +
			`"category":{"type":"string"},` +
			`"merchant":{"type":"string"},` +
			`"note":{"type":"string"},` +
			`"source":{"type":"string","enum":["chat","photo"]},` +
			`"from":{"type":"string","description":"YYYY-MM-DD"},` +
			`"to":{"type":"string","description":"YYYY-MM-DD"}},` +
			`"required":["action"],"additionalProperties":false}`),
		Effect: ports.InternalTool(),
	}}
}

func (t tool) Definition() ports.ToolDefinition { return t.definition }

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (t tool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	// Text fields are pointers so an update can tell "clear the note" ("")
	// from "leave it alone" (absent). Strict decoding refuses any other key,
	// which is what keeps an account from being named in an argument.
	var in struct {
		Action   string  `json:"action"`
		ID       string  `json:"id"`
		Amount   *string `json:"amount"`
		Currency *string `json:"currency"`
		Date     *string `json:"date"`
		Category *string `json:"category"`
		Merchant *string `json:"merchant"`
		Note     *string `json:"note"`
		Source   *string `json:"source"`
		From     string  `json:"from"`
		To       string  `json:"to"`
	}
	if err := services.DecodeToolInput(raw, &in); err != nil {
		return nil, err
	}
	var (
		text string
		err  error
	)
	switch in.Action {
	case "log":
		text, err = t.log(ctx, in.Amount, in.Currency, in.Date, in.Category, in.Merchant, in.Note, in.Source)
	case "update":
		text, err = t.update(ctx, in.ID, ports.FinancePatch{Amount: in.Amount, Currency: in.Currency, Date: in.Date, Category: in.Category, Merchant: in.Merchant, Note: in.Note})
	case "delete":
		text, err = t.delete(ctx, in.ID)
	case "list":
		text, err = t.list(ctx, in.From, in.To, str(in.Category))
	case "summary":
		text, err = t.summary(ctx, in.From, in.To)
	default:
		err = errors.New("action must be log, update, delete, list or summary")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(text)
}

func (t tool) log(ctx context.Context, amount, currency, date, category, merchant, note, source *string) (string, error) {
	// The schema offers chat and photo only. A model must not be able to claim
	// an entry came from the panel, so the service's third source is refused
	// here rather than trusted.
	if s := strings.ToLower(strings.TrimSpace(str(source))); s == "panel" {
		return "", fmt.Errorf("%w: source must be chat or photo", ports.ErrFinanceInvalid)
	}
	entry, err := t.svc.Log(ctx, ports.FinanceInput{
		Amount: str(amount), Currency: str(currency), Date: str(date), Category: str(category),
		Merchant: str(merchant), Note: str(note), Source: str(source),
	})
	if err != nil {
		return "", err
	}
	return "Logged: " + t.line(entry), nil
}

func (t tool) update(ctx context.Context, id string, patch ports.FinancePatch) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", errors.New("id is required to update an entry")
	}
	entry, err := t.svc.Update(ctx, id, patch)
	if err != nil {
		return "", err
	}
	return "Updated: " + t.line(entry), nil
}

func (t tool) delete(ctx context.Context, id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", errors.New("id is required to delete an entry")
	}
	entry, err := t.svc.Delete(ctx, id)
	if err != nil {
		return "", err
	}
	return "Deleted: " + t.line(entry), nil
}

func (t tool) list(ctx context.Context, from, to, category string) (string, error) {
	from, to, err := t.svc.Range(from, to)
	if err != nil {
		return "", err
	}
	entries, total, err := t.svc.List(ctx, ports.FinanceFilter{From: from, To: to, Category: category, Limit: listLimit})
	if err != nil {
		return "", err
	}
	label := rangeLabel(from, to)
	if len(entries) == 0 {
		return "No entries " + label + ".", nil
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%d %s %s, newest first:", total, plural(total, "entry", "entries"), label)
	for _, entry := range entries {
		out.WriteString("\n" + t.line(entry))
	}
	if total > len(entries) {
		fmt.Fprintf(&out, "\n(%d more not shown; narrow from/to or category)", total-len(entries))
	}
	return out.String(), nil
}

func (t tool) summary(ctx context.Context, from, to string) (string, error) {
	from, to, err := t.svc.Range(from, to)
	if err != nil {
		return "", err
	}
	totals, err := t.svc.Summary(ctx, from, to)
	if err != nil {
		return "", err
	}
	label := rangeLabel(from, to)
	if len(totals.ByCategory) == 0 {
		return "No spending " + label + ".", nil
	}
	// Per currency, because nothing converts between them: a total across SGD
	// and JPY would be a number that means nothing.
	type group struct {
		minor      int64
		count      int
		categories []string
	}
	var order []string
	groups := map[string]*group{}
	for _, row := range totals.ByCategory {
		g, ok := groups[row.Currency]
		if !ok {
			g = &group{}
			groups[row.Currency] = g
			order = append(order, row.Currency)
		}
		g.minor += row.AmountMinor
		g.count += row.Count
		g.categories = append(g.categories, row.Category+" "+t.svc.Format(row.AmountMinor, row.Currency))
	}
	var out strings.Builder
	out.WriteString("Spending " + label + ":")
	for _, currency := range order {
		g := groups[currency]
		fmt.Fprintf(&out, "\n%s %s (%d %s): %s", currency, t.svc.Format(g.minor, currency), g.count, plural(g.count, "entry", "entries"), strings.Join(g.categories, ", "))
	}
	return out.String(), nil
}

// line is one entry on one line, id first, because the id is what the model
// needs to carry into an update.
func (t tool) line(e ports.FinanceEntry) string {
	parts := []string{e.ID, e.OccurredOn, e.Currency + " " + t.svc.Format(e.AmountMinor, e.Currency), e.Category}
	if e.Merchant != "" {
		parts = append(parts, e.Merchant)
	}
	if e.Note != "" {
		parts = append(parts, e.Note)
	}
	return strings.Join(parts, " | ")
}

func rangeLabel(from, to string) string {
	switch {
	case from != "" && to != "":
		return from + " to " + to
	case from != "":
		return "from " + from
	default:
		return "up to " + to
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
