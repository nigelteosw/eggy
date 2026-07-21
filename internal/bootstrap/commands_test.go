package bootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	contextmarkdown "github.com/nigelteosw/eggy/internal/adapters/context/markdown"
	"github.com/nigelteosw/eggy/internal/adapters/state/jsonfile"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

// TestTelegramAndCLIProduceTheSameSemanticResult drives one named-arg style
// command (config set provider) and one positional style command
// (repositories add) through both surfaces' parsers and dispatch, and checks
// the resulting CommandResult is identical. Equivalent input on either
// surface must never behave differently.
func TestTelegramAndCLIProduceTheSameSemanticResult(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := &CommandService{configPath: path}

	telegramReq, ok := ParseTelegramInput(catalogIndex, "/config set provider name=openrouter adapter=openai_compatible base_url=https://openrouter.ai/api/v1 api_key_env=OPENROUTER_API_KEY")
	if !ok {
		t.Fatal("expected telegram match")
	}
	telegramResult, err := commands.dispatch(ctx, telegramReq)
	if err != nil {
		t.Fatal(err)
	}

	// Reset the config file so the CLI-driven call starts from the same state.
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
		t.Fatal(err)
	}
	cliReq, ok := ParseCLIArgs(catalogIndex, []string{"config", "set", "provider", "--name=openrouter", "--adapter=openai_compatible", "--base-url=https://openrouter.ai/api/v1", "--api-key-env=OPENROUTER_API_KEY"})
	if !ok {
		t.Fatal("expected cli match")
	}
	cliResult, err := commands.dispatch(ctx, cliReq)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(telegramResult, cliResult) {
		t.Fatalf("telegram=%#v cli=%#v", telegramResult, cliResult)
	}

	store := jsonfile.Open(t.TempDir() + "/state.json")
	runner := &commandTestRunner{workspace: "/tmp/runs/check-1"}
	checker := &commandTestChecker{}
	gateway := &commandTestApprovalGateway{approval: approvals.Approval{ID: "approval-1", Action: approvals.AddRepository}}
	repositories := services.NewRepositoriesService(store, runner, checker, gateway, gateway, ports.RepositoryCapabilities{Commit: true, Push: true, PullRequest: true}, func() string { return "check-1" }, nil)
	repoCommands := &CommandService{store: store, repositories: repositories, channel: &commandTestChannel{}, owner: "42"}

	telegramReq, ok = ParseTelegramInput(catalogIndex, "/repositories add eggy https://github.com/nigelteosw/eggy.git main")
	if !ok {
		t.Fatal("expected telegram match")
	}
	telegramResult, err = repoCommands.dispatch(ctx, telegramReq)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Remove(ctx, "eggy"); err != nil && !strings.Contains(err.Error(), "not configured") {
		t.Fatal(err)
	}

	cliReq, ok = ParseCLIArgs(catalogIndex, []string{"repositories", "add", "eggy", "https://github.com/nigelteosw/eggy.git", "main"})
	if !ok {
		t.Fatal("expected cli match")
	}
	cliResult, err = repoCommands.dispatch(ctx, cliReq)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(telegramResult, cliResult) {
		t.Fatalf("telegram=%#v cli=%#v", telegramResult, cliResult)
	}
}

func TestCommandModelSelection(t *testing.T) {
	store := jsonfile.Open(t.TempDir() + "/state.json")
	runtime := services.NewAgentRuntime(store, "deepseek-pro", []string{"openrouter-pro", "deepseek-pro"}, map[string][]string{"deepseek-pro": {"low", "high"}})
	commands := &CommandService{store: store, agentRuntime: runtime, defaultModel: "deepseek-pro", modelAliases: []string{"openrouter-pro", "deepseek-pro"}}
	ctx := context.Background()
	output, handled, err := commands.Execute(ctx, "/model")
	if err != nil || !handled || !strings.Contains(output, "Active model:** deepseek-pro") || strings.Index(output, "deepseek-pro") > strings.LastIndex(output, "openrouter-pro") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
	if !strings.Contains(output, "|deepseek-pro|low, high|yes|") {
		t.Fatalf("output=%q missing reasoning effort listing", output)
	}
	output, _, err = commands.Execute(ctx, "/model effort high")
	if err != nil || output != "Reasoning effort set to high." {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, _, err = commands.Execute(ctx, "/model")
	if err != nil || !strings.Contains(output, "Reasoning effort:** high") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, _, err = commands.Execute(ctx, "/model effort medium")
	if err != nil || !strings.Contains(output, "supports reasoning effort low|high") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, _, err = commands.Execute(ctx, "/model openrouter-pro")
	if err != nil || output != "Model set to openrouter-pro." {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, _, err = commands.Execute(ctx, "/model effort low")
	if err != nil || !strings.Contains(output, "does not support a reasoning effort option") {
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
	output, _, err = commands.Execute(ctx, "/model")
	if err != nil || !strings.Contains(output, "Reasoning effort:** high") {
		t.Fatalf("output=%q err=%v, want the effort set earlier to still apply after switching back", output, err)
	}
}

func TestCommandConfigReportsUnconfigured(t *testing.T) {
	commands := &CommandService{}
	output, handled, err := commands.Execute(context.Background(), "/config get providers")
	if err != nil || !handled || output != "Config file management is not configured." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
}

func TestCommandContinueRequiresConfiguredCoding(t *testing.T) {
	output, handled, err := (&CommandService{}).Execute(context.Background(), "/continue")
	if err != nil || !handled || output != "Coding is not configured." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
}

func TestCommandRestartInvokesCallback(t *testing.T) {
	var calls int
	commands := &CommandService{restart: func() { calls++ }}
	output, handled, err := commands.Execute(context.Background(), "/restart")
	if err != nil || !handled || !strings.Contains(output, "Restarting Eggy to pick up config changes. Back in a few seconds.") || !strings.Contains(output, "resumed with /continue") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d, want 1", calls)
	}
}

func TestCommandRestartReportsUnavailableWithoutCallback(t *testing.T) {
	output, handled, err := (&CommandService{}).Execute(context.Background(), "/restart")
	if err != nil || !handled || output != "Restart is not available in this environment." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
}

func TestCommandConfigGetAndSetRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := &CommandService{configPath: path}
	ctx := context.Background()

	output, _, err := commands.Execute(ctx, "/config set provider name=openrouter adapter=openai_compatible base_url=https://openrouter.ai/api/v1 api_key_env=OPENROUTER_API_KEY")
	if err != nil || !strings.Contains(output, "Set provider openrouter.") || !strings.Contains(output, "Restart Eggy for this to take effect.") || !strings.Contains(output, "/restart") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get providers")
	if err != nil || !strings.Contains(output, "openrouter") || !strings.Contains(output, "openai_compatible") || !strings.Contains(output, "https://openrouter.ai/api/v1") || !strings.Contains(output, "OPENROUTER_API_KEY") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set model alias=openrouter-pro provider=openrouter model=your-model-id")
	if err != nil || !strings.Contains(output, "Set model openrouter-pro.") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get models")
	if err != nil || !strings.Contains(output, "openrouter-pro") || !strings.Contains(output, "openrouter") || !strings.Contains(output, "your-model-id") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set model alias=orphan provider=missing-provider model=some-model")
	if err != nil || !strings.Contains(output, "unknown provider") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get calendar")
	if err != nil || !strings.Contains(output, "true") || !strings.Contains(output, "primary") || !strings.Contains(output, "UTC") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set calendar timezone=Asia/Singapore")
	if err != nil || !strings.Contains(output, "Set calendar.") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get calendar")
	if err != nil || !strings.Contains(output, "Asia/Singapore") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get path")
	if err != nil || output != path {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestCommandConfigUsageErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := &CommandService{configPath: path}
	ctx := context.Background()
	tests := []struct{ input, want string }{
		{"/config", "Use get, set, or show"},
		{"/config get", "Expected exactly one section"},
		{"/config get unknown", `Unknown config section "unknown"`},
		{"/config set", "provider, model, or calendar"},
		{"/config set provider name=openrouter adapter=openai_compatible", "Required: name, adapter, base_url, api_key_env."},
		{"/config set model alias=openrouter-pro provider=openrouter", "Required: alias, provider, model."},
		{"/config set calendar badkey=x", `Unknown field "badkey"`},
		{"/config set calendar", "At least one of enabled, default_calendar, or timezone is required"},
	}
	for _, tt := range tests {
		output, handled, err := commands.Execute(ctx, tt.input)
		if err != nil || !handled || !strings.Contains(output, tt.want) {
			t.Fatalf("input=%q output=%q handled=%v err=%v", tt.input, output, handled, err)
		}
		if !strings.Contains(output, "Telegram") || !strings.Contains(output, "CLI") {
			t.Fatalf("input=%q output=%q missing Telegram/CLI examples", tt.input, output)
		}
	}
}

func TestCommandRepositoriesListsAddsAndRemoves(t *testing.T) {
	store := jsonfile.Open(t.TempDir() + "/state.json")
	runner := &commandTestRunner{workspace: "/tmp/runs/check-1"}
	checker := &commandTestChecker{}
	gateway := &commandTestApprovalGateway{approval: approvals.Approval{ID: "approval-1", Action: approvals.AddRepository}}
	repositories := services.NewRepositoriesService(store, runner, checker, gateway, gateway, ports.RepositoryCapabilities{Commit: true, Push: true, PullRequest: true}, func() string { return "check-1" }, nil)
	var delivered approvals.Approval
	channel := &commandTestChannel{onApproval: func(approval approvals.Approval) { delivered = approval }}
	commands := &CommandService{store: store, repositories: repositories, channel: channel, owner: "42"}
	ctx := context.Background()

	output, handled, err := commands.Execute(ctx, "/repositories")
	if err != nil || !handled || !strings.Contains(output, "No repositories configured.") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/repositories add eggy https://github.com/nigelteosw/eggy.git")
	if err != nil || !handled || !strings.Contains(output, "awaiting approval") || delivered.ID != "approval-1" {
		t.Fatalf("output=%q handled=%v err=%v delivered=%#v", output, handled, err, delivered)
	}

	approval := delivered
	approval.Status = approvals.Approved
	if _, err := repositories.ExecuteApproved(ctx, approval); err != nil {
		t.Fatal(err)
	}

	output, handled, err = commands.Execute(ctx, "/repositories")
	if err != nil || !handled || !strings.Contains(output, "eggy") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/repositories remove eggy")
	if err != nil || !handled || !strings.Contains(output, "Removed eggy.") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/repositories remove eggy")
	if err != nil || !handled || !strings.Contains(output, "not configured") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/repositories add")
	if err != nil || !handled || !strings.Contains(output, "Usage:") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
}

type commandTestRunner struct{ workspace string }

func (r *commandTestRunner) Create(context.Context, string) (string, error) { return r.workspace, nil }
func (r *commandTestRunner) Execute(context.Context, ports.Command) (ports.CommandResult, error) {
	return ports.CommandResult{}, nil
}
func (r *commandTestRunner) Destroy(context.Context, string) error { return nil }

type commandTestChecker struct{}

func (commandTestChecker) CheckRemote(context.Context, ports.Repository, string) error { return nil }

type commandTestApprovalGateway struct {
	approval   approvals.Approval
	authorized approvals.Action
}

func (g *commandTestApprovalGateway) Request(_ context.Context, action approvals.Action, payload any, summary string) (approvals.Approval, error) {
	data, _ := json.Marshal(payload)
	g.approval.Payload = data
	return g.approval, nil
}
func (g *commandTestApprovalGateway) Authorize(_ context.Context, action approvals.Action, _ any, _ string) error {
	g.authorized = action
	return nil
}

type commandTestChannel struct{ onApproval func(approvals.Approval) }

func (c *commandTestChannel) Deliver(context.Context, string, string) error { return nil }
func (c *commandTestChannel) DeliverApproval(_ context.Context, _ string, approval approvals.Approval) error {
	if c.onApproval != nil {
		c.onApproval(approval)
	}
	return nil
}
func (c *commandTestChannel) DeliverTrackable(context.Context, string, string) (string, error) {
	return "", nil
}
func (c *commandTestChannel) EditText(context.Context, string, string, string) error { return nil }
func (c *commandTestChannel) AnswerCallback(context.Context, string) error           { return nil }
func (c *commandTestChannel) SendTyping(context.Context, string) error               { return nil }

func TestCommandUsageAndLayeredMemory(t *testing.T) {
	dir := t.TempDir()
	store := jsonfile.Open(dir + "/state.json")
	runtime := services.NewAgentRuntime(store, "deepseek-pro", []string{"deepseek-pro"}, nil)
	if err := runtime.RecordUsage(context.Background(), "deepseek-pro", ports.ModelUsage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14, CachedPromptTokens: 3}); err != nil {
		t.Fatal(err)
	}
	contextStore := contextmarkdown.Open(dir, 64<<10)
	if err := contextStore.Append(context.Background(), ports.ContextMemory, "Repositories", "Eggy is trusted"); err != nil {
		t.Fatal(err)
	}
	commands := &CommandService{store: store, agentRuntime: runtime, defaultModel: "deepseek-pro", modelAliases: []string{"deepseek-pro"}, context: contextStore}
	output, _, err := commands.Execute(context.Background(), "/usage")
	if err != nil || !strings.Contains(output, "|deepseek-pro|10|4|3|0|14|") || !strings.Contains(output, "do not replace the provider billing dashboard") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	memory, _, err := commands.Execute(context.Background(), "/memory")
	if err != nil || !strings.Contains(memory, "Eggy is trusted") || !strings.Contains(memory, "Durable memory") {
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

func TestPromptsCommandCRUD(t *testing.T) {
	dir := t.TempDir()
	contextStore := contextmarkdown.Open(dir, 64<<10)
	commands := &CommandService{context: contextStore}
	ctx := context.Background()

	output, handled, err := commands.Execute(ctx, "/prompts")
	if err != nil || !handled || !strings.Contains(output, "No custom prompts.") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/prompts set reviewer Be blunt about risk.")
	if err != nil || !handled || !strings.Contains(output, "Set prompt reviewer.") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/prompts")
	if err != nil || !handled || !strings.Contains(output, "reviewer") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/prompts show reviewer")
	if err != nil || !handled || !strings.Contains(output, "Be blunt about risk.") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/prompts show missing")
	if err != nil || !handled || !strings.Contains(output, "No such prompt: missing.") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/prompts remove reviewer")
	if err != nil || !handled || !strings.Contains(output, "Removed prompt reviewer.") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/prompts remove reviewer")
	if err != nil || !handled || !strings.Contains(output, "does not exist") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
}
