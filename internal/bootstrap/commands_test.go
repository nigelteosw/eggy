package bootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contextmarkdown "github.com/nigelteosw/eggy/internal/adapters/context/markdown"
	"github.com/nigelteosw/eggy/internal/adapters/state/jsonfile"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
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

func TestCommandCodingAgentListsSelectsAndResets(t *testing.T) {
	store := jsonfile.Open(t.TempDir() + "/state.json")
	runtime, err := services.NewCodingAgentRuntime(store, "codex", map[string]ports.CodingAgent{
		"zeta":  &commandTestCodingAgent{},
		"codex": &commandTestCodingAgent{},
	})
	if err != nil {
		t.Fatal(err)
	}
	commands := &CommandService{store: store, codingRuntime: runtime, defaultCodingAgent: "codex"}
	ctx := context.Background()

	output, handled, err := commands.Execute(ctx, "/coding_agent")
	if err != nil || !handled || output != "Active coding agent: codex\nAvailable coding agents:\ncodex\nzeta" {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
	output, _, err = commands.Execute(ctx, "/coding_agent zeta")
	if err != nil || output != "Coding agent set to zeta." {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, _, err = commands.Execute(ctx, "/coding_agent default")
	if err != nil || output != "Coding agent reset to codex." {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, _, err = commands.Execute(ctx, "/coding_agent missing")
	if err != nil || !strings.Contains(output, "not configured") || !strings.Contains(output, "codex, zeta") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, _, err = commands.Execute(ctx, "/coding_agent codex extra")
	if err != nil || output != "Usage: /coding_agent [alias|default]" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestCommandCodingAgentReportsUnconfiguredRuntime(t *testing.T) {
	commands := &CommandService{}
	output, handled, err := commands.Execute(context.Background(), "/coding_agent")
	if err != nil || !handled || output != "Coding agent selection is not configured." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
}

func TestCommandConfigReportsUnconfigured(t *testing.T) {
	commands := &CommandService{}
	output, handled, err := commands.Execute(context.Background(), "/config get coding")
	if err != nil || !handled || output != "Config file management is not configured." {
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

	output, handled, err := commands.Execute(ctx, "/config get coding")
	if err != nil || !handled || output != "default_agent: codex\ncodex  adapter=codex_cli" {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, _, err = commands.Execute(ctx, "/config set coding_agent alias=claude adapter=claude_cli credential_env=CLAUDE_CODE_OAUTH_TOKEN")
	if err != nil || output != "Set coding agent claude. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get coding")
	if err != nil || output != "default_agent: codex\nclaude  adapter=claude_cli  credential_env=CLAUDE_CODE_OAUTH_TOKEN\ncodex  adapter=codex_cli" {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set coding_agent alias=claude adapter=bad_adapter")
	if err != nil || !strings.Contains(output, "unsupported coding agent adapter") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set provider name=openrouter adapter=openai_compatible base_url=https://openrouter.ai/api/v1 api_key_env=OPENROUTER_API_KEY")
	if err != nil || output != "Set provider openrouter. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get providers")
	if err != nil || !strings.Contains(output, "openrouter  adapter=openai_compatible  base_url=https://openrouter.ai/api/v1  api_key_env=OPENROUTER_API_KEY") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set model alias=openrouter-pro provider=openrouter model=your-model-id")
	if err != nil || output != "Set model openrouter-pro. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get models")
	if err != nil || !strings.Contains(output, "openrouter-pro  provider=openrouter  model=your-model-id") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set model alias=orphan provider=missing-provider model=some-model")
	if err != nil || !strings.Contains(output, "unknown provider") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get calendar")
	if err != nil || output != "enabled=true  default_calendar=primary  timezone=UTC" {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set calendar timezone=Asia/Singapore")
	if err != nil || output != "Set calendar. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get calendar")
	if err != nil || output != "enabled=true  default_calendar=primary  timezone=Asia/Singapore" {
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
		{"/config", "Usage: /config get <coding|providers|models|calendar|path>|set <coding_agent|provider|model|calendar> ..."},
		{"/config get", "Usage: /config get <coding|providers|models|calendar|path>"},
		{"/config get unknown", "Usage: /config get <coding|providers|models|calendar|path>"},
		{"/config set", "Usage: /config set <coding_agent|provider|model|calendar> ..."},
		{"/config set coding_agent alias=claude", "Usage: /config set coding_agent alias=<alias> adapter=<codex_cli|claude_cli> [credential_env=<ENV_NAME>]"},
		{"/config set coding_agent alias=claude adapter=claude_cli unknown=x", "Usage: /config set coding_agent alias=<alias> adapter=<codex_cli|claude_cli> [credential_env=<ENV_NAME>]"},
		{"/config set coding_agent notkeyvalue", `invalid flag "notkeyvalue": expected key=value`},
		{"/config set provider name=openrouter adapter=openai_compatible", "Usage: /config set provider name=<name> adapter=openai_compatible base_url=<url> api_key_env=<ENV_NAME>"},
		{"/config set model alias=openrouter-pro provider=openrouter", "Usage: /config set model alias=<alias> provider=<provider> model=<model_id>"},
		{"/config set calendar badkey=x", "Usage: /config set calendar [enabled=<true|false>] [default_calendar=<id>] [timezone=<IANA timezone>]"},
		{"/config set calendar", "at least one of enabled, default_calendar, or timezone is required"},
	}
	for _, tt := range tests {
		output, handled, err := commands.Execute(ctx, tt.input)
		if err != nil || !handled || output != tt.want {
			t.Fatalf("input=%q output=%q handled=%v err=%v", tt.input, output, handled, err)
		}
	}
}

type commandTestCodingAgent struct{}

func (*commandTestCodingAgent) Run(context.Context, ports.CodingRequest, func(ports.CodingProgress)) (ports.CodingResult, error) {
	return ports.CodingResult{}, nil
}

func (*commandTestCodingAgent) Interrupt(string) error { return nil }

func TestCommandRepositoriesListsAddsAndRemoves(t *testing.T) {
	store := jsonfile.Open(t.TempDir() + "/state.json")
	runner := &commandTestRunner{workspace: "/tmp/runs/check-1"}
	checker := &commandTestChecker{}
	gateway := &commandTestApprovalGateway{approval: approvals.Approval{ID: "approval-1", Action: approvals.AddRepository}}
	repositories := services.NewRepositoriesService(store, runner, checker, gateway, gateway, ports.RepositoryCapabilities{Commit: true, Push: true, PullRequest: true}, func() string { return "check-1" })
	var delivered approvals.Approval
	channel := &commandTestChannel{onApproval: func(approval approvals.Approval) { delivered = approval }}
	commands := &CommandService{store: store, repositories: repositories, channel: channel, owner: "42"}
	ctx := context.Background()

	output, handled, err := commands.Execute(ctx, "/repositories")
	if err != nil || !handled || output != "No repositories configured." {
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
	if err != nil || !handled || output != "eggy" {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/repositories remove eggy")
	if err != nil || !handled || output != "Removed eggy." {
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

func TestPromptsCommandCRUD(t *testing.T) {
	dir := t.TempDir()
	contextStore := contextmarkdown.Open(dir, 64<<10)
	commands := &CommandService{context: contextStore}
	ctx := context.Background()

	output, handled, err := commands.Execute(ctx, "/prompts")
	if err != nil || !handled || output != "No custom prompts." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/prompts set reviewer Be blunt about risk.")
	if err != nil || !handled || output != "Set prompt reviewer." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/prompts")
	if err != nil || !handled || output != "reviewer" {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/prompts show reviewer")
	if err != nil || !handled || output != "Be blunt about risk." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/prompts show missing")
	if err != nil || !handled || output != "No such prompt: missing." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/prompts remove reviewer")
	if err != nil || !handled || output != "Removed prompt reviewer." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/prompts remove reviewer")
	if err != nil || !handled || !strings.Contains(output, "does not exist") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
}
