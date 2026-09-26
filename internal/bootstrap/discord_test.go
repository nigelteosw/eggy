package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/channel/discord"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/approvals"
	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/core/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

// fakeDiscord is the transport the wiring tests run over: DM verdicts and
// a record of every send, with a gateway that opens and closes on demand.
type fakeDiscord struct {
	mu       sync.Mutex
	channels map[string]discord.DMInfo
	sent     []string
	opened   int
	closed   int
	handle   func(context.Context, discord.Inbound)
	ctx      context.Context
}

func newFakeDiscord() *fakeDiscord {
	return &fakeDiscord{channels: map[string]discord.DMInfo{
		"dm-nigel":   {OneToOne: true, RecipientID: "9001"},
		"dm-partner": {OneToOne: true, RecipientID: "77"},
		"guild":      {},
	}}
}

func (f *fakeDiscord) SendMessage(_ context.Context, channelID, content string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, channelID+":"+content)
	return "m1", nil
}
func (f *fakeDiscord) EditMessage(context.Context, string, string, string) error { return nil }
func (f *fakeDiscord) Typing(context.Context, string) error                      { return nil }
func (f *fakeDiscord) DMChannel(_ context.Context, channelID string) (discord.DMInfo, error) {
	info, ok := f.channels[channelID]
	if !ok {
		return discord.DMInfo{}, errors.New("unknown channel")
	}
	return info, nil
}
func (f *fakeDiscord) Open(ctx context.Context, handle func(context.Context, discord.Inbound)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened++
	f.handle, f.ctx = handle, ctx
	return nil
}
func (f *fakeDiscord) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}
func (f *fakeDiscord) gateway() func(context.Context, discord.Inbound) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handle
}
func (f *fakeDiscord) sends() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

// discordAccountConfig is the pairing config with Discord on and both
// accounts linked: nigel to 9001, partner to 77.
func discordAccountConfig() string {
	body := strings.Replace(pairingAccountConfig(), "  - id: nigel\n", "  - id: nigel\n    discord_user_id: \"9001\"\n", 1)
	return strings.Replace(body, "data_dir: /data\n", "", 1)
}

// newDiscordApp boots an App from a live account config with faked
// adapters, then swaps in the fake Discord transport so the whole wiring --
// intake, dispatch, turn, delivery -- runs without a gateway.
func newDiscordApp(t *testing.T) (*App, *fakeDiscord, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
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
	transport := newFakeDiscord()
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true, ConfigPath: configPath, Getenv: getenv, discordTransport: transport})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.database.Close() })
	return app, transport, configPath
}

// drain runs every queued event to completion, the way Run's loop would.
func drain(t *testing.T, app *App) {
	t.Helper()
	for {
		select {
		case event := <-app.eventQueue:
			if err := app.HandleEvent(context.Background(), event); err != nil {
				t.Fatalf("event %s: %v", event.ID, err)
			}
		default:
			return
		}
	}
}

func TestDiscordDisabledConstructsNothing(t *testing.T) {
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	if app.discord.client != nil || app.discord.channel != nil || app.discord.running() {
		t.Fatalf("discord=%+v, want the zero value", app.discord)
	}
	if err := app.discord.open(context.Background(), app.logger); err != nil {
		t.Fatal(err)
	}
	app.discord.close(app.logger)
}

func TestDiscordEnabledWithoutATokenRunsNoBot(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Discord.Enabled = true
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{HTTPClient: offlineClient(), TelegramBaseURL: "https://telegram.test"})
	if err != nil {
		t.Fatalf("a missing bot token must not fail boot: %v", err)
	}
	if app.discord.running() {
		t.Fatal("a bot ran without a token")
	}
}

func TestDiscordEnabledWithATokenBuildsOneClient(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Discord.Enabled = true
	secrets := appTestSecrets("deepseek")
	secrets.DiscordBotToken = "bot-token"
	app, err := NewApp(cfg, secrets, AppOptions{HTTPClient: offlineClient(), TelegramBaseURL: "https://telegram.test"})
	if err != nil {
		t.Fatal(err)
	}
	if !app.discord.running() || app.discord.channel == nil {
		t.Fatal("expected a Discord client and channel")
	}
	// Constructed, not connected: Open is Run's job.
	app.discord.close(app.logger)
	app.discord.close(app.logger)
}

func TestDiscordOwnerTurnRepliesInTheSameDMAndOwnersAreIsolated(t *testing.T) {
	app, transport, _ := newDiscordApp(t)
	intake := app.discord.intake(app.logger)
	ctx := context.Background()
	intake(ctx, discord.Inbound{MessageID: "1", ChannelID: "dm-nigel", AuthorID: "9001", Content: "hello"})
	intake(ctx, discord.Inbound{MessageID: "2", ChannelID: "dm-partner", AuthorID: "77", Content: "hi"})
	// Guild traffic from an owner and a stranger's DM start nothing.
	intake(ctx, discord.Inbound{MessageID: "3", ChannelID: "guild", GuildID: "g", AuthorID: "9001", Content: "hello"})
	drain(t, app)
	sends := transport.sends()
	if len(sends) != 2 || !strings.HasPrefix(sends[0], "dm-nigel:") || !strings.HasPrefix(sends[1], "dm-partner:") {
		t.Fatalf("sends=%v", sends)
	}
	// Each owner's history is their own conversation.
	nigelCtx := ports.WithPrincipal(ctx, ports.Principal{AccountID: "nigel"})
	recent, err := app.database.RecentMessages(nigelCtx, "discord:dm:dm-nigel", 10)
	if err != nil || len(recent) == 0 {
		t.Fatalf("nigel history=%v err=%v", recent, err)
	}
	partnerCtx := ports.WithPrincipal(ctx, ports.Principal{AccountID: "partner"})
	if crossed, err := app.database.RecentMessages(partnerCtx, "discord:dm:dm-nigel", 10); err != nil || len(crossed) != 0 {
		t.Fatalf("partner can read nigel's DM history: %v err=%v", crossed, err)
	}
}

func TestDiscordUnlinkBlocksDeliveryForWorkInFlight(t *testing.T) {
	app, transport, configPath := newDiscordApp(t)
	ctx := destination.With(ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "nigel"}), destination.Destination{Kind: destination.Discord, ChannelID: "dm-nigel"})
	if err := app.channel.Deliver(ctx, "before"); err != nil {
		t.Fatal(err)
	}
	if err := config.UnlinkDiscordAccount(configPath, "nigel"); err != nil {
		t.Fatal(err)
	}
	if err := app.channel.Deliver(ctx, "after"); err == nil {
		t.Fatal("delivery continued after the link was removed")
	}
	if sends := transport.sends(); len(sends) != 1 {
		t.Fatalf("sends=%v", sends)
	}
	// And intake refuses the now-unknown user with a denial, not a turn.
	app.discord.intake(app.logger)(context.Background(), discord.Inbound{MessageID: "9", ChannelID: "dm-nigel", AuthorID: "9001", Content: "still me"})
	drain(t, app)
	sends := transport.sends()
	if len(sends) != 2 || !strings.Contains(sends[1], "doesn't know you") {
		t.Fatalf("sends=%v", sends)
	}
}

func TestDiscordApprovalIsANoticeAndTheDecisionReturnsToTheDM(t *testing.T) {
	app, transport, _ := newDiscordApp(t)
	nigel := destination.With(ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "nigel"}), destination.Destination{Kind: destination.Discord, ChannelID: "dm-nigel"})
	asker := &approvalAsker{service: app.approvals, channel: app.channel}
	approval, err := asker.Request(nigel, approvals.Action("test_action"), map[string]string{"id": "evt-1"}, "Run action")
	if err != nil {
		t.Fatal(err)
	}
	sends := transport.sends()
	if len(sends) != 1 || !strings.Contains(sends[0], "Run action") || !strings.Contains(sends[0], "web panel") {
		t.Fatalf("sends=%v", sends)
	}
	// Owner B cannot decide owner A's approval: it is not in B's state.
	payload, _ := json.Marshal(events.ApprovalDecision{ApprovalID: approval.ID, Approved: false})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "decision-b", Type: events.TypeApproval, Owner: "partner", Payload: payload}); err == nil {
		t.Fatal("partner decided nigel's approval")
	}
	// Owner A's decision, arriving from the web panel with no destination
	// of its own, is answered in the DM the approval was raised in.
	if err := app.HandleEvent(context.Background(), events.Event{ID: "decision-a", Type: events.TypeApproval, Owner: "nigel", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	sends = transport.sends()
	if len(sends) != 2 || sends[1] != "dm-nigel:Action rejected." {
		t.Fatalf("sends=%v", sends)
	}
}

func TestDiscordGatewayOpensOnRunAndClosesOnceOnRestart(t *testing.T) {
	app, transport, _ := newDiscordApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	// A message arriving through the opened gateway becomes a turn.
	deadline := time.Now().Add(5 * time.Second)
	for transport.gateway() == nil {
		if time.Now().After(deadline) {
			t.Fatal("Run did not open the gateway")
		}
		time.Sleep(10 * time.Millisecond)
	}
	transport.gateway()(context.Background(), discord.Inbound{MessageID: "1", ChannelID: "dm-nigel", AuthorID: "9001", Content: "hello"})
	deadline = time.Now().Add(5 * time.Second)
	for len(transport.sends()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no reply through the opened gateway")
		}
		time.Sleep(10 * time.Millisecond)
	}
	app.Restart()
	if err := <-done; !errors.Is(err, ErrRestart) {
		t.Fatalf("run=%v", err)
	}
	if transport.opened != 1 || transport.closed != 1 {
		t.Fatalf("opened=%d closed=%d", transport.opened, transport.closed)
	}
}

// offlineClient answers every outbound call as Telegram would, so a
// non-fake App boots without the network.
func offlineClient() *http.Client {
	return &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "getMe") {
			return appJSON(200, `{"ok":true,"result":{"username":"eggy_bot"}}`), nil
		}
		return appJSON(200, `{"ok":true,"result":{}}`), nil
	})}
}
