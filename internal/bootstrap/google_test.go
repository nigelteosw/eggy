package bootstrap

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
	googleadapter "github.com/nigelteosw/eggy/plugins/tools/google"
)

// An owner who never configured approvals at all must still get one: Eggy may
// read their mail unasked and may not send any.
func TestGoogleGatesEveryMutationByDefault(t *testing.T) {
	gated, err := googleApprovals(config.GoogleConfig{Products: []string{"gmail", "calendar", "sheets"}})
	if err != nil {
		t.Fatal(err)
	}
	for tool, actions := range googleadapter.Mutations() {
		if !slices.Equal(gated[tool], actions) {
			t.Fatalf("%s gated=%v, want every mutation %v", tool, gated[tool], actions)
		}
	}
	// The reads are the other half of the claim, and the half that would make
	// the gate useless if it were wrong.
	for _, read := range []string{"search", "get", "labels"} {
		if slices.Contains(gated["google_gmail"], read) {
			t.Fatalf("reading mail requires approval by default: %v", gated["google_gmail"])
		}
	}
}

// Empty is a decision, absent is a default, and collapsing the two would
// silently re-gate an owner who turned the gates off.
func TestGoogleEmptyApprovalListGatesNothing(t *testing.T) {
	gated, err := googleApprovals(config.GoogleConfig{RequireApproval: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(gated) != 0 {
		t.Fatalf("an explicitly empty require_approval gated %v", gated)
	}
}

func TestGoogleApprovalListIsCheckedAgainstTheRealActions(t *testing.T) {
	gated, err := googleApprovals(config.GoogleConfig{RequireApproval: []string{"gmail.send", "calendar.delete", "drive.*"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(gated["google_gmail"], []string{"send"}) || !slices.Equal(gated["google_calendar"], []string{"delete"}) {
		t.Fatalf("gated=%v", gated)
	}
	// Gating a product whole is available, but only when asked for explicitly.
	if !slices.Equal(gated["google_drive"], []string{googleadapter.GateAll}) {
		t.Fatalf("drive gated=%v", gated["google_drive"])
	}

	// Whole-product gating cannot be narrowed by a later entry, whichever
	// order the two arrive in.
	for _, entries := range [][]string{{"gmail.*", "gmail.send"}, {"gmail.send", "gmail.*"}} {
		gated, err := googleApprovals(config.GoogleConfig{RequireApproval: entries})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(gated["google_gmail"], googleadapter.GateAll) {
			t.Fatalf("%v narrowed a whole-product gate to %v", entries, gated["google_gmail"])
		}
	}

	for entry, want := range map[string]string{
		"gmail.snd":     "has no action",
		"gmail":         "must name an action",
		"telegram.send": "names no Google product",
		"":              "names no Google product",
	} {
		_, err := googleApprovals(config.GoogleConfig{RequireApproval: []string{entry}})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("require_approval %q: error=%v, want %q", entry, err, want)
		}
	}
	if _, err := googleApprovals(config.GoogleConfig{RequireApproval: []string{"gmail.send", "gmail.send"}}); err == nil {
		t.Fatal("a duplicate require_approval entry was accepted")
	}
}

// Wrapping is per tool: a product with nothing gated keeps its description
// byte for byte, so the mechanism costs an ungated owner no prompt bytes.
func TestGoogleGatesOnlyTheToolsThatNeedIt(t *testing.T) {
	tools := []ports.Tool{stubGoogleTool{name: "google_calendar"}, stubGoogleTool{name: "google_drive"}}
	before := tools[1].Definition().Description
	wrapped := gateGoogleTools(tools, map[string][]string{"google_calendar": {"create", "delete"}}, nil, nil)

	calendar := wrapped[0].Definition().Description
	if !strings.Contains(calendar, "create, delete") || !strings.Contains(calendar, "require the owner's approval") {
		t.Fatalf("calendar description does not name the gated actions: %q", calendar)
	}
	// Naming them matters as much as gating them: told the tool as a whole
	// needs approval, a model stops using the actions that never did.
	if strings.Contains(calendar, "list") {
		t.Fatalf("the notice lists an action that is not gated: %q", calendar)
	}
	if wrapped[1].Definition().Description != before {
		t.Fatalf("an ungated tool was rewritten: %q", wrapped[1].Definition().Description)
	}
}

// Mutations decides what is gated by default, and it is maintained by hand
// next to the switch statements. An action missing from both Reads and
// Mutations is, most likely, an ungated write -- so the two together must
// account for every action the schema advertises, and this is what says so.
func TestEveryGoogleActionIsClassifiedAsReadOrWrite(t *testing.T) {
	mutations := googleadapter.Mutations()
	// Nothing may be called both, or "is this gated" has two answers.
	for tool, reads := range googleadapter.Reads() {
		for _, read := range reads {
			if slices.Contains(mutations[tool], read) {
				t.Fatalf("%s %q is listed as both a read and a mutation", tool, read)
			}
		}
	}
	for _, tool := range googleadapter.Tools(nil, []string{"gmail", "calendar", "drive", "docs", "sheets", "contacts"}, nil) {
		name := tool.Definition().Name
		var schema struct {
			Properties struct {
				Action struct {
					Enum []string `json:"enum"`
				} `json:"action"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.Definition().Schema, &schema); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		declared := googleadapter.Actions()[name]
		if !slices.Equal(slices.Sorted(slices.Values(declared)), slices.Sorted(slices.Values(schema.Properties.Action.Enum))) {
			t.Fatalf("%s advertises %v but Actions() says %v", name, schema.Properties.Action.Enum, declared)
		}
		for _, action := range mutations[name] {
			if !slices.Contains(declared, action) {
				t.Fatalf("%s is gated on %q, which it does not accept", name, action)
			}
		}
	}
}

type stubGoogleTool struct{ name string }

func (t stubGoogleTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: t.name, Description: "does a thing", Schema: json.RawMessage(`{"type":"object"}`)}
}

func (t stubGoogleTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
