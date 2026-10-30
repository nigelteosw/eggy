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

// batchLimit is how many entries one log, update or delete call may carry: a
// month of receipts in one call, not a year of statements.
const batchLimit = 50

type tool struct {
	svc        *Service
	definition ports.ToolDefinition
}

// NewTool returns the one tool this extension adds. Five actions share one schema
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
		Description: "Track the owner's money. " +
			"log: amount and category; kind expense (default), income, invest or divest; currency defaults to " + svc.DefaultCurrency() + ", date to today. " +
			"update/delete: by id. Several at once: entries (log; update with id) or ids (delete), one call. " +
			"list/summary: from..to, default this month; list also takes query and offset. " +
			"Log a receipt's total unless asked; source=photo for an image; ask if total, currency or date is unclear. " +
			"Tell the owner what you logged, with ids. " +
			"Categories: food, groceries, transport, shopping, bills, entertainment, health, travel, other.",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"action":{"type":"string","enum":["log","update","delete","list","summary"]},` +
			`"id":{"type":"string"},` +
			`"amount":{"type":"string","description":"digits only, like 14.50"},` +
			`"currency":{"type":"string","enum":` + string(currencies) + `},` +
			`"date":{"type":"string","description":"YYYY-MM-DD"},` +
			`"kind":{"type":"string","enum":["expense","income","invest","divest"]},` +
			`"category":{"type":"string"},` +
			`"merchant":{"type":"string"},` +
			`"note":{"type":"string"},` +
			`"source":{"type":"string","enum":["chat","photo"]},` +
			`"from":{"type":"string","description":"YYYY-MM-DD"},` +
			`"to":{"type":"string","description":"YYYY-MM-DD"},` +
			`"query":{"type":"string"},` +
			`"offset":{"type":"integer","minimum":0},` +
			// Items take the top-level entry fields, which the description
			// says; repeating their schema here would double what every call
			// pays, and strict decoding still refuses any other key.
			`"entries":{"type":"array","maxItems":` + fmt.Sprint(batchLimit) + `,"items":{"type":"object","description":"id, amount, kind, currency, date, category, merchant, note, source"}},` +
			`"ids":{"type":"array","maxItems":` + fmt.Sprint(batchLimit) + `,"items":{"type":"string"}}},` +
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
		Action string `json:"action"`
		entryArgs
		From    string      `json:"from"`
		To      string      `json:"to"`
		Query   string      `json:"query"`
		Offset  int         `json:"offset"`
		Entries []entryArgs `json:"entries"`
		IDs     []string    `json:"ids"`
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
		text, err = t.batch(in.entryArgs, in.Entries, func(e entryArgs) (string, error) { return t.log(ctx, e) })
	case "update":
		text, err = t.batch(in.entryArgs, in.Entries, func(e entryArgs) (string, error) { return t.update(ctx, e.ID, e.patch()) })
	case "delete":
		ids := in.IDs
		if in.ID != "" {
			ids = append([]string{in.ID}, ids...)
		}
		entries := make([]entryArgs, len(ids))
		for i, id := range ids {
			entries[i].ID = id
		}
		text, err = t.batch(entryArgs{}, entries, func(e entryArgs) (string, error) { return t.delete(ctx, e.ID) })
	case "list":
		text, err = t.list(ctx, in.From, in.To, str(in.Kind), str(in.Category), in.Query, in.Offset)
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

// entryArgs is one entry as a call states it, either at the top level or as
// an item of entries. Text fields are pointers so an update can tell "clear
// the note" ("") from "leave it alone" (absent).
type entryArgs struct {
	ID       string  `json:"id"`
	Amount   *string `json:"amount"`
	Kind     *string `json:"kind"`
	Currency *string `json:"currency"`
	Date     *string `json:"date"`
	Category *string `json:"category"`
	Merchant *string `json:"merchant"`
	Note     *string `json:"note"`
	Source   *string `json:"source"`
}

func (e entryArgs) patch() ports.FinancePatch {
	return ports.FinancePatch{Amount: e.Amount, Kind: e.Kind, Currency: e.Currency, Date: e.Date, Category: e.Category, Merchant: e.Merchant, Note: e.Note}
}

func (e entryArgs) empty() bool { return e == entryArgs{} }

// batch runs one write per entry: the top-level fields when the call carries
// no entries, each item otherwise. Every entry is attempted and reported, so a
// bad line in a receipt costs that line, not the ones logged before it; the
// call fails only when nothing succeeded.
func (t tool) batch(single entryArgs, entries []entryArgs, write func(entryArgs) (string, error)) (string, error) {
	if len(entries) == 0 {
		return write(single)
	}
	if !single.empty() {
		return "", fmt.Errorf("%w: pass either entries or one entry's fields, not both", ports.ErrFinanceInvalid)
	}
	if len(entries) > batchLimit {
		return "", fmt.Errorf("%w: at most %d entries per call", ports.ErrFinanceInvalid, batchLimit)
	}
	lines := make([]string, 0, len(entries))
	failed := 0
	for i, entry := range entries {
		line, err := write(entry)
		if err != nil {
			failed++
			line = fmt.Sprintf("Failed item %d: %v", i+1, err)
		}
		lines = append(lines, line)
	}
	if failed == len(entries) {
		return "", errors.New(strings.Join(lines, "\n"))
	}
	return strings.Join(lines, "\n"), nil
}

func (t tool) log(ctx context.Context, in entryArgs) (string, error) {
	// The schema offers chat and photo only. A model must not be able to claim
	// an entry came from the panel, so the service's third source is refused
	// here rather than trusted.
	if s := strings.ToLower(strings.TrimSpace(str(in.Source))); s == "panel" {
		return "", fmt.Errorf("%w: source must be chat or photo", ports.ErrFinanceInvalid)
	}
	entry, err := t.svc.Log(ctx, ports.FinanceInput{
		Amount: str(in.Amount), Kind: str(in.Kind), Currency: str(in.Currency), Date: str(in.Date), Category: str(in.Category),
		Merchant: str(in.Merchant), Note: str(in.Note), Source: str(in.Source),
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

func (t tool) list(ctx context.Context, from, to, kind, category, query string, offset int) (string, error) {
	from, to, err := t.svc.Range(from, to)
	if err != nil {
		return "", err
	}
	entries, total, err := t.svc.List(ctx, ports.FinanceFilter{From: from, To: to, Kind: kind, Category: category, Query: query, Limit: listLimit, Offset: offset})
	if err != nil {
		return "", err
	}
	label := rangeLabel(from, to)
	if len(entries) == 0 {
		if offset > 0 && total > 0 {
			return fmt.Sprintf("No entries past offset %d; there are %d %s.", offset, total, label), nil
		}
		return "No entries " + label + ".", nil
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%d %s %s, newest first", total, plural(total, "entry", "entries"), label)
	if offset > 0 {
		fmt.Fprintf(&out, ", from #%d", offset+1)
	}
	out.WriteString(":")
	for _, entry := range entries {
		out.WriteString("\n" + t.line(entry))
	}
	if rest := total - offset - len(entries); rest > 0 {
		fmt.Fprintf(&out, "\n(%d more; pass offset=%d for the next page)", rest, offset+len(entries))
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
	flows := t.svc.flowsOf(totals)
	if len(flows) == 0 {
		return "No money moved " + label + ".", nil
	}
	// Per currency, because nothing converts between them: a total across SGD
	// and JPY would be a number that means nothing.
	spending := map[string][]string{}
	for _, row := range totals.ByCategory {
		if row.Kind == ports.FinanceKindExpense {
			spending[row.Currency] = append(spending[row.Currency], row.Category+" "+t.svc.Format(row.AmountMinor, row.Currency))
		}
	}
	var out strings.Builder
	out.WriteString("Money " + label + ":")
	for _, f := range flows {
		fmt.Fprintf(&out, "\n%s in %s, out %s, invested %s, net %s (%d %s)", f.Currency,
			t.svc.Format(f.Inflow, f.Currency), t.svc.Format(f.Outflow, f.Currency), t.svc.Format(f.Invested, f.Currency),
			t.svc.FormatSigned(f.Net(), f.Currency), f.Count, plural(f.Count, "entry", "entries"))
		if cats := spending[f.Currency]; len(cats) > 0 {
			out.WriteString("\n  spending: " + strings.Join(cats, ", "))
		}
	}
	return out.String(), nil
}

// line is one entry on one line, id first, because the id is what the model
// needs to carry into an update.
func (t tool) line(e ports.FinanceEntry) string {
	parts := []string{e.ID, e.OccurredOn, e.Currency + " " + t.svc.Format(e.AmountMinor, e.Currency)}
	if e.Kind != ports.FinanceKindExpense {
		parts = append(parts, e.Kind)
	}
	parts = append(parts, e.Category)
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
