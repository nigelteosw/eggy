package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/ports"
)

type diagnosticsContextStore struct{ ports.AgentContext }

func (s diagnosticsContextStore) Load(context.Context) (ports.AgentContext, error) {
	return s.AgentContext, nil
}
func (diagnosticsContextStore) AddEntry(context.Context, ports.ContextDocument, string) error {
	return nil
}
func (diagnosticsContextStore) ReplaceEntry(context.Context, ports.ContextDocument, string, string) error {
	return nil
}
func (diagnosticsContextStore) RemoveEntry(context.Context, ports.ContextDocument, string) error {
	return nil
}

type diagnosticsRuntime struct{ alias, effort string }

func (r diagnosticsRuntime) SelectedModel(context.Context) (string, error)   { return r.alias, nil }
func (r diagnosticsRuntime) ReasoningEffort(context.Context) (string, error) { return r.effort, nil }

type diagnosticsSkills struct{ skills []ports.SkillSummary }

func (s diagnosticsSkills) Enabled(context.Context) ([]ports.SkillSummary, error) {
	return s.skills, nil
}

type diagnosticsConversation struct{ messages []ports.Message }

func (c diagnosticsConversation) RecentMessages(context.Context, string) ([]ports.Message, error) {
	return c.messages, nil
}

type diagnosticsTool struct{ definition ports.ToolDefinition }

func (t diagnosticsTool) Definition() ports.ToolDefinition { return t.definition }
func (diagnosticsTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}

func diagnosticsFixture() *Diagnostics {
	loop := agent.NewSelectedLoop(nil, agent.StaticTools{
		diagnosticsTool{ports.ToolDefinition{Name: "read_file", Description: "read", Schema: json.RawMessage(`{"type":"object"}`)}},
		diagnosticsTool{ports.ToolDefinition{Name: "terminal", Description: "run", Schema: json.RawMessage(`{"type":"object"}`)}},
	}, agent.ContextPolicy{BudgetChars: 96000, RecentSteps: 16, OutputExcerptChars: 8192, MaxSteps: 500})
	return NewDiagnostics(DiagnosticsOptions{
		Context: diagnosticsContextStore{ports.AgentContext{
			Soul: "soul body", User: "user body", Memory: "memory body",
			UserMaxBytes: 100, MemoryMaxBytes: 200,
		}},
		Runtime:      diagnosticsRuntime{alias: "deepseek-pro", effort: "high"},
		Skills:       diagnosticsSkills{[]ports.SkillSummary{{Name: "ship", Description: "how to ship"}}},
		Conversation: diagnosticsConversation{[]ports.Message{{Content: "hello"}, {Content: "world!"}}},
		Loop:         loop,
		Manifest:     agent.CapabilityManifest{CalendarEnabled: true, SelfRepository: "eggy", RepositoryCommitReady: true},
		Policy:       agent.ContextPolicy{BudgetChars: 96000, RecentSteps: 16, OutputExcerptChars: 8192, MaxSteps: 500},
		Integrations: []string{"web", "telegram"},
	})
}

// TestContextReportMeasuresEverySection asserts the report names every
// injected section and measures it, rather than reporting a single opaque
// total: attribution is the whole point of /context.
func TestContextReportMeasuresEverySection(t *testing.T) {
	report, err := diagnosticsFixture().ContextReport(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	sections := map[string]ports.ContextSection{}
	for _, section := range report.Sections {
		sections[section.Name] = section
	}
	for _, name := range []string{"hard runtime policy", "capability manifest", "skills index", "SOUL.md", "USER.md", "MEMORY.md", "temporal context", "tool schemas", "recent history"} {
		section, ok := sections[name]
		if !ok {
			t.Fatalf("report is missing section %q", name)
		}
		if section.Bytes <= 0 {
			t.Errorf("section %q measured %d bytes", name, section.Bytes)
		}
	}
	// The two agent-curated documents are the only budgeted sections, and
	// their budgets come from the store rather than from a constant here.
	if got := sections["USER.md"].MaxBytes; got != 100 {
		t.Errorf("USER.md budget = %d, want 100", got)
	}
	if got := sections["MEMORY.md"].MaxBytes; got != 200 {
		t.Errorf("MEMORY.md budget = %d, want 200", got)
	}
	if got := sections["SOUL.md"].MaxBytes; got != 0 {
		t.Errorf("SOUL.md is unbudgeted but reported %d", got)
	}
	if got := sections["recent history"].Bytes; got != len("hello")+len("world!") {
		t.Errorf("recent history = %d bytes, want %d", got, len("hello")+len("world!"))
	}
	if report.RecentMessages != 2 {
		t.Errorf("recent messages = %d, want 2", report.RecentMessages)
	}
	if report.ResidentBytes() <= sections["hard runtime policy"].Bytes {
		t.Error("resident total should exceed any single section")
	}
	if report.BudgetChars != 96000 || report.RecentSteps != 16 || report.OutputExcerptChars != 8192 || report.MaxSteps != 500 {
		t.Errorf("report did not carry the active context policy: %+v", report)
	}
}

// TestContextReportIsDeterministic pins the property the roadmap item asks
// for: two calls with unchanged state produce identical numbers, so nothing
// in the report depends on the clock.
func TestContextReportIsDeterministic(t *testing.T) {
	diagnostics := diagnosticsFixture()
	first, err := diagnostics.ContextReport(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := diagnostics.ContextReport(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.ResidentBytes() != second.ResidentBytes() {
		t.Errorf("resident bytes changed between calls: %d then %d", first.ResidentBytes(), second.ResidentBytes())
	}
}

// TestCapabilityReportReflectsWiring checks the report describes what was
// wired and what state holds, and that unavailable readiness stays false.
func TestCapabilityReportReflectsWiring(t *testing.T) {
	report, err := diagnosticsFixture().CapabilityReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.ActiveModel != "deepseek-pro" || report.ReasoningEffort != "high" {
		t.Errorf("model = %q effort = %q", report.ActiveModel, report.ReasoningEffort)
	}
	if got := report.Tools; len(got) != 2 || got[0] != "read_file" || got[1] != "terminal" {
		t.Errorf("tools = %v", got)
	}
	if !report.CalendarEnabled || report.SelfRepository != "eggy" {
		t.Errorf("report dropped manifest capability: %+v", report)
	}
	// No state store is wired, so no repository is configured, and shipping
	// readiness must not claim otherwise.
	if report.RepositoryPushReady || report.PullRequestReady {
		t.Errorf("unwired shipping reported ready: %+v", report)
	}
	if len(report.Integrations) != 2 {
		t.Errorf("integrations = %v", report.Integrations)
	}
}

// TestCapabilityReportCarriesNoSecret is the exposure guard the roadmap item
// names: the report is built from names and flags only, so no secret value
// can reach it even when one is present in the documents it measures.
func TestCapabilityReportCarriesNoSecret(t *testing.T) {
	report, err := diagnosticsFixture().CapabilityReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"soul body", "user body", "memory body", "how to ship"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("capability report leaked context content %q", forbidden)
		}
	}
}
