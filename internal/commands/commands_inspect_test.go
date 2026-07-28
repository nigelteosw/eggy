package commands

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type stubDiagnostics struct {
	capabilities   ports.CapabilityReport
	contextReport  ports.ContextReport
	conversationID string
}

func (d *stubDiagnostics) CapabilityReport(context.Context) (ports.CapabilityReport, error) {
	return d.capabilities, nil
}

func (d *stubDiagnostics) ContextReport(_ context.Context, conversationID string) (ports.ContextReport, error) {
	d.conversationID = conversationID
	return d.contextReport, nil
}

type stubChanges struct{ changes []ports.Change }

func (c stubChanges) List(context.Context) ([]ports.Change, error) { return c.changes, nil }

func dispatchInput(t *testing.T, service *CommandService, input string) CommandResult {
	t.Helper()
	req, ok := ParseTelegramInput(catalogIndex, input)
	if !ok {
		t.Fatalf("%q did not match a catalog entry", input)
	}
	result, err := service.dispatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCapabilitiesRendersTheReport(t *testing.T) {
	service := &CommandService{diagnostics: &stubDiagnostics{capabilities: ports.CapabilityReport{
		ActiveModel: "deepseek-pro", ReasoningEffort: "high",
		Repositories: []string{"eggy"}, SelfRepository: "eggy",
		Integrations: []string{"web", "telegram"}, Tools: []string{"read_file", "terminal"},
		RepositoryCommitReady: true, CalendarEnabled: true,
	}}}
	rendered := dispatchInput(t, service, "/capabilities").RenderPlainText()
	for _, want := range []string{"deepseek-pro", "high", "eggy", "telegram", "read_file", "terminal"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("/capabilities output is missing %q:\n%s", want, rendered)
		}
	}
	// Push and pull-request readiness are false in the report, and the view
	// must say so rather than defaulting to the optimistic reading.
	if strings.Count(rendered, "unavailable") != 2 {
		t.Errorf("expected push and pull request to read unavailable:\n%s", rendered)
	}
}

func TestContextReportsSectionsAndLimits(t *testing.T) {
	service := &CommandService{diagnostics: &stubDiagnostics{contextReport: ports.ContextReport{
		Sections: []ports.ContextSection{
			{Name: "SOUL.md", Bytes: 400},
			{Name: "MEMORY.md", Bytes: 500, MaxBytes: 1000},
		},
		RecentMessages: 3, BudgetChars: 96000, RecentSteps: 16,
		OutputExcerptChars: 8192, MaxSteps: 500,
	}}}
	rendered := dispatchInput(t, service, "/context").RenderPlainText()
	for _, want := range []string{
		"SOUL.md", "MEMORY.md",
		"50% of 1000", // MEMORY.md against its write budget
		"900",         // the resident total
		"96000", "8192", "500",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("/context output is missing %q:\n%s", want, rendered)
		}
	}
}

// TestInspectionCommandsWithoutDiagnostics guards the tolerated-zero-value
// contract every other handler follows: an unwired dependency reports itself
// rather than panicking.
func TestInspectionCommandsWithoutDiagnostics(t *testing.T) {
	service := &CommandService{}
	for _, input := range []string{"/capabilities", "/context"} {
		result := dispatchInput(t, service, input)
		if result.State != ResultInfo {
			t.Errorf("%s with no diagnostics = %q, want an info result", input, result.State)
		}
	}
}

func TestRunsShowDetail(t *testing.T) {
	started := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	service := &CommandService{
		changes: stubChanges{[]ports.Change{{
			ID: "run-1", Repository: "eggy", Branch: "eggy/run-1", BaseRevision: "abc123",
			Phase: ports.PhaseRunning, Validation: "go test ./... passed",
			PullRequestURL: "https://github.com/acme/eggy/pull/7", PullRequestNumber: 7,
			StartedAt: started, UpdatedAt: started.Add(time.Minute),
		}}},
		now: func() time.Time { return started.Add(90 * time.Second) },
	}
	rendered := dispatchInput(t, service, "/runs show run-1").RenderPlainText()
	for _, want := range []string{"run-1", "eggy/run-1", "abc123", "running", "go test ./... passed", "1m30s", "#7", "owner"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("/runs show output is missing %q:\n%s", want, rendered)
		}
	}

	missing := dispatchInput(t, service, "/runs show run-9")
	if missing.State != ResultError {
		t.Errorf("unknown run = %q, want an error result", missing.State)
	}
	if usage := dispatchInput(t, service, "/runs show"); usage.State != ResultHelp {
		t.Errorf("/runs show without an id = %q, want usage help", usage.State)
	}
}

// TestRunsShowElapsedUsesFinishedAt pins that a finished run reports its own
// duration rather than one that keeps growing with the wall clock.
func TestRunsShowElapsedUsesFinishedAt(t *testing.T) {
	started := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	session := ports.Change{ID: "run-1", StartedAt: started, FinishedAt: started.Add(2 * time.Minute)}
	if got := elapsed(session, func() time.Time { return started.Add(time.Hour) }); got != "2m0s" {
		t.Errorf("elapsed = %q, want 2m0s", got)
	}
}
