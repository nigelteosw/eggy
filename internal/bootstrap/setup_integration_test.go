package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
)

func freshInstallEnv() map[string]string {
	return map[string]string{
		"DEEPSEEK_API_KEY":                "provider-secret",
		"EGGY_GOOGLE_LOGIN_CLIENT_SECRET": "login-secret",
		"TELEGRAM_BOT_TOKEN":              "telegram-bot-token-fixture",
		"TELEGRAM_WEBHOOK_SECRET":         "telegram-webhook-fixture",
		"EGGY_ENCRYPTION_KEY":             "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	}
}

func freshInstallSetupInput() config.SetupInput {
	return config.SetupInput{
		AccountID: "you", GoogleEmail: "you@example.com", PublicBaseURL: "https://eggy.test",
		LoginClientID: "web-client", LoginClientSecretEnv: "EGGY_GOOGLE_LOGIN_CLIENT_SECRET",
		ProviderName: "deepseek", ProviderBaseURL: "https://deepseek.test", ProviderAPIKeyEnv: "DEEPSEEK_API_KEY",
		ModelAlias: "deepseek-pro", ModelID: "deepseek-v4-pro", TelegramEnabled: true,
	}
}

// TestFreshInstallCompletesSetupBootsAndPairsTelegram exercises the whole
// journey Task 10 describes: no config on disk, guided setup writes one
// using only provisioned environment credentials, the resulting App boots,
// and a Telegram sender pairs through the real webhook route and is then
// recognized without a restart. No credential value may reach config.yaml,
// an HTTP response, or a log line anywhere along the way.
func TestFreshInstallCompletesSetupBootsAndPairsTelegram(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.yaml")
	env := freshInstallEnv()
	getenv := func(name string) string { return env[name] }

	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatal("config.yaml already exists before setup")
	}
	input := freshInstallSetupInput()
	if err := config.CompleteSetup(home, configPath, input, getenv); err != nil {
		t.Fatalf("CompleteSetup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".env")); !os.IsNotExist(err) {
		t.Fatal("setup created .env")
	}
	written, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range env {
		if bytes.Contains(written, []byte(secret)) {
			t.Fatalf("a credential value reached config.yaml: %s", written)
		}
	}

	cfg, secrets, err := config.LoadConfig(configPath, getenv)
	if err != nil {
		t.Fatalf("LoadConfig after setup: %v", err)
	}

	var startupLog bytes.Buffer
	var mu sync.Mutex
	var telegramSends []string
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Path, "getMe"):
			return appJSON(200, `{"ok":true,"result":{"username":"eggy_bot"}}`), nil
		case strings.Contains(request.URL.Path, "sendMessage"):
			body, _ := io.ReadAll(request.Body)
			mu.Lock()
			telegramSends = append(telegramSends, string(body))
			mu.Unlock()
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		case strings.Contains(request.URL.Path, "setMyCommands"):
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		case request.URL.Host == "deepseek.test":
			body, _ := io.ReadAll(request.Body)
			if bytes.Contains(body, []byte(secrets.ProviderAPIKeys["deepseek"])) {
				t.Fatal("provider API key reached the outbound model request body")
			}
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"Hello from fresh install."}}]}`), nil
		}
		return appJSON(404, `{}`), nil
	})}
	app, err := NewApp(cfg, secrets, AppOptions{
		HTTPClient: client, TelegramBaseURL: "https://telegram.test",
		ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"},
		Logger:           slog.New(slog.NewJSONHandler(&startupLog, nil)),
		ConfigPath:       configPath, Getenv: getenv,
	})
	if err != nil {
		t.Fatalf("NewApp from setup output: %v", err)
	}
	defer app.database.Close()
	if err := app.Ready(); err != nil {
		t.Fatal(err)
	}
	for _, secret := range env {
		if strings.Contains(startupLog.String(), secret) {
			t.Fatalf("a credential value reached the startup log: %s", startupLog.String())
		}
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	runErrors := make(chan error, 1)
	go func() { runErrors <- app.Run(runCtx) }()
	ctx := context.Background()

	// Pairing: seed a code the way the authenticated panel would, then walk
	// it through the real webhook exactly as Telegram would deliver it.
	var raw [32]byte
	raw[7] = 9
	code := base64.RawURLEncoding.EncodeToString(raw[:])
	hash := sha256.Sum256(raw[:])
	if err := app.database.CreateIdentityLink(ctx, "you", TelegramConnection, hash, time.Now().Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	post := func(sender int64, text string) int {
		body := fmt.Sprintf(`{"update_id":1,"message":{"message_id":1,"from":{"id":%d},"chat":{"id":%d},"text":%q}}`, sender, sender, text)
		request := httptest.NewRequest(http.MethodPost, cfg.Server.TelegramWebhookPath, strings.NewReader(body))
		request.Header.Set("X-Telegram-Bot-Api-Secret-Token", secrets.TelegramWebhookSecret)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		return response.Code
	}
	if status := post(9001, "/start "+code); status != http.StatusNoContent {
		t.Fatalf("pairing status=%d", status)
	}
	linked, err := config.LoadDocument(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if account, ok := linked.AccountForTelegram(9001); !ok || account.ID != "you" {
		t.Fatalf("pairing did not link the account: %#v, %v", account, ok)
	}
	// The newly mapped sender's next message routes to the paired account
	// without a restart: the account directory reads config.yaml live, and
	// the running App never cached its boot-time account list.
	if status := post(9001, "hello again"); status != http.StatusNoContent {
		t.Fatalf("second message status=%d", status)
	}
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		replied := len(telegramSends) > 0
		mu.Unlock()
		if replied {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for the paired sender's reply")
		case <-time.After(10 * time.Millisecond):
		}
	}
	mu.Lock()
	reply := telegramSends[len(telegramSends)-1]
	mu.Unlock()
	if !strings.Contains(reply, "9001") {
		t.Fatalf("reply did not go to the paired sender's chat: %s", reply)
	}

	cancelRun()
	if err := <-runErrors; err != nil && err != context.Canceled {
		t.Fatalf("Run returned %v", err)
	}
}
