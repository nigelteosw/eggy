package bootstrap

import (
	"context"
	"strings"
	"testing"

	contextmarkdown "github.com/nigelteosw/eggy/internal/adapters/context/markdown"
	"github.com/nigelteosw/eggy/internal/adapters/state/jsonfile"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestCommandModelSelection(t *testing.T) {
	store := jsonfile.Open(t.TempDir() + "/state.json")
	runtime := services.NewAgentRuntime(store, "deepseek-pro", []string{"openrouter-pro", "deepseek-pro"})
	commands := &CommandService{store: store, agentRuntime: runtime, defaultModel: "deepseek-pro", modelAliases: []string{"openrouter-pro", "deepseek-pro"}}
	ctx := context.Background()
	output, handled, err := commands.Execute(ctx, "/model")
	if err != nil || !handled || !strings.Contains(output, "Active model: deepseek-pro") || strings.Index(output, "deepseek-pro") > strings.LastIndex(output, "openrouter-pro") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
	output, _, err = commands.Execute(ctx, "/model openrouter-pro")
	if err != nil || output != "Model set to openrouter-pro." {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, _, err = commands.Execute(ctx, "/model missing")
	if err != nil || !strings.Contains(output, "not configured") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, _, err = commands.Execute(ctx, "/model default")
	if err != nil || output != "Model reset to deepseek-pro." {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestCommandUsageAndLayeredMemory(t *testing.T) {
	dir := t.TempDir()
	store := jsonfile.Open(dir + "/state.json")
	runtime := services.NewAgentRuntime(store, "deepseek-pro", []string{"deepseek-pro"})
	if err := runtime.RecordUsage(context.Background(), "deepseek-pro", ports.ModelUsage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14, CachedPromptTokens: 3}); err != nil {
		t.Fatal(err)
	}
	contextStore := contextmarkdown.Open(dir, 64<<10)
	if err := contextStore.Append(context.Background(), ports.ContextMemory, "Repositories", "Eggy is trusted"); err != nil {
		t.Fatal(err)
	}
	commands := &CommandService{store: store, agentRuntime: runtime, defaultModel: "deepseek-pro", modelAliases: []string{"deepseek-pro"}, context: contextStore}
	output, _, err := commands.Execute(context.Background(), "/usage")
	if err != nil || !strings.Contains(output, "prompt=10") || !strings.Contains(output, "cached=3") || !strings.Contains(output, "do not replace the provider billing dashboard") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	memory, _, err := commands.Execute(context.Background(), "/memory")
	if err != nil || !strings.Contains(memory, "Eggy is trusted") {
		t.Fatalf("memory=%q err=%v", memory, err)
	}
	output, _, err = commands.Execute(context.Background(), "/usage reset")
	if err != nil || !strings.Contains(output, "reset") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	usage, _ := runtime.Usage(context.Background())
	if len(usage) != 0 {
		t.Fatalf("usage=%#v", usage)
	}
}
