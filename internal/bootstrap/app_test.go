package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/channels/telegram"
	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/kernel/lane"
	"github.com/nigelteosw/eggy/internal/ports"
	"gopkg.in/yaml.v3"
)

func TestEventLaneNeverLetsScheduledTextAuthorizeImplementation(t *testing.T) {
	text := "Implement the approved design"
	if got := eventLane(events.TypeMessage, text); got != lane.Implementation {
		t.Fatalf("message lane=%v, want implementation", got)
	}
	if got := eventLane(events.TypeSchedule, text); got != lane.Assistant {
		t.Fatalf("schedule lane=%v, want assistant", got)
	}
}

func TestCapabilityManifestSeparatesRepositoryAndShippingReadiness(t *testing.T) {
	app := &App{config: Config{Coding: CodingConfig{DefaultAgent: "codex"}}, codingAliases: []string{"codex"}, manifest: agent.CapabilityManifest{RepositoryCommitReady: true, RepositoryPushReady: false, PullRequestReady: false}}
	withoutRepository := app.capabilityManifest(ports.State{}, "deepseek-pro")
	if withoutRepository.CodingAgentReady || withoutRepository.RepositoryCommitReady || withoutRepository.RepositoryPushReady || withoutRepository.PullRequestReady {
		t.Fatalf("without repository=%#v", withoutRepository)
	}
	withRepository := app.capabilityManifest(ports.State{Repositories: map[string]ports.Repository{"eggy": {Name: "eggy"}}}, "deepseek-pro")
	if withRepository.ActiveCodingAgent != "codex" || !withRepository.CodingAgentReady || !withRepository.RepositoryCommitReady || withRepository.RepositoryPushReady || withRepository.PullRequestReady {
		t.Fatalf("with repository=%#v", withRepository)
	}
}

func TestCodingAgentBootstrapPreservesCodexOnlyCompatibility(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	app, err := NewApp(cfg, appTestSecrets("key"), AppOptions{FakeAdapters: true, CodexExecutable: "/missing/codex"})
	if err != nil {
		t.Fatal(err)
	}
	output, handled, err := app.ExecuteCommand(context.Background(), "/coding_agent")
	if err != nil || !handled || output != "Active coding agent: codex\nAvailable coding agents:\ncodex" {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
}

func TestCodingAgentBootstrapRegistersCredentialReadyClaudeAndSwitchesGlobally(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Coding = CodingConfig{DefaultAgent: "codex", Agents: map[string]CodingAgentConfig{
		"codex":  {Adapter: "codex_cli"},
		"claude": {Adapter: "claude_cli", CredentialEnv: "CLAUDE_CODE_OAUTH_TOKEN"},
	}}
	secrets := appTestSecrets("key")
	secrets.CodingAgentCredentials = map[string]string{"claude": "railway-secret"}
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true, CodexExecutable: "/missing/codex", ClaudeExecutable: "/missing/claude"})
	if err != nil {
		t.Fatal(err)
	}
	if output, _, err := app.ExecuteCommand(context.Background(), "/coding_agent claude"); err != nil || output != "Coding agent set to claude." {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, _, err := app.ExecuteCommand(context.Background(), "/coding_agent")
	if err != nil || output != "Active coding agent: claude\nAvailable coding agents:\nclaude\ncodex" {
		t.Fatalf("output=%q err=%v", output, err)
	}
	state, err := app.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manifest := app.capabilityManifest(ports.State{Coding: state.Coding, Repositories: map[string]ports.Repository{"eggy": {Name: "eggy"}}}, "deepseek-pro")
	if manifest.ActiveCodingAgent != "claude" || !manifest.CodingAgentReady {
		t.Fatalf("manifest=%#v", manifest)
	}
}

func TestAppConfigSetWritesToConfiguredPath(t *testing.T) {
	dataDir := t.TempDir()
	configPath := filepath.Join(dataDir, "config.yaml")
	cfg := appTestConfig(dataDir)
	body, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(cfg, appTestSecrets("key"), AppOptions{FakeAdapters: true, ConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	output, handled, err := app.ExecuteCommand(context.Background(), "/config set provider name=openrouter adapter=openai_compatible base_url=https://openrouter.ai/api/v1 api_key_env=OPENROUTER_API_KEY")
	if err != nil || !handled || output != "Set provider openrouter. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
	reloaded, _, err := LoadConfig(configPath, mapEnv(map[string]string{"TELEGRAM_BOT_TOKEN": "bot", "TELEGRAM_WEBHOOK_SECRET": "webhook", "DEEPSEEK_API_KEY": "key"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Providers["openrouter"]; !ok {
		t.Fatalf("providers = %#v", reloaded.Providers)
	}
}

func TestCodingAgentBootstrapSkipsClaudeWithoutOptionalCredential(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Coding = CodingConfig{DefaultAgent: "codex", Agents: map[string]CodingAgentConfig{
		"codex":  {Adapter: "codex_cli"},
		"claude": {Adapter: "claude_cli", CredentialEnv: "CLAUDE_CODE_OAUTH_TOKEN"},
	}}
	app, err := NewApp(cfg, appTestSecrets("key"), AppOptions{FakeAdapters: true, CodexExecutable: "/missing/codex", ClaudeExecutable: "/missing/claude"})
	if err != nil {
		t.Fatal(err)
	}
	output, _, err := app.ExecuteCommand(context.Background(), "/coding_agent")
	if err != nil || strings.Contains(output, "claude") || !strings.Contains(output, "codex") {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestCodingAgentBootstrapRejectsUnavailableDefault(t *testing.T) {
	for _, test := range []struct {
		name, credential, executable, want string
	}{
		{name: "missing credential", executable: "/usr/bin/true", want: "CLAUDE_CODE_OAUTH_TOKEN"},
		{name: "missing executable", credential: "railway-secret", executable: "/definitely/missing/claude", want: "claude"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := appTestConfig(t.TempDir())
			if test.name == "missing executable" {
				cfg.Repositories = []RepositoryConfig{{Name: "eggy", CloneURL: "https://github.com/nigelteosw/eggy.git", BaseBranch: "main"}}
			}
			cfg.Coding = CodingConfig{DefaultAgent: "claude", Agents: map[string]CodingAgentConfig{
				"claude": {Adapter: "claude_cli", CredentialEnv: "CLAUDE_CODE_OAUTH_TOKEN"},
			}}
			secrets := appTestSecrets("key")
			secrets.CodingAgentCredentials = map[string]string{"claude": test.credential}
			_, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: test.name == "missing credential", ClaudeExecutable: test.executable})
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "default coding agent") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestCodingAgentReadinessReportsAvailableAliasWithoutCredentials(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Repositories = []RepositoryConfig{{Name: "eggy", CloneURL: "https://github.com/nigelteosw/eggy.git", BaseBranch: "main"}}
	cfg.Coding = CodingConfig{DefaultAgent: "claude", Agents: map[string]CodingAgentConfig{
		"claude": {Adapter: "claude_cli", CredentialEnv: "CLAUDE_CODE_OAUTH_TOKEN"},
	}}
	secrets := appTestSecrets("key")
	secrets.CodingAgentCredentials = map[string]string{"claude": "railway-secret"}
	var startupLog bytes.Buffer
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true, ClaudeExecutable: "/missing/claude", Logger: slog.New(slog.NewJSONHandler(&startupLog, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Ready(); err != nil {
		t.Fatal(err)
	}
	output := startupLog.String()
	if !strings.Contains(output, `"integrations":["claude","github","model_provider","telegram"]`) || strings.Contains(output, "codex") || strings.Contains(output, "railway-secret") {
		t.Fatalf("readiness log=%s", output)
	}
}

func TestScheduledImplementationTextGetsAssistantToolsAndManifest(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Repositories = []RepositoryConfig{{Name: "eggy", CloneURL: "https://github.com/nigelteosw/eggy.git", BaseBranch: "main", ProtectedBranches: []string{"main"}}}
	var modelBodies [][]byte
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "deepseek.test" {
			body, _ := io.ReadAll(request.Body)
			modelBodies = append(modelBodies, body)
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`), nil
		}
		return appJSON(200, `{"ok":true,"result":true}`), nil
	})}
	app, err := NewApp(cfg, appTestSecrets("key"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}, CodexExecutable: "/usr/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.Message{ChatID: "42", Text: "Implement the approved design"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "message", Type: events.TypeMessage, Owner: "42", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if err := app.HandleEvent(context.Background(), events.Event{ID: "schedule", Type: events.TypeSchedule, Owner: "42", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(modelBodies) != 2 {
		t.Fatalf("model requests=%d, want 2", len(modelBodies))
	}
	if !strings.Contains(string(modelBodies[0]), "repository_modify") {
		t.Fatalf("implementation turn did not advertise repository_modify: %s", modelBodies[0])
	}
	if strings.Contains(string(modelBodies[1]), "repository_modify") {
		t.Fatalf("scheduled turn advertised repository_modify: %s", modelBodies[1])
	}
}

func TestAppComposesReadyServiceAndHandlesCommandsAndAssistantTurns(t *testing.T) {
	dataDir := t.TempDir()
	cfg := appTestConfig(dataDir)
	cfg.Calendar.Timezone = "Asia/Singapore"
	secrets := appTestSecrets("provider-secret")
	var mu sync.Mutex
	var telegramBodies [][]byte
	var modelBody []byte
	var startupLog bytes.Buffer
	fixedNow := time.Date(2026, 7, 19, 12, 34, 56, 0, time.FixedZone("SGT", 8*60*60))
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "sendMessage") {
			body, _ := io.ReadAll(request.Body)
			mu.Lock()
			telegramBodies = append(telegramBodies, body)
			mu.Unlock()
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		}
		if request.URL.Host == "deepseek.test" {
			modelBody, _ = io.ReadAll(request.Body)
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"Hello from Eggy."}}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`), nil
		}
		return appJSON(404, `{}`), nil
	})}
	logger := slog.New(slog.NewJSONHandler(&startupLog, nil))
	app, err := NewApp(cfg, secrets, AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}, Now: func() time.Time { return fixedNow }, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Ready(); err != nil {
		t.Fatal(err)
	}
	logOutput := startupLog.String()
	if !strings.Contains(logOutput, "agent runtime ready") || !strings.Contains(logOutput, "deepseek-pro") || !strings.Contains(logOutput, "SOUL.md") || strings.Contains(logOutput, secrets.ProviderAPIKeys["deepseek"]) || strings.Contains(logOutput, secrets.TelegramBotToken) {
		t.Fatalf("unsafe or incomplete startup log: %s", logOutput)
	}
	statusPayload, _ := json.Marshal(events.Message{ChatID: "42", Text: "/status"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "1", Type: events.TypeMessage, Owner: "42", Payload: statusPayload}); err != nil {
		t.Fatal(err)
	}
	messagePayload, _ := json.Marshal(events.Message{ChatID: "42", Text: "Say hello"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "2", Type: events.TypeMessage, Owner: "42", Payload: messagePayload}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(telegramBodies) != 2 || !strings.Contains(string(telegramBodies[0]), "pending_approvals") || !strings.Contains(string(telegramBodies[1]), "Hello from Eggy") {
		t.Fatalf("telegram=%q", telegramBodies)
	}
	if !strings.Contains(string(modelBody), "Eggy Memory") || !strings.Contains(string(modelBody), "Hard runtime policy") || !strings.Contains(string(modelBody), "Capability manifest") || !strings.Contains(string(modelBody), `"model":"deepseek-v4-pro"`) || !strings.Contains(string(modelBody), "2026-07-19T12:34:56+08:00") || !strings.Contains(string(modelBody), "Asia/Singapore") {
		t.Fatalf("unified context missing from model request: %s", modelBody)
	}
	state, err := app.store.Load(context.Background())
	if err != nil || state.Agent.Usage["deepseek-pro"].TotalTokens != 14 {
		t.Fatalf("usage=%#v err=%v", state.Agent.Usage, err)
	}
	if app.Handler() == nil {
		t.Fatal("HTTP handler missing")
	}
	cfg.Server.TelegramWebhookPath = "/private-telegram-hook"
	customApp, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/private-telegram-hook", strings.NewReader(`{}`))
	request.Header.Set("X-Telegram-Bot-Api-Secret-Token", "webhook")
	response := httptest.NewRecorder()
	customApp.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusNotFound {
		t.Fatal("configured Telegram webhook path was not registered")
	}
}

func TestNewAppRegistersTelegramCommandSuggestionsOnBoot(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	var setCommandsBody []byte
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "setMyCommands") {
			setCommandsBody, _ = io.ReadAll(request.Body)
			return appJSON(200, `{"ok":true,"result":true}`), nil
		}
		return appJSON(200, `{"ok":true,"result":{}}`), nil
	})}
	_, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if setCommandsBody == nil {
		t.Fatal("expected NewApp to call setMyCommands on boot")
	}
	var payload struct {
		Commands []struct {
			Command     string `json:"command"`
			Description string `json:"description"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(setCommandsBody, &payload); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, command := range payload.Commands {
		if command.Description == "" {
			t.Fatalf("command %q has no description", command.Command)
		}
		names[command.Command] = true
	}
	for _, want := range []string{"status", "repositories", "runs", "stop", "schedules", "memory", "model", "usage", "new", "calendar_auth"} {
		if !names[want] {
			t.Fatalf("command %q missing from registered suggestions: %v", want, names)
		}
	}
}

func TestUnifiedAgentDefectTranscript(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Repositories = []RepositoryConfig{{Name: "eggy", CloneURL: "https://github.com/nigelteosw/eggy.git", BaseBranch: "main", ProtectedBranches: []string{"main"}}}
	secrets := appTestSecrets("provider-secret")
	secrets.GitHubToken = "github-secret"
	var modelBodies [][]byte
	var delivered []byte
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Host == "deepseek.test":
			body, _ := io.ReadAll(request.Body)
			modelBodies = append(modelBodies, body)
			if len(modelBodies) == 1 {
				return appJSON(200, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"repos-1","type":"function","function":{"name":"repository_list","arguments":"{}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`), nil
			}
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"I can work on the configured eggy repository."}}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`), nil
		case strings.Contains(request.URL.Path, "sendMessage"):
			delivered, _ = io.ReadAll(request.Body)
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		case strings.Contains(request.URL.Path, "setMyCommands"):
			return appJSON(200, `{"ok":true,"result":true}`), nil
		default:
			return appJSON(404, `{}`), nil
		}
	})}
	app, err := NewApp(cfg, secrets, AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}, CodexExecutable: "/usr/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.Message{ChatID: "42", Text: "What repositories can you work on?"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "repo-question", Type: events.TypeMessage, Owner: "42", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(modelBodies) != 2 || !strings.Contains(string(modelBodies[1]), `\"status\":\"configured\"`) || !strings.Contains(string(modelBodies[1]), `\"name\":\"eggy\"`) {
		t.Fatalf("repository tool result was not returned to the model: %q", modelBodies)
	}
	for _, body := range modelBodies {
		if strings.Contains(string(body), "provider-secret") || strings.Contains(string(body), "github-secret") {
			t.Fatalf("secret leaked into model request: %s", body)
		}
	}
	if !strings.Contains(string(delivered), "configured eggy repository") {
		t.Fatalf("telegram response=%s", delivered)
	}
	state, err := app.store.Load(context.Background())
	if err != nil || state.Agent.Usage["deepseek-pro"].TotalTokens != 19 {
		t.Fatalf("usage=%#v err=%v", state.Agent.Usage, err)
	}
}

func TestCommandServiceSupportsOperationalShortcuts(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"/status", "/repositories", "/runs", "/schedules", "/memory", "/new"} {
		output, handled, err := app.commands.Execute(context.Background(), command)
		if err != nil || !handled || output == "" {
			t.Fatalf("%s output=%q handled=%v err=%v", command, output, handled, err)
		}
	}
}

func TestCommandServiceHandlesEveryRegisteredTelegramCommand(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range telegram.Commands() {
		_, handled, err := app.commands.Execute(context.Background(), "/"+command.Name)
		if err != nil || !handled {
			t.Fatalf("registered command %q was not handled by CommandService: handled=%v err=%v", command.Name, handled, err)
		}
	}
	if _, handled, _ := app.commands.Execute(context.Background(), "/unknown"); handled {
		t.Fatal("unknown command handled")
	}
}

func TestCalendarAuthCommandCreatesShortLivedOwnerEnrollment(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Calendar = CalendarConfig{Enabled: true, DefaultCalendar: "primary", Timezone: "UTC"}
	secrets := appTestSecrets("deepseek")
	secrets.GoogleClientID, secrets.GoogleClientSecret, secrets.EncryptionKey = "client", "secret", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	output, handled, err := app.ExecuteCommand(context.Background(), "/calendar_auth")
	if err != nil || !handled || !strings.Contains(output, "/auth/google?enrollment=") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
	state, _ := app.store.Load(context.Background())
	if state.Calendar.EnrollmentDigest == "" || !state.Calendar.EnrollmentExpires.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("calendar auth=%#v", state.Calendar)
	}
}

func TestWebhookQueuesSlowAssistantTurnBeforeAcknowledging(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	started := make(chan struct{})
	release := make(chan struct{})
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "deepseek.test" {
			close(started)
			<-release
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`), nil
		}
		return appJSON(200, `{"ok":true,"result":{}}`), nil
	})}
	app, err := NewApp(cfg, appTestSecrets("key"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = app.Run(ctx) }()
	body := `{"update_id":99,"message":{"message_id":1,"from":{"id":42},"chat":{"id":42},"text":"slow turn"}}`
	request := httptest.NewRequest(http.MethodPost, cfg.Server.TelegramWebhookPath, strings.NewReader(body))
	request.Header.Set("X-Telegram-Bot-Api-Secret-Token", "webhook")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { app.Handler().ServeHTTP(response, request); close(done) }()
	select {
	case <-done:
		if response.Code != http.StatusNoContent {
			t.Fatalf("status=%d", response.Code)
		}
	case <-time.After(200 * time.Millisecond):
		close(release)
		t.Fatal("webhook waited for the assistant turn")
	}
	<-started
	close(release)
}

func TestHandleMessageSendsTypingIndicatorDuringSlowAssistantTurn(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	started := make(chan struct{})
	release := make(chan struct{})
	var typingCalls int32
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "deepseek.test" {
			close(started)
			<-release
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`), nil
		}
		if strings.Contains(request.URL.Path, "sendChatAction") {
			atomic.AddInt32(&typingCalls, 1)
		}
		return appJSON(200, `{"ok":true,"result":true}`), nil
	})}
	app, err := NewApp(cfg, appTestSecrets("key"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.Message{ChatID: "42", Text: "slow turn"})
	done := make(chan struct{})
	go func() {
		_ = app.HandleEvent(context.Background(), events.Event{ID: "typing-1", Type: events.TypeMessage, Owner: "42", Payload: payload})
		close(done)
	}()
	<-started
	if atomic.LoadInt32(&typingCalls) < 1 {
		t.Fatal("expected a typing indicator to be sent before the slow model call returned")
	}
	close(release)
	<-done
}

func TestRepositoriesAddApprovalFlowReachesLiveState(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	secrets := appTestSecrets("provider-secret")
	secrets.GitHubToken = "github-secret"
	remote := createLocalGitRemote(t)
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		return appJSON(200, `{"ok":true,"result":{}}`), nil
	})}
	app, err := NewApp(cfg, secrets, AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}, CodexExecutable: "/usr/bin/true"})
	if err != nil {
		t.Fatal(err)
	}

	output, handled, err := app.commands.Execute(context.Background(), "/repositories add eggy "+remote)
	if err != nil || !handled || !strings.Contains(output, "awaiting approval") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	state, err := app.store.Load(context.Background())
	if err != nil || len(state.Approvals) != 1 {
		t.Fatalf("approvals=%#v err=%v", state.Approvals, err)
	}
	var approvalID string
	for id := range state.Approvals {
		approvalID = id
	}

	decisionPayload, _ := json.Marshal(events.ApprovalDecision{ApprovalID: approvalID, Approved: true})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "decide-1", Type: events.TypeApproval, Owner: "42", Payload: decisionPayload}); err != nil {
		t.Fatal(err)
	}

	state, err = app.store.Load(context.Background())
	if err != nil || state.Repositories["eggy"].CloneURL != remote {
		t.Fatalf("repositories=%#v err=%v", state.Repositories, err)
	}
}

func createLocalGitRemote(t *testing.T) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source")
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "-b", "main", source)
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", ".")
	runGit(t, source, "commit", "-m", "initial")
	runGit(t, "", "clone", "--bare", source, remote)
	return remote
}

func runGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	if directory != "" {
		command.Dir = directory
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}

func appTestConfig(dataDir string) Config {
	return Config{
		Version: 2, DataDir: dataDir,
		Server:       ServerConfig{Listen: ":8080", PublicBaseURL: "https://eggy.test", TelegramWebhookPath: "/webhooks/telegram"},
		Telegram:     TelegramConfig{OwnerID: 42},
		Agent:        AgentConfig{DefaultModel: "deepseek-pro"},
		Providers:    map[string]ProviderConfig{"deepseek": {Adapter: "openai_compatible", BaseURL: "https://api.deepseek.com", APIKeyEnv: "DEEPSEEK_API_KEY"}},
		ModelAliases: map[string]ModelAliasConfig{"deepseek-pro": {Provider: "deepseek", Model: "deepseek-v4-pro"}},
		Runner:       RunnerConfig{Root: filepath.Join(dataDir, "runs"), Timeout: Duration(time.Minute), Retention: Duration(time.Minute), MaxOutputBytes: 1 << 20, AllowedEnv: []string{"PATH"}},
		Scheduler:    SchedulerConfig{HeartbeatCadence: Duration(30 * time.Minute), QuietHours: QuietHoursConfig{Start: "22:00", End: "07:00", Timezone: "UTC"}, MinimumProactiveInterval: Duration(time.Hour), WeeklyProactiveLimit: 3},
	}
}

func appTestSecrets(providerKey string) Secrets {
	return Secrets{TelegramBotToken: "bot", TelegramWebhookSecret: "webhook", ProviderAPIKeys: map[string]string{"deepseek": providerKey}}
}

type appRoundTrip func(*http.Request) (*http.Response, error)

func (f appRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
func appJSON(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
