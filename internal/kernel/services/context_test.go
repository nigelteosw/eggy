package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
	contextmarkdown "github.com/nigelteosw/eggy/plugins/context/markdown"
)

func TestSecretGuardRejectsCredentials(t *testing.T) {
	guard := NewSecretGuard([]string{"exact-active-secret"})
	for _, input := range []struct{ section, content string }{
		{"Credentials", "ordinary"}, {"Notes", "github_pat_abcdefghijklmnopqrstuvwxyz"}, {"Notes", "Bearer abcdef"},
		{"Notes", "password=hunter2"}, {"Notes", "-----BEGIN PRIVATE KEY-----"}, {"Notes", "exact-active-secret"},
	} {
		if err := guard.Validate(input.section, input.content); err == nil || !strings.Contains(err.Error(), "secret") {
			t.Fatalf("section=%q content=%q error=%v", input.section, input.content, err)
		}
	}
	if err := guard.Validate("Preferences", "Use repository eggy by default"); err != nil {
		t.Fatal(err)
	}
}

func memoryToolFor(t *testing.T, secrets []string) (ports.Tool, ports.ContextStore) {
	t.Helper()
	store := contextmarkdown.InDir(t.TempDir(), contextmarkdown.DefaultUserMaxBytes, contextmarkdown.DefaultMemoryMaxBytes)
	tools := NewContextTools(store, NewSecretGuard(secrets))
	if len(tools) != 1 || tools[0].Definition().Name != "memory" {
		t.Fatalf("expected a single memory tool, got %d", len(tools))
	}
	return tools[0], store
}

func TestMemoryToolCuratesUserAndMemory(t *testing.T) {
	tool, store := memoryToolFor(t, []string{"secret-value"})
	ctx := context.Background()

	result, err := tool.Execute(ctx, json.RawMessage(`{"action":"add","file":"user","text":"Prefers concise answers"}`))
	if err != nil || string(result) != `{"updated":true}` {
		t.Fatalf("result=%s err=%v", result, err)
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"action":"add","file":"memory","text":"Eggy is trusted"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"action":"replace","file":"memory","old_text":"trusted","text":"Eggy is trusted for eggy"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"action":"remove","file":"user","old_text":"concise"}`)); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(loaded.User, "concise") {
		t.Fatalf("user=%q", loaded.User)
	}
	if !strings.Contains(loaded.Memory, "- Eggy is trusted for eggy") {
		t.Fatalf("memory=%q", loaded.Memory)
	}
}

// TestMemoryToolRejectsSecretsAndUnwritableFiles proves the guard still runs
// on the unified surface, and that SOUL.md has no route through it now that
// it is owner-editable only.
func TestMemoryToolRejectsSecretsAndUnwritableFiles(t *testing.T) {
	tool, store := memoryToolFor(t, []string{"secret-value"})
	ctx := context.Background()

	if _, err := tool.Execute(ctx, json.RawMessage(`{"action":"add","file":"memory","text":"token: secret-value"}`)); err == nil {
		t.Fatal("expected secret rejection")
	}
	for _, file := range []string{"soul", "heartbeat", "nonsense"} {
		raw := json.RawMessage(`{"action":"add","file":"` + file + `","text":"identity"}`)
		if _, err := tool.Execute(ctx, raw); err == nil || !strings.Contains(err.Error(), "file must be") {
			t.Fatalf("file=%q err=%v", file, err)
		}
	}
	loaded, err := store.Load(ctx)
	if err != nil || strings.Contains(loaded.Memory, "secret-value") || strings.Contains(loaded.Soul, "identity") {
		t.Fatalf("context=%#v err=%v", loaded, err)
	}
}

func TestMemoryToolValidatesArguments(t *testing.T) {
	tool, _ := memoryToolFor(t, nil)
	ctx := context.Background()
	for name, raw := range map[string]string{
		"missing text on add":         `{"action":"add","file":"memory"}`,
		"missing old_text on remove":  `{"action":"remove","file":"memory"}`,
		"missing old_text on replace": `{"action":"replace","file":"memory","text":"new"}`,
		"unknown action":              `{"action":"merge","file":"memory","text":"new"}`,
		"unknown field":               `{"action":"add","file":"memory","text":"new","section":"Notes"}`,
	} {
		if _, err := tool.Execute(ctx, json.RawMessage(raw)); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
	// A miss is reported rather than silently succeeding.
	if _, err := tool.Execute(ctx, json.RawMessage(`{"action":"remove","file":"memory","old_text":"never stored"}`)); err == nil {
		t.Fatal("expected error removing an entry that does not exist")
	}
}
