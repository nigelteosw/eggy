package services

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
	sqlitestore "github.com/nigelteosw/eggy/plugins/store/sqlite"
)

// newStateStore backs the runtime with the real store, because what these
// tests exercise is the compare-and-set around a selection: a map behind a
// mutex would pass while the concurrent case that matters went untested.
func newStateStore(t *testing.T) ports.StateStore {
	t.Helper()
	database, err := sqlitestore.Open(filepath.Join(t.TempDir(), "eggy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database.State()
}

func TestAgentRuntimeSelectsModelsAndResetsDefault(t *testing.T) {
	runtime := NewAgentRuntime(newStateStore(t), "deepseek-pro", []string{"deepseek-pro", "openrouter-pro"}, map[string][]string{"deepseek-pro": {"low", "medium", "high", "max"}})
	ctx := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "42"})
	if got, err := runtime.SelectedModel(ctx); err != nil || got != "deepseek-pro" {
		t.Fatalf("selected=%q err=%v", got, err)
	}
	if err := runtime.SelectModel(ctx, "openrouter-pro"); err != nil {
		t.Fatal(err)
	}
	if got, _ := runtime.SelectedModel(ctx); got != "openrouter-pro" {
		t.Fatalf("selected=%q", got)
	}
	if err := runtime.SelectModel(ctx, "missing"); err == nil {
		t.Fatal("expected unknown alias error")
	}
	if err := runtime.SelectModel(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := runtime.SelectedModel(ctx); got != "deepseek-pro" {
		t.Fatalf("selected=%q after reset", got)
	}
}

func TestAgentRuntimeFallsBackWhenTheSelectedAliasWasRemoved(t *testing.T) {
	store := newStateStore(t)
	before := NewAgentRuntime(store, "deepseek-pro", []string{"deepseek-pro", "retired"}, nil)
	if err := before.SelectModel(ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "42"}), "retired"); err != nil {
		t.Fatal(err)
	}
	after := NewAgentRuntime(store, "deepseek-pro", []string{"deepseek-pro"}, nil)
	if got, err := after.SelectedModel(ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "42"})); err != nil || got != "deepseek-pro" {
		t.Fatalf("selected model = %q, err=%v", got, err)
	}
}

func TestAgentRuntimeSelectsReasoningEffortPerActiveModel(t *testing.T) {
	runtime := NewAgentRuntime(newStateStore(t), "deepseek-pro", []string{"deepseek-pro", "openrouter-pro"}, map[string][]string{"deepseek-pro": {"low", "high"}})
	ctx := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "42"})

	if got, err := runtime.ReasoningEffort(ctx); err != nil || got != "" {
		t.Fatalf("effort=%q err=%v, want empty before anything is set", got, err)
	}
	if err := runtime.SelectReasoningEffort(ctx, "medium"); err == nil {
		t.Fatal("expected rejection of an effort level deepseek-pro doesn't support")
	}
	if err := runtime.SelectReasoningEffort(ctx, "high"); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.ReasoningEffort(ctx); err != nil || got != "high" {
		t.Fatalf("effort=%q err=%v", got, err)
	}

	if err := runtime.SelectModel(ctx, "openrouter-pro"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SelectReasoningEffort(ctx, "high"); err == nil {
		t.Fatal("expected rejection: openrouter-pro has no configured reasoning efforts")
	}
	if got, err := runtime.ReasoningEffort(ctx); err != nil || got != "" {
		t.Fatalf("effort=%q err=%v, want empty once the active model doesn't support the stored value", got, err)
	}

	if err := runtime.SelectModel(ctx, "deepseek-pro"); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.ReasoningEffort(ctx); err != nil || got != "high" {
		t.Fatalf("effort=%q err=%v, want the earlier selection to apply again", got, err)
	}
}

func TestAgentRuntimeRecordsConcurrentUsageAndResets(t *testing.T) {
	runtime := NewAgentRuntime(newStateStore(t), "deepseek-pro", []string{"deepseek-pro"}, nil)
	ctx := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "42"})
	var workers sync.WaitGroup
	errorsChannel := make(chan error, 16)
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errorsChannel <- runtime.RecordUsage(ctx, "deepseek-pro", ports.ModelUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2})
		}()
	}
	workers.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	usage, err := runtime.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := usage["deepseek-pro"]; got.PromptTokens != 16 || got.CompletionTokens != 16 || got.TotalTokens != 32 {
		t.Fatalf("usage=%#v", got)
	}
	usage["deepseek-pro"] = ports.ModelUsage{}
	again, _ := runtime.Usage(ctx)
	if again["deepseek-pro"].TotalTokens != 32 {
		t.Fatal("Usage returned internal map")
	}
	if err := runtime.ResetUsage(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := runtime.Usage(ctx)
	if len(after) != 0 {
		t.Fatalf("usage after reset=%#v", after)
	}
}

// Every runtime setting is the calling account's own: one person's model,
// effort, or thinking choice is never another's default, and a person who
// arrives later starts from the deployment default, not from whoever set
// theirs first.
func TestPersonalModelSelectionDoesNotChangeAnotherAccount(t *testing.T) {
	store := newStateStore(t)
	runtime := NewAgentRuntime(store, "shared-default", []string{"shared-default", "other"}, nil)
	a := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "a"})
	b := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "b"})
	if err := runtime.SelectModel(a, "other"); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.SelectedModel(b); err != nil || got != "shared-default" {
		t.Fatalf("second user's model=%q err=%v", got, err)
	}
	if err := runtime.SetShowThinking(a, false); err != nil {
		t.Fatal(err)
	}
	if show, err := runtime.ShowThinking(b); err != nil || !show {
		t.Fatalf("second user's thinking=%v err=%v", show, err)
	}
	if show, err := runtime.ShowThinking(a); err != nil || show {
		t.Fatalf("first user's thinking=%v err=%v", show, err)
	}
	c := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "c"})
	if got, err := runtime.SelectedModel(c); err != nil || got != "shared-default" {
		t.Fatalf("new user's model=%q err=%v", got, err)
	}
	if show, err := runtime.ShowThinking(c); err != nil || !show {
		t.Fatalf("new user's thinking=%v err=%v", show, err)
	}
	// Resetting to the default writes the empty alias and follows the
	// configured default from then on.
	if err := runtime.SelectModel(a, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.SelectedModel(a); err != nil || got != "shared-default" {
		t.Fatalf("reset model=%q err=%v", got, err)
	}
}

func TestAgentRuntimeResetsEffortEvenOnAModelWithoutEfforts(t *testing.T) {
	runtime := NewAgentRuntime(newStateStore(t), "reasoner", []string{"reasoner", "plain"}, map[string][]string{"reasoner": {"high"}})
	ctx := ports.WithPrincipal(t.Context(), ports.Principal{AccountID: "alice"})
	if err := runtime.SelectReasoningEffort(ctx, "high"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SelectModel(ctx, "plain"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SelectReasoningEffort(ctx, ""); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := runtime.SelectModel(ctx, "reasoner"); err != nil {
		t.Fatal(err)
	}
	if effort, err := runtime.ReasoningEffort(ctx); err != nil || effort != "" {
		t.Fatalf("effort=%q err=%v", effort, err)
	}
}
