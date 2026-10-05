package finance

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/approvals"
	"github.com/nigelteosw/eggy/internal/core/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

func newTool(t *testing.T) (ports.Tool, *Service) {
	t.Helper()
	svc, _ := newService(t, time.Date(2026, 10, 15, 4, 0, 0, 0, time.UTC))
	return NewTool(svc), svc
}

// run calls the tool as account "a" and returns the text it answers with.
func run(t *testing.T, tool ports.Tool, arguments string) (string, error) {
	t.Helper()
	raw, err := tool.Execute(asAccount("a"), json.RawMessage(arguments))
	if err != nil {
		return "", err
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		t.Fatalf("result %s is not a JSON string: %v", raw, err)
	}
	return text, nil
}

func TestDefinitionDeclaresItsOwnEffectAndCurrencyEnum(t *testing.T) {
	tool, _ := newTool(t)
	def := tool.Definition()
	if def.Name != "finance" {
		t.Fatalf("name = %q", def.Name)
	}
	if !reflect.DeepEqual(def.Effect, ports.InternalTool()) {
		t.Fatalf("effect = %+v, want InternalTool: entries are private to the calling account", def.Effect)
	}
	if def.TurnScoped {
		t.Fatal("an owner turn must be able to use finance")
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
		Required             []string `json:"required"`
		AdditionalProperties bool     `json:"additionalProperties"`
	}
	if err := json.Unmarshal(def.Schema, &schema); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	if !slices.Equal(schema.Properties["currency"].Enum, config.FinanceCurrencies) {
		t.Fatalf("currency enum = %v, want config.FinanceCurrencies", schema.Properties["currency"].Enum)
	}
	if !slices.Equal(schema.Properties["action"].Enum, []string{"log", "update", "delete", "list", "summary"}) {
		t.Fatalf("action enum = %v", schema.Properties["action"].Enum)
	}
	if !slices.Equal(schema.Properties["source"].Enum, []string{"chat", "photo"}) {
		t.Fatalf("source enum = %v: the model must not claim to be the panel", schema.Properties["source"].Enum)
	}
	if !slices.Equal(schema.Required, []string{"action"}) || schema.AdditionalProperties {
		t.Fatalf("required=%v additionalProperties=%v", schema.Required, schema.AdditionalProperties)
	}
}

// The schema ships on every model call while finance is enabled.
func TestDescriptionStaysSmall(t *testing.T) {
	tool, _ := newTool(t)
	def := tool.Definition()
	if n := len(def.Description); n > 700 {
		t.Fatalf("description is %d bytes; it is paid on every call", n)
	}
	if n := len(def.Schema); n > 1200 {
		t.Fatalf("schema is %d bytes; it is paid on every call", n)
	}
	if !strings.Contains(def.Description, "SGD") {
		t.Fatal("the description should name the default currency")
	}
}

func TestLogReportsTheEntryWithItsID(t *testing.T) {
	tool, _ := newTool(t)
	text, err := run(t, tool, `{"action":"log","amount":"14.50","category":"Food","merchant":"Ya Kun"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Logged", "id1", "2026-10-15", "SGD 14.50", "food", "Ya Kun"} {
		if !strings.Contains(text, want) {
			t.Errorf("log reply %q lacks %q", text, want)
		}
	}
}

func TestLogRecordsASourceAndRefusesThePanelSource(t *testing.T) {
	tool, svc := newTool(t)
	if _, err := run(t, tool, `{"action":"log","amount":"5","category":"food","source":"photo"}`); err != nil {
		t.Fatal(err)
	}
	got, _, _ := svc.List(asAccount("a"), ports.FinanceFilter{})
	if len(got) != 1 || got[0].Source != "photo" {
		t.Fatalf("entries = %+v", got)
	}
	if _, err := run(t, tool, `{"action":"log","amount":"5","category":"food","source":"panel"}`); err == nil {
		t.Fatal("the model logged as the panel")
	}
}

func TestUpdateAndDeleteByID(t *testing.T) {
	tool, _ := newTool(t)
	run(t, tool, `{"action":"log","amount":"14.50","category":"food"}`)
	text, err := run(t, tool, `{"action":"update","id":"id1","amount":"15.40","note":"with tip"}`)
	if err != nil || !strings.Contains(text, "Updated") || !strings.Contains(text, "15.40") || !strings.Contains(text, "with tip") {
		t.Fatalf("update = %q, %v", text, err)
	}
	text, err = run(t, tool, `{"action":"delete","id":"id1"}`)
	if err != nil || !strings.Contains(text, "Deleted") || !strings.Contains(text, "15.40") {
		t.Fatalf("delete = %q, %v", text, err)
	}
	if _, err := run(t, tool, `{"action":"delete","id":"id1"}`); err == nil {
		t.Fatal("deleting a missing entry succeeded")
	}
}

func TestUpdateAndDeleteNeedAnID(t *testing.T) {
	tool, _ := newTool(t)
	for _, args := range []string{`{"action":"update","amount":"1"}`, `{"action":"delete"}`} {
		if _, err := run(t, tool, args); err == nil || !strings.Contains(err.Error(), "id") {
			t.Errorf("%s: err = %v, want a refusal naming id", args, err)
		}
	}
}

func TestUpdateTouchesOnlyWhatWasSent(t *testing.T) {
	tool, svc := newTool(t)
	run(t, tool, `{"action":"log","amount":"14.50","category":"food","merchant":"Ya Kun","note":"toast"}`)
	// An empty string is a value, not an absence: it clears the note.
	if _, err := run(t, tool, `{"action":"update","id":"id1","note":""}`); err != nil {
		t.Fatal(err)
	}
	got, _, _ := svc.List(asAccount("a"), ports.FinanceFilter{})
	if got[0].Note != "" || got[0].Merchant != "Ya Kun" || got[0].AmountMinor != 1450 {
		t.Fatalf("entry = %+v", got[0])
	}
}

func TestListShowsEntriesAndCapsAtFiftyWithTheRemainder(t *testing.T) {
	tool, _ := newTool(t)
	for i := 0; i < 53; i++ {
		if _, err := run(t, tool, fmt.Sprintf(`{"action":"log","amount":"%d","category":"food"}`, i+1)); err != nil {
			t.Fatal(err)
		}
	}
	text, err := run(t, tool, `{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if rows := strings.Count(text, "\nid"); rows != 50 {
		t.Fatalf("listed %d rows, want 50:\n%s", rows, text)
	}
	if !strings.Contains(text, "3 more") {
		t.Fatalf("list does not say what it left out:\n%s", text)
	}
}

func TestListEmptyAndFiltered(t *testing.T) {
	tool, _ := newTool(t)
	text, err := run(t, tool, `{"action":"list"}`)
	if err != nil || !strings.Contains(text, "No entries") {
		t.Fatalf("empty list = %q, %v", text, err)
	}
	run(t, tool, `{"action":"log","amount":"5","category":"food","date":"2026-09-10"}`)
	run(t, tool, `{"action":"log","amount":"6","category":"bills","date":"2026-09-11"}`)
	text, _ = run(t, tool, `{"action":"list","from":"2026-09-01","to":"2026-09-30","category":"bills"}`)
	if !strings.Contains(text, "bills") || strings.Contains(text, "food") {
		t.Fatalf("filtered list = %q", text)
	}
}

func TestSummaryGroupsPerCurrencyThenCategory(t *testing.T) {
	svc, store := newService(t, time.Date(2026, 10, 15, 4, 0, 0, 0, time.UTC))
	tool := NewTool(svc)
	// The fake store sums nothing, so it hands back one row per entry; the
	// tool's job is to fold those per currency and say so clearly.
	ctx := asAccount("a")
	svc.Log(ctx, ports.FinanceInput{Amount: "30", Category: "food"})
	svc.Log(ctx, ports.FinanceInput{Amount: "15.20", Category: "transport"})
	svc.Log(ctx, ports.FinanceInput{Amount: "1200", Currency: "JPY", Category: "travel"})
	_ = store
	text, err := run(t, tool, `{"action":"summary"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2026-10-01 to 2026-10-31", "SGD 45.20 (2 entries)", "food 30.00", "transport 15.20", "JPY 1200 (1 entry)", "travel 1200"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary lacks %q:\n%s", want, text)
		}
	}
	if text, _ := run(t, tool, `{"action":"summary","from":"2020-01-01","to":"2020-01-31"}`); !strings.Contains(text, "No spending") {
		t.Errorf("empty summary = %q", text)
	}
}

func TestRefusesUnknownActionsFieldsAndBadInput(t *testing.T) {
	tool, _ := newTool(t)
	for name, args := range map[string]string{
		"unknown action":  `{"action":"refund"}`,
		"no action":       `{}`,
		"unknown field":   `{"action":"list","account":"b"}`,
		"trailing json":   `{"action":"list"} {"action":"list"}`,
		"not json":        `nope`,
		"bad amount":      `{"action":"log","amount":"$5","category":"food"}`,
		"no category":     `{"action":"log","amount":"5"}`,
		"bad currency":    `{"action":"log","amount":"5","category":"food","currency":"XYZ"}`,
		"numeric amount":  `{"action":"log","amount":14.5,"category":"food"}`,
		"inverted range":  `{"action":"list","from":"2026-10-31","to":"2026-10-01"}`,
		"bad date format": `{"action":"summary","from":"October"}`,
	} {
		if _, err := run(t, tool, args); err == nil {
			t.Errorf("%s: accepted %s", name, args)
		}
	}
}

// No argument can name the account: the schema has no such field and strict
// decoding refuses one, so the only account that can be touched is the one on
// the context.
func TestTheToolActsAsTheAccountOnTheContext(t *testing.T) {
	tool, svc := newTool(t)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`)); err == nil {
		t.Fatal("the tool ran without an account on the context")
	}
	run(t, tool, `{"action":"log","amount":"5","category":"food"}`)
	if _, total, _ := svc.List(asAccount("b"), ports.FinanceFilter{}); total != 0 {
		t.Fatal("account b can see account a's entry")
	}
}

type spyTool struct{ calls int }

func (s *spyTool) Definition() ports.ToolDefinition { return ports.ToolDefinition{Name: "finance"} }
func (s *spyTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	s.calls++
	return json.RawMessage(`"ran"`), nil
}

type spyRequester struct{ requests int }

func (s *spyRequester) Request(context.Context, approvals.Action, any, string) (approvals.Approval, error) {
	s.requests++
	return approvals.Approval{ID: "a1"}, nil
}

type fixedMode ports.ApprovalMode

func (m fixedMode) Mode(context.Context) (ports.ApprovalMode, error) {
	return ports.ApprovalMode(m), nil
}

// D6: logging a coffee must not ask the owner in normal mode, and strict --
// "ask me about everything" -- must still stop it.
func TestNormalModeRunsEveryActionWithoutAskingAndStrictAsks(t *testing.T) {
	tool, _ := newTool(t)
	rule := services.RuleFor(tool.Definition())
	for _, c := range []struct {
		mode              ports.ApprovalMode
		wantRan, wantAsks bool
	}{
		{ports.ModeNormal, true, false},
		{ports.ModeAuto, true, false},
		{ports.ModeStrict, false, true},
	} {
		for _, action := range []string{"log", "update", "delete", "list", "summary"} {
			spy, requester := &spyTool{}, &spyRequester{}
			gated := services.NewApprovalGatedToolIf(spy, requester, fixedMode(c.mode), rule)
			if _, err := gated.Execute(asAccount("a"), json.RawMessage(fmt.Sprintf(`{"action":%q}`, action))); err != nil {
				t.Fatal(err)
			}
			if (spy.calls == 1) != c.wantRan || (requester.requests == 1) != c.wantAsks {
				t.Errorf("%s/%s: ran=%v asked=%v, want ran=%v asked=%v", c.mode, action, spy.calls == 1, requester.requests == 1, c.wantRan, c.wantAsks)
			}
		}
	}
}
