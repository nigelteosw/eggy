package bootstrap

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/auth/session"
	"github.com/nigelteosw/eggy/plugins/channels/discord"
)

var webLinkPattern = regexp.MustCompile(`https://eggy\.example/auth/link#token=([A-Za-z0-9_-]{43})`)

// newWebLinkApp boots an App with Telegram and Discord configured, every
// outbound call faked, and the application log captured.
func newWebLinkApp(t *testing.T) (app *App, configPath string, sends func() []string, logs *bytes.Buffer, transport *fakeDiscord) {
	t.Helper()
	dir := t.TempDir()
	configPath = filepath.Join(dir, "config.yaml")
	body := strings.Replace(discordAccountConfig(), "server:\n", "data_dir: "+dir+"\nserver:\n", 1)
	body = strings.Replace(body, "root: /data/runs", "root: "+filepath.Join(dir, "runs"), 1)
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		return map[string]string{"EGGY_UI_USER_EMAIL": "owner@example.com", "EGGY_UI_PASSWORD": "operator-password", "MODEL_KEY": "k", "TELEGRAM_BOT_TOKEN": "t", "TELEGRAM_WEBHOOK_SECRET": "w", "EGGY_ENCRYPTION_KEY": strings.Repeat("A", 43) + "="}[key]
	}
	cfg, secrets, err := config.LoadConfig(configPath, getenv)
	if err != nil {
		t.Fatal(err)
	}
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
			return appJSON(200, `{"ok":true,"result":{"message_id":1}}`), nil
		case strings.Contains(request.URL.Path, "setMyCommands"), strings.Contains(request.URL.Path, "sendChatAction"):
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		case request.URL.Host == "model.test":
			return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"noted"}}]}`), nil
		}
		return appJSON(404, `{}`), nil
	})}
	logs = &bytes.Buffer{}
	transport = newFakeDiscord()
	app, err = NewApp(cfg, secrets, AppOptions{
		HTTPClient: client, TelegramBaseURL: "https://telegram.test",
		ProviderBaseURLs: map[string]string{"provider": "https://model.test"},
		Logger:           slog.New(slog.NewJSONHandler(logs, nil)),
		ConfigPath:       configPath, Getenv: getenv, discordTransport: transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.database.Close() })
	sends = func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), telegramSends...)
	}
	return app, configPath, sends, logs, transport
}

func postTelegram(app *App, updateID int, sender int64, text string) int {
	body := fmt.Sprintf(`{"update_id":%d,"message":{"message_id":%d,"from":{"id":%d},"chat":{"id":%d},"text":%q}}`, updateID, updateID, sender, sender, text)
	request := httptest.NewRequest(http.MethodPost, "/webhooks/telegram", strings.NewReader(body))
	request.Header.Set("X-Telegram-Bot-Api-Secret-Token", "w")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	return response.Code
}

// drainLenient runs every queued event, tolerating delivery failures.
func drainLenient(app *App) {
	for {
		select {
		case event := <-app.eventQueue:
			_ = app.HandleEvent(context.Background(), event)
		default:
			return
		}
	}
}

func countLinks(t *testing.T, app *App) int {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(app.config.DataDir, "eggy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM web_login_links`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTelegramWebMintsALinkOnlyFromTheVerifiedPrivateChat(t *testing.T) {
	app, configPath, sends, logs, transport := newWebLinkApp(t)
	ctx := context.Background()

	// 1. partner (sender 77) asks /web in the private chat.
	if code := postTelegram(app, 1, 77, "/web"); code != http.StatusNoContent {
		t.Fatalf("webhook status=%d", code)
	}
	drain(t, app)
	replies := sends()
	if len(replies) != 1 {
		t.Fatalf("replies=%v", replies)
	}
	match := webLinkPattern.FindStringSubmatch(replies[0])
	if match == nil {
		t.Fatalf("reply carries no link: %s", replies[0])
	}
	token := match[1]
	link, err := app.database.WebLoginLink(ctx, session.HashToken(token), time.Now())
	if err != nil || link.AccountID != "partner" || link.SenderID != "77" {
		t.Fatalf("stored link=%+v err=%v", link, err)
	}

	// 2. The token is in the chat reply and nowhere durable.
	partner := ports.WithPrincipal(ctx, ports.Principal{AccountID: "partner"})
	recent, err := app.database.RecentMessages(partner, "telegram", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range recent {
		if strings.Contains(message.Content, token) {
			t.Fatal("the login token was recorded in the conversation")
		}
	}
	traces, err := app.database.ListTraces(partner, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range traces {
		trace, spans, _, err := app.database.Trace(partner, summary.ID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(trace.Input, token) || strings.Contains(trace.Output, token) {
			t.Fatal("the login token was recorded in a trace")
		}
		for _, span := range spans {
			if strings.Contains(span.Request, token) || strings.Contains(span.Response, token) {
				t.Fatal("the login token was recorded in a trace span")
			}
		}
	}
	if strings.Contains(logs.String(), token) {
		t.Fatal("the login token reached the application log")
	}

	// 3. The browser redeems it with an explicit POST and lands as partner.
	request := httptest.NewRequest(http.MethodPost, "/api/login/link", strings.NewReader(`{"token":"`+token+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://eggy.example")
	request.Header.Set("X-Eggy-Login", "1")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 {
		t.Fatalf("redeem status=%d body=%s", response.Code, response.Body.String())
	}
	if id, _ := sessionOf(t, app, response.Result().Cookies()[0]); id != "partner" {
		t.Fatalf("link session resolved to %q", id)
	}

	// 4. A /web queued before the sender is reassigned mints for nobody:
	// not the old owner, whose chat it no longer is, and not the new one,
	// who did not ask.
	if code := postTelegram(app, 2, 77, "/web"); code != http.StatusNoContent {
		t.Fatalf("webhook status=%d", code)
	}
	if err := config.UnlinkTelegramAccount(configPath, "partner"); err != nil {
		t.Fatal(err)
	}
	if err := config.LinkTelegramAccount(configPath, "nigel", 77); err != nil {
		t.Fatal(err)
	}
	before := countLinks(t, app)
	drainLenient(app)
	if countLinks(t, app) != before {
		t.Fatal("a reassigned sender minted a link")
	}
	for _, reply := range sends()[1:] {
		if webLinkPattern.MatchString(reply) {
			t.Fatalf("a link was sent after reassignment: %s", reply)
		}
	}

	// 5. /web from a Discord DM is the bare address.
	app.discord.intake(app.logger)(ctx, discord.Inbound{MessageID: "1", ChannelID: "dm-nigel", AuthorID: "9001", Content: "/web"})
	drainLenient(app)
	discordSends := transport.sends()
	if len(discordSends) == 0 || !strings.Contains(discordSends[len(discordSends)-1], "https://eggy.example") || strings.Contains(discordSends[len(discordSends)-1], "token=") {
		t.Fatalf("discord reply=%v", discordSends)
	}
	if countLinks(t, app) != before {
		t.Fatal("a Discord message minted a link")
	}

	// 6. A schedule whose text is /web is a turn, not a command.
	nigel := ports.WithPrincipal(ctx, ports.Principal{AccountID: "nigel"})
	now := time.Now()
	if err := app.scheduler.Add(nigel, ports.Schedule{ID: "web-later", Kind: ports.ScheduleExact, Execution: ports.ScheduleExecutionAgent, Instruction: "/web", NextRun: now.Add(-time.Minute), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := app.onScheduleTick(ctx, now); err != nil {
		t.Fatal(err)
	}
	app.workers.Wait()
	drainLenient(app)
	if countLinks(t, app) != before {
		t.Fatal("a scheduled /web minted a link")
	}
	// Duplicate update: replayed update IDs are dropped by dedup, so no
	// second link for a resent webhook.
	if err := config.UnlinkTelegramAccount(configPath, "nigel"); err != nil {
		t.Fatal(err)
	}
	if err := config.LinkTelegramAccount(configPath, "partner", 77); err != nil {
		t.Fatal(err)
	}
	if code := postTelegram(app, 3, 77, "/web"); code != http.StatusNoContent {
		t.Fatalf("webhook status=%d", code)
	}
	drain(t, app)
	minted := countLinks(t, app)
	if minted != before+1 {
		t.Fatalf("links after a fresh /web=%d want %d", minted, before+1)
	}
	if code := postTelegram(app, 3, 77, "/web"); code != http.StatusNoContent {
		t.Fatalf("duplicate webhook status=%d", code)
	}
	drainLenient(app)
	if countLinks(t, app) != minted {
		t.Fatal("a duplicate update minted a second link")
	}
}
