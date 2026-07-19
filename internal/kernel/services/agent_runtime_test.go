package services

import (
	"context"
	"sync"
	"testing"

	"github.com/nigelteosw/eggy/internal/adapters/state/jsonfile"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestAgentRuntimeSelectsModelsAndResetsDefault(t *testing.T) {
	runtime := NewAgentRuntime(jsonfile.Open(t.TempDir()+"/state.json"), "deepseek-pro", []string{"deepseek-pro", "openrouter-pro"})
	ctx := context.Background()
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

func TestAgentRuntimeRecordsConcurrentUsageAndResets(t *testing.T) {
	runtime := NewAgentRuntime(jsonfile.Open(t.TempDir()+"/state.json"), "deepseek-pro", []string{"deepseek-pro"})
	ctx := context.Background()
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
