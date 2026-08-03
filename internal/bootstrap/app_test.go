package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/kernel/turns"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestNewAppRegistersMCPToolsOnlyForDirectOwnerTurns(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.MCP.Servers = map[string]config.MCPServerConfig{
		"railway": {Enabled: true, URL: "https://mcp.railway.com", Transport: "streamable-http", Auth: "oauth", ToolFilter: config.MCPToolFilterConfig{Include: []string{"list-projects"}}},
	}
	secrets := appTestSecrets("deepseek")
	secrets.EncryptionKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	direct := app.loop.ToolNames(agent.RunOptions{})
	scheduled := app.loop.ToolNames(turns.ReadOnlyTools())
	if !slices.Contains(direct, "railway__list_projects") || slices.Contains(scheduled, "railway__list_projects") {
		t.Fatalf("direct=%v scheduled=%v mcp_status=%#v mcp_tools=%v", direct, scheduled, app.mcp.Statuses(), toolDefinitionNames(app.mcp.Tools()))
	}
}

func toolDefinitionNames(tools []ports.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Definition().Name)
	}
	return names
}

func TestDirectOwnerMessagesAndSchedulesExposeOnlyReadOnlyRepositoryTools(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Repositories = []config.RepositoryConfig{{Name: "eggy", CloneURL: createLocalGitRemote(t), BaseBranch: "main", ProtectedBranches: []string{"main"}, Self: true}}
	var modelBodies [][]byte
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "deepseek.test" {
			body, _ := io.ReadAll(request.Body)
			modelBodies = append(modelBodies, body)
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`), nil
		}
		return appJSON(200, `{"ok":true,"result":true}`), nil
	})}
	app, err := NewApp(cfg, appTestSecrets("key"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.Message{Text: "yes make the change"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "message", Type: events.TypeMessage, Owner: "42", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if err := app.HandleEvent(context.Background(), events.Event{ID: "schedule", Type: events.TypeSchedule, Owner: "42", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(modelBodies) != 2 {
		t.Fatalf("model requests=%d, want 2", len(modelBodies))
	}
	directTools := requestedToolNames(t, modelBodies[0])
	for _, name := range []string{"repository_list", "repository_github", "workspace_open", "read_file", "workspace_close"} {
		if !directTools[name] {
			t.Fatalf("direct owner message did not advertise read-only tool %q: %s", name, modelBodies[0])
		}
	}
	scheduledTools := requestedToolNames(t, modelBodies[1])
	for _, tools := range []map[string]bool{directTools, scheduledTools} {
		for _, gone := range []string{"terminal", "workspace_edit", "patch", "write_file", "propose_change"} {
			if tools[gone] {
				t.Fatalf("repository mutation or shell tool %q was advertised: direct=%v scheduled=%v", gone, directTools, scheduledTools)
			}
		}
	}
	if !strings.Contains(string(modelBodies[1]), "self_repository: eggy") {
		t.Fatalf("scheduled turn did not learn which repository is its own body: %s", modelBodies[1])
	}
}

// requestedToolNames parses the tools array out of a serialized model
// request body. It deliberately does not use a raw substring search over
// the whole body — the hard runtime policy's prose legitimately mentions
// tool names like "workspace_edit" regardless of which tools are
// actually offered, so a substring check would false-positive on that text.
func requestedToolNames(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	var decoded struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	names := make(map[string]bool, len(decoded.Tools))
	for _, tool := range decoded.Tools {
		names[tool.Function.Name] = true
	}
	return names
}

func TestAppComposesReadyServiceAndHandlesCommandsAndAssistantTurns(t *testing.T) {
	dataDir := t.TempDir()
	cfg := appTestConfig(dataDir)
	cfg.Agent.Timezone = "Asia/Singapore"
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
	statusPayload, _ := json.Marshal(events.Message{Text: "/status"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "1", Type: events.TypeMessage, Owner: "42", Payload: statusPayload}); err != nil {
		t.Fatal(err)
	}
	messagePayload, _ := json.Marshal(events.Message{Text: "Say hello"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "2", Type: events.TypeMessage, Owner: "42", Payload: messagePayload}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(telegramBodies) != 2 || !strings.Contains(string(telegramBodies[0]), "Pending approvals") || !strings.Contains(string(telegramBodies[1]), "Hello from Eggy") {
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

func TestNewAppOpensDurableMemoryAndRegistersTextRecallWithoutEmbeddings(t *testing.T) {
	dataDir := t.TempDir()
	app, err := NewApp(appTestConfig(dataDir), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "eggy.db")); err != nil {
		t.Fatalf("stat eggy.db: %v", err)
	}
	if app.memory == nil {
		t.Fatal("durable memory store is nil")
	}
	if !slices.Contains(app.loop.ToolNames(agent.RunOptions{}), "recall_conversation") {
		t.Fatalf("registered tools=%v", app.loop.ToolNames(agent.RunOptions{}))
	}
}

func TestDirectOwnerTurnStoresExactlyUserAndAssistantWithDefaultSourceAndClock(t *testing.T) {
	dataDir := t.TempDir()
	cfg := appTestConfig(dataDir)
	fixedNow := time.Date(2026, 7, 23, 9, 8, 7, 0, time.UTC)
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "deepseek.test" {
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"durable assistant reply"}}]}`), nil
		}
		return appJSON(200, `{"ok":true,"result":true}`), nil
	})}
	app, err := NewApp(cfg, appTestSecrets("provider-secret"), AppOptions{
		HTTPClient: client, TelegramBaseURL: "https://telegram.test",
		ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"},
		Now:              func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.Message{Text: "durable owner prompt"})
	if err := app.HandleEvent(context.Background(), events.Event{
		ID: "direct", Type: events.TypeMessage, Owner: "42", Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}

	messages, err := app.memory.RecentMessages(context.Background(), "telegram", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("durable messages=%#v", messages)
	}
	if messages[0].Role != ports.RoleUser || messages[0].Content != "durable owner prompt" ||
		messages[1].Role != ports.RoleAssistant || messages[1].Content != "durable assistant reply" {
		t.Fatalf("durable messages=%#v", messages)
	}
	for _, message := range messages {
		if message.Source != "telegram" || !message.CreatedAt.Equal(fixedNow) {
			t.Fatalf("durable message=%#v", message)
		}
	}
}

func TestCommandFailedModelAndApprovalEventsDoNotWriteDurableMemory(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "deepseek.test" {
			return appJSON(500, `{"error":"provider unavailable"}`), nil
		}
		return appJSON(200, `{"ok":true,"result":true}`), nil
	})}
	app, err := NewApp(cfg, appTestSecrets("provider-secret"), AppOptions{
		HTTPClient: client, TelegramBaseURL: "https://telegram.test",
		ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	commandPayload, _ := json.Marshal(events.Message{Text: "/status"})
	if err := app.HandleEvent(context.Background(), events.Event{
		ID: "command", Type: events.TypeMessage, Owner: "42", Payload: commandPayload,
	}); err != nil {
		t.Fatal(err)
	}
	failedPayload, _ := json.Marshal(events.Message{Text: "this model turn fails"})
	if err := app.HandleEvent(context.Background(), events.Event{
		ID: "failed", Type: events.TypeMessage, Owner: "42", Payload: failedPayload,
	}); err == nil {
		t.Fatal("failed model turn returned nil error")
	}
	approvalPayload, _ := json.Marshal(events.ApprovalDecision{ApprovalID: "missing", Approved: true})
	if err := app.HandleEvent(context.Background(), events.Event{
		ID: "approval", Type: events.TypeApproval, Owner: "42", Payload: approvalPayload,
	}); err == nil {
		t.Fatal("missing approval returned nil error")
	}

	messages, err := app.memory.RecentMessages(context.Background(), "telegram", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("durable messages=%#v", messages)
	}
}

// TestDurableMemoryFailureIsLoggedWithoutBlockingReply covers SQLite now
// being the single source of truth for the live turn-context window (see
// docs/superpowers/specs/2026-07-23-multi-thread-web-chat-design.md): a
// durable-store outage degrades the read (no recent history injected) and
// is logged on both the read and the two post-turn writes, but a turn
// still completes and still delivers a reply rather than failing outright.
func TestDurableMemoryFailureIsLoggedWithoutBlockingReply(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	var logs bytes.Buffer
	var delivered atomic.Int32
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "deepseek.test" {
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"reply still delivered"}}]}`), nil
		}
		if strings.Contains(request.URL.Path, "sendMessage") {
			delivered.Add(1)
		}
		return appJSON(200, `{"ok":true,"result":true}`), nil
	})}
	app, err := NewApp(cfg, appTestSecrets("provider-secret"), AppOptions{
		HTTPClient: client, TelegramBaseURL: "https://telegram.test",
		ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"},
		Logger:           slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.memory.Close(); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.Message{Text: "keep the live turn working"})
	if err := app.HandleEvent(context.Background(), events.Event{
		ID: "direct", Type: events.TypeMessage, Source: "telegram", Owner: "42", Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	if delivered.Load() != 1 {
		t.Fatalf("delivered=%d", delivered.Load())
	}
	if count := strings.Count(logs.String(), "recent conversation window unavailable"); count != 1 {
		t.Fatalf("read failure logs=%d: %s", count, logs.String())
	}
	if count := strings.Count(logs.String(), "durable conversation write failed"); count != 2 {
		t.Fatalf("durable failure logs=%d: %s", count, logs.String())
	}
}

func TestRecallConversationRedactsBareUIPasswordFromStoredHistory(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	secrets := appTestSecrets("provider-secret")
	secrets.UIPassword = "bare-ui-password"
	var modelRequests atomic.Int32
	var secondBody []byte
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "deepseek.test" {
			return appJSON(200, `{"ok":true,"result":true}`), nil
		}
		if modelRequests.Add(1) == 1 {
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"recall-1","type":"function","function":{"name":"recall_conversation","arguments":"{\"query\":\"remembered\"}"}}]}}]}`), nil
		}
		secondBody, _ = io.ReadAll(request.Body)
		return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`), nil
	})}
	app, err := NewApp(cfg, secrets, AppOptions{
		HTTPClient: client, TelegramBaseURL: "https://telegram.test",
		ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.memory.WriteMessage(context.Background(), ports.StoredMessage{
		Role: ports.RoleUser, Content: "remembered bare-ui-password", Source: "web", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.loop.Run(context.Background(), "deepseek-pro", "", "recall it", nil, agent.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(secondBody), "bare-ui-password") || !strings.Contains(string(secondBody), "[redacted]") {
		t.Fatalf("second model request did not redact UI password: %s", secondBody)
	}
}

func TestHandleMessageDeliversReasoningContentBeforeAnswer(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	secrets := appTestSecrets("provider-secret")
	var mu sync.Mutex
	var telegramTexts []string
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "sendMessage") {
			body, _ := io.ReadAll(request.Body)
			var payload struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(body, &payload)
			mu.Lock()
			telegramTexts = append(telegramTexts, payload.Text)
			mu.Unlock()
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		}
		if request.URL.Host == "deepseek.test" {
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"42.","reasoning_content":"Let me work through this step by step."}}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`), nil
		}
		return appJSON(404, `{}`), nil
	})}
	app, err := NewApp(cfg, secrets, AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Ready(); err != nil {
		t.Fatal(err)
	}
	messagePayload, _ := json.Marshal(events.Message{Text: "What is 6*7?"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "1", Type: events.TypeMessage, Owner: "42", Payload: messagePayload}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(telegramTexts) != 2 {
		t.Fatalf("telegram messages=%#v, want a reasoning message followed by the answer", telegramTexts)
	}
	if !strings.Contains(telegramTexts[0], "Thinking:") || !strings.Contains(telegramTexts[0], "Let me work through this step by step.") {
		t.Fatalf("first message=%q, want the reasoning content", telegramTexts[0])
	}
	if telegramTexts[1] != "42." {
		t.Fatalf("second message=%q, want the final answer", telegramTexts[1])
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
	if len(names) != 9 {
		t.Fatalf("registered %d commands, want 9: %v", len(names), names)
	}
	for _, want := range []string{"help", "status", "stop", "clear", "model", "mcp", "google", "auto", "restart"} {
		if !names[want] {
			t.Fatalf("command %q missing from registered suggestions: %v", want, names)
		}
	}
}

func TestUnifiedAgentDefectTranscript(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Repositories = []config.RepositoryConfig{{Name: "eggy", CloneURL: createLocalGitRemote(t), BaseBranch: "main", ProtectedBranches: []string{"main"}}}
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
	app, err := NewApp(cfg, secrets, AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.Message{Text: "What repositories can you work on?"})
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

func TestCommandServiceSupportsFiveConversationalCommands(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"/help", "/status", "/stop", "/clear", "/model"} {
		output, handled, err := app.commands.Execute(context.Background(), command)
		if err != nil || !handled || output == "" {
			t.Fatalf("%s output=%q handled=%v err=%v", command, output, handled, err)
		}
	}
}

// TestCommandServiceHandlesEveryRegisteredTelegramCommand is the catalog
// coverage test: every top-level command Telegram's autocomplete advertises
// must also have a working commands.CommandService handler, so the two surfaces can
// never drift apart.
func TestCommandServiceHandlesEveryRegisteredTelegramCommand(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range commands.TelegramAutocomplete() {
		if command.Description == "" {
			t.Fatalf("command %q has no description", command.Name)
		}
		_, handled, err := app.commands.Execute(context.Background(), "/"+command.Name)
		if err != nil || !handled {
			t.Fatalf("registered command %q was not handled by commands.CommandService: handled=%v err=%v", command.Name, handled, err)
		}
	}
	if output, handled, _ := app.commands.Execute(context.Background(), "/unknown"); !handled || !strings.Contains(output, "/help") {
		t.Fatalf("unknown command output=%q handled=%v", output, handled)
	}
}

// TestHandleMessageRepliesGracefullyWhenToolStepLimitReached proves that
// exhausting the outer loop's tool-step budget delivers an explanatory
// Telegram message instead of leaving the owner with no reply and only an
// ERROR line in the event logs.
func TestHandleMessageRepliesGracefullyWhenToolStepLimitReached(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	var delivered []byte
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Host == "deepseek.test":
			// Always answer with another tool call so the loop never
			// terminates on its own and must hit the step limit.
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":"status","arguments":"{}"}}]}}]}`), nil
		case strings.Contains(request.URL.Path, "sendMessage"):
			body, _ := io.ReadAll(request.Body)
			delivered = body
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		case strings.Contains(request.URL.Path, "setMyCommands"):
			return appJSON(200, `{"ok":true,"result":true}`), nil
		default:
			return appJSON(404, `{}`), nil
		}
	})}
	app, err := NewApp(cfg, appTestSecrets("key"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.Message{Text: "keep checking status forever"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "loop-1", Type: events.TypeMessage, Owner: "42", Payload: payload}); err != nil {
		t.Fatalf("expected the step-limit case to be handled gracefully, got error: %v", err)
	}
	if !strings.Contains(string(delivered), "ran out of tool-call steps") {
		t.Fatalf("delivered message did not explain the step limit: %s", delivered)
	}
}

// TestToolCallSurfacesALiveIndicatorBeforeTheFinalReply covers the gap
// flagged after the multi-thread chat rollout: an ordinary tool call (not
// just a coding run's) should be visible mid-turn, not folded silently into
// the final answer. Telegram's sendMessage/editMessageText calls double as
// an ordering probe here: the indicator must be sent, then finalized, then
// -- and only then -- the real answer goes out as a new message.
func TestToolCallSurfacesALiveIndicatorBeforeTheFinalReply(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	var calls []string
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Host == "deepseek.test":
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), `"tool_call_id"`) {
				return appJSON(200, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"status","arguments":"{}"}}]}}]}`), nil
			}
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"all good"}}]}`), nil
		case strings.Contains(request.URL.Path, "sendMessage"):
			body, _ := io.ReadAll(request.Body)
			var decoded struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(body, &decoded)
			calls = append(calls, "send:"+decoded.Text)
			return appJSON(200, `{"ok":true,"result":{"message_id":123}}`), nil
		case strings.Contains(request.URL.Path, "editMessageText"):
			body, _ := io.ReadAll(request.Body)
			var decoded struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(body, &decoded)
			calls = append(calls, "edit:"+decoded.Text)
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		case strings.Contains(request.URL.Path, "setMyCommands"):
			return appJSON(200, `{"ok":true,"result":true}`), nil
		default:
			return appJSON(404, `{}`), nil
		}
	})}
	app, err := NewApp(cfg, appTestSecrets("key"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.Message{Text: "what's the status?"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "status-1", Type: events.TypeMessage, Owner: "42", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(calls) < 2 {
		t.Fatalf("calls=%#v, want at least an indicator and a final reply", calls)
	}
	if !strings.Contains(calls[0], "Calling status") {
		t.Fatalf("calls[0]=%q, want the live indicator sent first", calls[0])
	}
	last := calls[len(calls)-1]
	if !strings.Contains(last, "all good") {
		t.Fatalf("calls=%#v, want the real answer delivered last, after any progress indicator", calls)
	}
	for _, call := range calls[:len(calls)-1] {
		if strings.Contains(call, "all good") {
			t.Fatalf("calls=%#v, the real answer must not be sent before the tool-call indicator is finalized", calls)
		}
	}
}

// TestToolCallIndicatorRoutesToTheWebThreadThatTriggeredIt is the
// thread-isolation counterpart: a tool call made from a web thread must
// surface its live indicator on that thread's Hub connection only, never
// on Telegram or a different thread -- the same routedChannel/destination
// guarantee the rest of this design relies on.
func TestToolCallIndicatorRoutesToTheWebThreadThatTriggeredIt(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "deepseek.test" {
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), `"tool_call_id"`) {
				return appJSON(200, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"status","arguments":"{}"}}]}}]}`), nil
			}
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"all good"}}]}`), nil
		}
		return appJSON(200, `{"ok":true,"result":true}`), nil
	})}
	app, err := NewApp(cfg, appTestSecrets("key"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.memory.CreateThread(context.Background(), "thread-a", "web", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, threadEvents, unregisterThread := app.chatHub.Register("thread-a")
	defer unregisterThread()
	_, telegramEvents, unregisterOther := app.chatHub.Register("some-other-thread")
	defer unregisterOther()

	payload, _ := json.Marshal(events.Message{Text: "what's the status?"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "web-status-1", Type: events.TypeMessage, Source: "web", Owner: "42", Destination: destination.Destination{Kind: destination.Web, ThreadID: "thread-a"}, Payload: payload}); err != nil {
		t.Fatal(err)
	}

	sawIndicator := false
	deadline := time.After(2 * time.Second)
	for !sawIndicator {
		select {
		case event := <-threadEvents:
			if strings.Contains(event.Text, "Calling status") {
				sawIndicator = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for the tool-call indicator on the triggering thread")
		}
	}

	select {
	case event := <-telegramEvents:
		t.Fatalf("expected no events on an unrelated thread, got %#v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWebhookQueuesSlowAssistantTurnBeforeAcknowledging(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	started := make(chan struct{})
	release := make(chan struct{})
	delivered := make(chan struct{})
	var deliveredOnce sync.Once
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "deepseek.test" {
			close(started)
			<-release
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`), nil
		}
		if strings.Contains(request.URL.Path, "sendMessage") {
			deliveredOnce.Do(func() { close(delivered) })
		}
		return appJSON(200, `{"ok":true,"result":{}}`), nil
	})}
	app, err := NewApp(cfg, appTestSecrets("key"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	// The queued turn keeps writing its transcript and durable conversation
	// into cfg's data dir after this test's assertion is satisfied, and that
	// dir is a t.TempDir. Cancel and wait for Run to return -- it defers
	// workers.Wait() -- so no goroutine is still writing when the TempDir
	// cleanup runs RemoveAll and fails with "directory not empty".
	defer func() {
		cancel()
		<-runDone
	}()
	go func() { _ = app.Run(ctx); close(runDone) }()
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
	// Let the released turn run to completion before the deferred cancel, so
	// shutdown is not racing a half-written transcript. The assertion above
	// is already satisfied; this only keeps teardown orderly, so a turn that
	// never reaches delivery falls through to the same deferred cleanup
	// rather than hanging the test.
	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
	}
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
	payload, _ := json.Marshal(events.Message{Text: "slow turn"})
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

func TestNewAppRejectsAnInaccessibleConfiguredRepository(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Repositories = []config.RepositoryConfig{{
		Name:       "missing",
		CloneURL:   filepath.Join(t.TempDir(), "missing.git"),
		BaseBranch: "main",
	}}
	_, err := NewApp(cfg, appTestSecrets("key"), AppOptions{})
	if err == nil || !strings.Contains(err.Error(), `validate repository "missing"`) {
		t.Fatalf("NewApp error=%v, want configured repository validation failure", err)
	}
}

func appTestConfig(dataDir string) config.Config {
	return config.Config{
		DataDir:      dataDir,
		Server:       config.ServerConfig{Listen: ":8080", PublicBaseURL: "https://eggy.test", TelegramWebhookPath: "/webhooks/telegram"},
		Owner:        config.OwnerConfig{ID: "42"},
		Telegram:     config.TelegramConfig{OwnerID: 42},
		Agent:        config.AgentConfig{DefaultModel: "deepseek-pro", Timezone: "UTC"},
		Providers:    map[string]config.ProviderConfig{"deepseek": {Adapter: "openai_compatible", BaseURL: "https://api.deepseek.com", APIKeyEnv: "DEEPSEEK_API_KEY"}},
		ModelAliases: map[string]config.ModelAliasConfig{"deepseek-pro": {Provider: "deepseek", Model: "deepseek-v4-pro"}},
		Runner:       config.RunnerConfig{Root: filepath.Join(dataDir, "runs"), Timeout: config.Duration(time.Minute), Retention: config.Duration(time.Minute), MaxOutputBytes: 1 << 20, AllowedEnv: []string{"PATH"}},
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

func appTestSecrets(providerKey string) config.Secrets {
	return config.Secrets{TelegramBotToken: "bot", TelegramWebhookSecret: "webhook", ProviderAPIKeys: map[string]string{"deepseek": providerKey}}
}

type appRoundTrip func(*http.Request) (*http.Response, error)

func (f appRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
func appJSON(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

// A web-only deployment configures no Telegram at all: no owner_id, no bot
// token, no webhook secret. It must construct, serve, and route proactive
// output to the web channel rather than panicking on a nil Telegram client.
func TestNewAppBuildsAWebOnlyDeploymentWithNoTelegramConfiguration(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Owner = config.OwnerConfig{ID: "owner-42"}
	cfg.Telegram = config.TelegramConfig{}
	secrets := config.Secrets{ProviderAPIKeys: map[string]string{"deepseek": "deepseek"}}

	app, err := NewApp(cfg, secrets, AppOptions{})
	if err != nil {
		t.Fatalf("a web-only deployment must boot without Telegram: %v", err)
	}

	// The webhook route is served as unavailable rather than registered.
	request := httptest.NewRequest(http.MethodPost, cfg.Server.TelegramWebhookPath, nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatalf("the Telegram webhook must not be live without Telegram configured, status=%d", response.Code)
	}

	// A scheduled message is unprompted output. With no Telegram configured
	// it must be dropped, not redirected into a web thread the owner never
	// asked to be pushed to.
	web := &fakeChannel{name: "web"}
	app.channel = newRoutedChannel(nil, web)
	payload, err := json.Marshal(events.Message{Text: "scheduled reminder"})
	if err != nil {
		t.Fatal(err)
	}
	event := events.Event{ID: "schedule:1", Type: events.TypeScheduledMessage, Owner: "owner-42", Payload: payload}
	if err := app.processEvent(context.Background(), event); err != nil {
		t.Fatalf("an unprompted turn must not fail on a web-only deployment: %v", err)
	}
	if len(web.delivered) != 0 {
		t.Fatalf("web delivered=%v, want a web-only deployment to produce no unprompted output", web.delivered)
	}
}

// Unprompted output is Telegram's, deliberately: heartbeat, scheduled agent
// turns, and scheduled messages all report there and never to a web thread.
// Pinned as a test so a future destination change is a decision rather than
// an accident.
func TestUnpromptedTurnsAlwaysReportToTelegram(t *testing.T) {
	if kind := proactiveDestination().Kind; kind != destination.Telegram {
		t.Fatalf("proactive destination=%q, want Telegram", kind)
	}

	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	telegramChannel, webChannel := &fakeChannel{name: "telegram"}, &fakeChannel{name: "web"}
	app.channel = newRoutedChannel(telegramChannel, webChannel)

	payload, err := json.Marshal(events.Message{Text: "scheduled reminder"})
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately handed a *web* destination on the event: the proactive
	// path must override it rather than honour it.
	event := events.Event{
		ID: "schedule:1", Type: events.TypeScheduledMessage, Owner: "42",
		Destination: destination.Destination{Kind: destination.Web, ThreadID: "thread-a"}, Payload: payload,
	}
	if err := app.processEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(telegramChannel.delivered) != 1 || telegramChannel.delivered[0] != "scheduled reminder" {
		t.Fatalf("telegram delivered=%v, want the scheduled message", telegramChannel.delivered)
	}
	if len(webChannel.delivered) != 0 {
		t.Fatalf("web delivered=%v, want unprompted output kept off the web channel", webChannel.delivered)
	}
}

// The wiring end of R1: a require_approval entry has to survive config, the
// manager, and the provider boundary to reach the model as a gated tool. The
// description is what proves the gate is on -- it is the only part of the
// wrapper the loop can see.
func TestNewAppGatesConfiguredMCPToolsBehindApproval(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.MCP.Servers = map[string]config.MCPServerConfig{
		"railway": {
			Enabled: true, URL: "https://mcp.railway.com", Transport: "streamable-http", Auth: "oauth",
			ToolFilter:      config.MCPToolFilterConfig{Include: []string{"list-projects", "delete-project"}},
			RequireApproval: []string{"delete-project"},
		},
	}
	secrets := appTestSecrets("deepseek")
	secrets.EncryptionKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	gated, plain := "", ""
	for _, listing := range app.tools.Catalog() {
		switch listing.Definition.Name {
		case "railway__delete_project":
			gated = listing.Definition.Description
		case "railway__list_projects":
			plain = listing.Definition.Description
		}
	}
	if !strings.Contains(gated, "requires the owner's approval") {
		t.Fatalf("configured tool is not gated: %q", gated)
	}
	if plain == "" || strings.Contains(plain, "requires the owner's approval") {
		t.Fatalf("an unconfigured tool was gated: %q", plain)
	}
}

// Nothing configured, nothing paid for: no wrapper on any tool, and no
// executor registered for an action that can never be requested.
func TestNewAppLeavesMCPToolsUngatedWhenNothingRequiresApproval(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.MCP.Servers = map[string]config.MCPServerConfig{
		"railway": {Enabled: true, URL: "https://mcp.railway.com", Transport: "streamable-http", Auth: "oauth", ToolFilter: config.MCPToolFilterConfig{Include: []string{"list-projects"}}},
	}
	secrets := appTestSecrets("deepseek")
	secrets.EncryptionKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, listing := range app.tools.Catalog() {
		if strings.Contains(listing.Definition.Description, "requires the owner's approval") {
			t.Fatalf("tool %q was gated with no require_approval configured", listing.Definition.Name)
		}
	}
}

// /restart has to be a real request to the supervisor, and the reply has to
// survive it: Run drains in-flight turns rather than cancelling them, so the
// acknowledgement is delivered before the daemon comes apart. A restart that
// cut off its own confirmation would leave the owner unable to tell a reload
// from a crash.
func TestRestartCommandStopsRunAfterDeliveringItsAcknowledgement(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	delivered := make(chan string, 4)
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "sendMessage") {
			body, _ := io.ReadAll(request.Body)
			delivered <- string(body)
		}
		return appJSON(200, `{"ok":true,"result":{}}`), nil
	})}
	app, err := NewApp(cfg, appTestSecrets("key"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErrors := make(chan error, 1)
	go func() { runErrors <- app.Run(ctx) }()

	body := `{"update_id":1,"message":{"message_id":1,"from":{"id":42},"chat":{"id":42},"text":"/restart"}}`
	request := httptest.NewRequest(http.MethodPost, cfg.Server.TelegramWebhookPath, strings.NewReader(body))
	request.Header.Set("X-Telegram-Bot-Api-Secret-Token", "webhook")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d", response.Code)
	}

	select {
	case err := <-runErrors:
		if !errors.Is(err, ErrRestart) {
			t.Fatalf("Run returned %v, want ErrRestart", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to stop for the restart")
	}
	// Run returned, which means workers.Wait already drained: the reply must
	// be here without waiting for it.
	select {
	case sent := <-delivered:
		if !strings.Contains(sent, "Restarting") {
			t.Fatalf("delivered %q, want the restart acknowledgement", sent)
		}
	default:
		t.Fatal("Run stopped before the restart acknowledgement was delivered")
	}
}
