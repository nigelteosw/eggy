package bootstrap

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
	googleadapter "github.com/nigelteosw/eggy/plugins/tools/google"
)

// An owner who never configured approvals at all must still get one: Eggy may
// read their mail unasked and may not send any. With no override configured
// there is nothing for bootstrap to say, and the tools' own classification is
// what decides.
func TestGoogleGatesEveryMutationByDefault(t *testing.T) {
	override, err := googleApprovals(config.GoogleConfig{Products: []string{"gmail", "calendar", "sheets"}})
	if err != nil {
		t.Fatal(err)
	}
	if override != nil {
		t.Fatalf("an unconfigured require_approval produced an override: %v", override)
	}
	tools, err := googleClassifiedTools(&googleadapter.Workspace{}, config.GoogleConfig{Products: []string{"gmail", "calendar", "sheets"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 {
		t.Fatalf("built %d tools", len(tools))
	}
	for _, tool := range tools {
		definition := tool.Definition()
		if !slices.Equal(definition.Effect.Mutations, googleadapter.Mutations()[definition.Name]) {
			t.Fatalf("%s declares %v, want every mutation %v", definition.Name, definition.Effect.Mutations, googleadapter.Mutations()[definition.Name])
		}
		// The reads are the other half of the claim, and the half that would
		// make the gate useless if it were wrong.
		for _, read := range googleadapter.Reads()[definition.Name] {
			if slices.Contains(definition.Effect.Mutations, read) {
				t.Fatalf("%s gates %q, which only reads", definition.Name, read)
			}
		}
	}
}

// Empty is a decision, absent is a default, and collapsing the two would
// silently re-gate an owner who turned the gates off. The override has to
// reach every Google tool, not just the ones an entry named.
func TestGoogleEmptyApprovalListGatesNothing(t *testing.T) {
	override, err := googleApprovals(config.GoogleConfig{RequireApproval: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if override == nil || len(override) != 0 {
		t.Fatalf("an explicitly empty require_approval produced %v, want an empty override rather than none", override)
	}
	tools, err := googleClassifiedTools(&googleadapter.Workspace{}, config.GoogleConfig{Products: []string{"gmail"}, RequireApproval: []string{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !tools[0].Definition().Effect.ReadOnly {
		t.Fatalf("gmail still asks after the owner turned the gates off: %+v", tools[0].Definition().Effect)
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

// The tools classify themselves, and the notice the gate adds has to name the
// actions that classification covers -- told the tool as a whole needs
// approval, a model stops using the actions that never did.
func TestGoogleToolsCarryTheirOwnClassification(t *testing.T) {
	tools := googleadapter.Tools(nil, []string{"calendar", "drive"}, nil)
	for _, tool := range tools {
		effect := tool.Definition().Effect
		if effect.ReadOnly || len(effect.Mutations) == 0 {
			t.Fatalf("%s declares no writes: %+v", tool.Definition().Name, effect)
		}
		gated := services.NewApprovalGatedToolIf(tool, nil, nil, services.RuleFor(tool.Definition()))
		description := gated.Definition().Description
		for _, mutation := range effect.Mutations {
			if !strings.Contains(description, mutation) {
				t.Fatalf("%s does not announce that %q asks: %q", tool.Definition().Name, mutation, description)
			}
		}
		if !strings.Contains(description, "require the owner's approval") {
			t.Fatalf("%s announces no gate: %q", tool.Definition().Name, description)
		}
	}
}

// A read-only tool takes no notice at all. It is still wrapped, because strict
// mode has to be able to close on it, but in every other mode it costs the
// owner no prompt bytes for a gate that will not fire.
func TestReadOnlyToolsAreWrappedSilently(t *testing.T) {
	tool := stubGoogleTool{name: "google_reader", effect: ports.ReadOnlyTool()}
	gated := services.NewApprovalGatedToolIf(tool, nil, nil, services.RuleFor(tool.Definition()))
	if gated.Definition().Description != tool.Definition().Description {
		t.Fatalf("a read-only tool was given a notice: %q", gated.Definition().Description)
	}
}

// require_approval overrides what the tools declare, in both directions: it
// can gate an action they call a read, and an empty list turns their own
// writes into calls that never ask.
func TestRequireApprovalOverridesTheDeclaredClassification(t *testing.T) {
	tool := googleadapter.Reclassified(stubGoogleTool{name: "google_calendar"}, []string{"list"})
	if !slices.Equal(tool.Definition().Effect.Mutations, []string{"list"}) {
		t.Fatalf("effect=%+v", tool.Definition().Effect)
	}
	silent := googleadapter.Reclassified(stubGoogleTool{name: "google_calendar"}, nil)
	if !silent.Definition().Effect.ReadOnly {
		t.Fatalf("an empty override left the tool gated: %+v", silent.Definition().Effect)
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

type stubGoogleTool struct {
	name   string
	effect ports.ToolEffect
}

func (t stubGoogleTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: t.name, Description: "does a thing", Schema: json.RawMessage(`{"type":"object"}`), Effect: t.effect}
}

func (t stubGoogleTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
