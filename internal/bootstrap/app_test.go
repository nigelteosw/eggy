package bootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/events"
)

func TestAppComposesReadyServiceAndHandlesCommandsAndAssistantTurns(t *testing.T) {
	dataDir := t.TempDir()
	cfg := appTestConfig(dataDir)
	secrets := Secrets{TelegramBotToken: "bot", TelegramWebhookSecret: "webhook", DeepSeekAPIKey: "deepseek"}
	var mu sync.Mutex
	var telegramBodies [][]byte
	var modelBody []byte
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
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"Hello from Eggy."}}]}`), nil
		}
		return appJSON(404, `{}`), nil
	})}
	app, err := NewApp(cfg, secrets, AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", DeepSeekEndpoint: "https://deepseek.test/chat", Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Ready(); err != nil {
		t.Fatal(err)
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
	if !strings.Contains(string(modelBody), "Eggy Memory") {
		t.Fatalf("memory missing from model request: %s", modelBody)
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

func TestCommandServiceSupportsOperationalShortcuts(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	app, err := NewApp(cfg, Secrets{TelegramBotToken: "bot", TelegramWebhookSecret: "webhook", DeepSeekAPIKey: "deepseek"}, AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"/status", "/repositories", "/runs", "/schedules", "/memory", "/new"} {
		output, handled, err := app.commands.Execute(context.Background(), command)
		if err != nil || !handled || output == "" {
			t.Fatalf("%s output=%q handled=%v err=%v", command, output, handled, err)
		}
	}
	if _, handled, _ := app.commands.Execute(context.Background(), "/unknown"); handled {
		t.Fatal("unknown command handled")
	}
}

func TestCalendarAuthCommandCreatesShortLivedOwnerEnrollment(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Calendar = CalendarConfig{Enabled: true, DefaultCalendar: "primary", Timezone: "UTC"}
	secrets := Secrets{TelegramBotToken: "bot", TelegramWebhookSecret: "webhook", DeepSeekAPIKey: "deepseek", GoogleClientID: "client", GoogleClientSecret: "secret", EncryptionKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	output, handled, err := app.ExecuteCommand(context.Background(), "/calendar-auth")
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
	app, err := NewApp(cfg, Secrets{TelegramBotToken: "bot", TelegramWebhookSecret: "webhook", DeepSeekAPIKey: "key"}, AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", DeepSeekEndpoint: "https://deepseek.test/chat"})
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

func appTestConfig(dataDir string) Config {
	return Config{
		Version: 1, DataDir: dataDir,
		Server:    ServerConfig{Listen: ":8080", PublicBaseURL: "https://eggy.test", TelegramWebhookPath: "/webhooks/telegram"},
		Telegram:  TelegramConfig{OwnerID: 42},
		Models:    ModelsConfig{Flash: ModelConfig{Adapter: "deepseek", ID: "flash"}, Pro: ModelConfig{Adapter: "deepseek", ID: "pro"}, Escalation: EscalationConfig{ToolSteps: 3, RecoverableFailures: 2}},
		Runner:    RunnerConfig{Root: filepath.Join(dataDir, "runs"), Timeout: Duration(time.Minute), Retention: Duration(time.Minute), MaxOutputBytes: 1 << 20, AllowedEnv: []string{"PATH"}},
		Scheduler: SchedulerConfig{HeartbeatCadence: Duration(30 * time.Minute), QuietHours: QuietHoursConfig{Start: "22:00", End: "07:00", Timezone: "UTC"}, MinimumProactiveInterval: Duration(time.Hour), WeeklyProactiveLimit: 3},
	}
}

type appRoundTrip func(*http.Request) (*http.Response, error)

func (f appRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
func appJSON(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
