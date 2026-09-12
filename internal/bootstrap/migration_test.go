package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

// writeOldHome lays out the artifacts a home carried before machine state
// consolidated into eggy.db: approvals and mode in state.json, one job per
// file under cron/, and sealed grants in auth.json.
func writeOldHome(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "cron"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"state.json": `{
  "schema_version": 5,
  "version": 12,
  "approval_mode": "strict",
  "approvals": {"a1": {"id": "a1", "action": "tool_call", "status": "pending", "created_at": "2026-09-06T10:00:00Z", "expires_at": "2026-09-06T10:30:00Z"}},
  "processed_events": {"telegram:99": "2026-09-06T10:00:00Z"},
  "agent": {"selected_model": "deepseek-pro", "reasoning_effort": "high"}
}`,
		"auth.json":         `{"version":1,"sections":{"mcp":{"railway":{"version":1,"ciphertext":"sealed-mcp-grant"}}}}`,
		"cron/morning.yaml": "id: morning\nkind: recurring\ninstruction: status\ncron: '0 9 * * *'\nnext_run: 2026-09-07T09:00:00Z\nenabled: true\n",
		"cron/once.yaml":    "id: once\nkind: exact\ninstruction: check oven\nnext_run: 2026-09-06T18:00:00Z\nenabled: true\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// This is the migration's acceptance test: a home written by the previous
// release boots, and everything an owner would notice missing -- the pending
// approval, the strictness they chose, the deduplication window, their
// schedules, and their OAuth grant -- is still there, from SQLite, across a
// restart.
func TestOldHomeStartsWithApprovalsModeSchedulesAndGrantsIntact(t *testing.T) {
	home := t.TempDir()
	writeOldHome(t, home)
	ctx := ownerCtx()

	for _, boot := range []string{"first", "restart"} {
		app, err := NewApp(appTestConfig(home), appTestSecrets("provider-secret"), AppOptions{})
		if err != nil {
			t.Fatalf("%s boot: %v", boot, err)
		}
		state, err := app.store.Load(ctx)
		if err != nil {
			t.Fatalf("%s boot: %v", boot, err)
		}
		if state.ApprovalMode != ports.ModeStrict {
			t.Fatalf("%s boot mode=%q", boot, state.ApprovalMode)
		}
		if approval, ok := state.Approvals["a1"]; !ok || approval.Status != "pending" {
			t.Fatalf("%s boot approvals=%#v", boot, state.Approvals)
		}
		if _, ok := state.ProcessedEvents["telegram:99"]; !ok {
			t.Fatalf("%s boot processed=%#v", boot, state.ProcessedEvents)
		}
		if state.Agent.SelectedModel != "deepseek-pro" || state.Agent.ReasoningEffort != "high" {
			t.Fatalf("%s boot agent=%#v", boot, state.Agent)
		}
		schedules, err := app.scheduler.List(ctx)
		if err != nil || len(schedules) != 2 {
			t.Fatalf("%s boot schedules=%#v err=%v", boot, schedules, err)
		}
		grant, err := app.database.Auth().Read("mcp", "railway")
		if err != nil || len(grant) == 0 {
			t.Fatalf("%s boot grant=%s err=%v", boot, grant, err)
		}
		if err := app.database.Close(); err != nil {
			t.Fatal(err)
		}
	}

	// The sources are archived rather than deleted, because a rollback needs
	// something to roll back to.
	for _, name := range []string{"state.json", "auth.json", "cron"} {
		if _, err := os.Lstat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Fatalf("%s survived the migration: %v", name, err)
		}
		if _, err := os.Lstat(filepath.Join(home, name+".migrated")); err != nil {
			t.Fatalf("%s has no archive: %v", name, err)
		}
	}
}
