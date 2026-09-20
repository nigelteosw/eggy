package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// accountConfig is validConfig with the legacy owner replaced by an explicit
// account list and the binding that says which account the environment
// credentials sign in.
func accountConfig() string {
	body := strings.Replace(validConfig(), "telegram:\n  owner_id: 42\n", `accounts:
  - id: nigel
    telegram_user_id: 42
  - id: partner
web:
  password_account_id: nigel
`, 1)
	return body
}

// googleAccountConfig is the shape the previous release wrote: the same two
// people with Google addresses and the inbound login client. Only the
// migration reads it now.
func googleAccountConfig() string {
	body := strings.Replace(validConfig(), "telegram:\n  owner_id: 42\n", `accounts:
  - id: nigel
    google_email: nigel@example.com
    telegram_user_id: 42
  - id: partner
    google_email: Partner@Example.com
web:
  google_login:
    client_id: web-client.apps.googleusercontent.com
    client_secret_env: EGGY_GOOGLE_LOGIN_CLIENT_SECRET
`, 1)
	return body
}

func accountSecrets() map[string]string {
	env := testSecrets()
	env["EGGY_UI_USER_EMAIL"] = "owner@example.com"
	env["EGGY_UI_PASSWORD"] = "existing-owner-password"
	return env
}

func TestAccountConfigLoads(t *testing.T) {
	cfg, secrets, err := loadText(t, accountConfig(), accountSecrets())
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.AccountMode() {
		t.Fatal("explicit accounts must put the deployment in account mode")
	}
	accounts := cfg.Principals()
	if len(accounts) != 2 || accounts[0].ID != "nigel" || accounts[1].ID != "partner" {
		t.Fatalf("Principals() = %#v", accounts)
	}
	if cfg.PasswordAccountID() != "nigel" {
		t.Fatalf("PasswordAccountID() = %q", cfg.PasswordAccountID())
	}
	if secrets.UIUserEmail != "owner@example.com" || secrets.UIPassword != "existing-owner-password" {
		t.Fatalf("environment login = %#v", secrets)
	}
	if account, ok := cfg.AccountForUsername(" OWNER@example.com ", secrets.UIUserEmail); !ok || account.ID != "nigel" {
		t.Fatalf("alias must resolve the password account, got %#v %v", account, ok)
	}
	if account, ok := cfg.AccountForUsername(" partner ", secrets.UIUserEmail); !ok || account.ID != "partner" {
		t.Fatalf("id must resolve exactly, got %#v %v", account, ok)
	}
	if _, ok := cfg.AccountForUsername("Partner", secrets.UIUserEmail); ok {
		t.Fatal("ids are not matched case-insensitively at login")
	}
	if account, ok := cfg.AccountForTelegram(42); !ok || account.ID != "nigel" {
		t.Fatalf("AccountForTelegram(42) = %#v, %v", account, ok)
	}
	if _, ok := cfg.AccountForTelegram(7); ok {
		t.Fatal("unmapped telegram sender must not resolve")
	}
	if _, ok := cfg.Account("nobody"); ok {
		t.Fatal("unknown account must not resolve")
	}
	if !cfg.TelegramEnabled() {
		t.Fatal("an account with a telegram id makes telegram a channel")
	}
}

func TestTelegramEnabledSupportsExplicitAndLegacyStates(t *testing.T) {
	enabled, disabled := true, false
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"explicit enabled without pairing", Config{Accounts: []AccountConfig{{ID: "you"}}, Telegram: TelegramConfig{Enabled: &enabled}}, true},
		{"explicit disabled overrides pairing", Config{Accounts: []AccountConfig{{ID: "you", TelegramUserID: 42}}, Telegram: TelegramConfig{Enabled: &disabled}}, false},
		{"absent infers paired account", Config{Accounts: []AccountConfig{{ID: "you", TelegramUserID: 42}}}, true},
		{"absent legacy owner", Config{Owner: OwnerConfig{ID: "42"}, Telegram: TelegramConfig{OwnerID: 42}}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.TelegramEnabled(); got != tt.want {
				t.Fatalf("TelegramEnabled()=%v want %v", got, tt.want)
			}
		})
	}
}

func TestLinkAndUnlinkTelegramAccount(t *testing.T) {
	path := writeAccountConfig(t)
	if err := SetTelegramEnabled(path, true); err != nil {
		t.Fatal(err)
	}
	if err := UnlinkTelegramAccount(path, "nigel"); err != nil {
		t.Fatal(err)
	}
	if err := LinkTelegramAccount(path, "partner", 77); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if account, _ := cfg.Account("partner"); account.TelegramUserID != 77 {
		t.Fatalf("partner=%#v", account)
	}
	before, _ := os.ReadFile(path)
	for _, attempt := range []struct {
		id   string
		user int64
	}{
		{"missing", 88},
		{"nigel", 77},
		{"nigel", 0},
	} {
		if err := LinkTelegramAccount(path, attempt.id, attempt.user); err == nil {
			t.Fatalf("LinkTelegramAccount(%q,%d) succeeded", attempt.id, attempt.user)
		}
		after, _ := os.ReadFile(path)
		if string(after) != string(before) {
			t.Fatal("refused link changed config")
		}
	}
}

func TestLegacyOwnerNormalizesToOnePrincipal(t *testing.T) {
	cfg, _, err := loadText(t, validConfig(), testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccountMode() {
		t.Fatal("legacy owner config is not account mode")
	}
	accounts := cfg.Principals()
	if len(accounts) != 1 || accounts[0].ID != "42" || accounts[0].TelegramUserID != 42 {
		t.Fatalf("Principals() = %#v", accounts)
	}
	if account, ok := cfg.AccountForTelegram(42); !ok || account.ID != "42" {
		t.Fatalf("AccountForTelegram(42) = %#v, %v", account, ok)
	}
	if !cfg.TelegramEnabled() {
		t.Fatal("legacy telegram owner keeps telegram enabled")
	}
}

func TestAccountConfigRejections(t *testing.T) {
	replace := func(old, new string) string { return strings.Replace(accountConfig(), old, new, 1) }
	cases := []struct {
		name string
		body string
		env  map[string]string
		want string
	}{
		{name: "duplicate id", body: replace("id: partner", "id: nigel"), want: "duplicate account id"},
		{name: "duplicate id differing by case", body: replace("id: partner", "id: Nigel"), want: "duplicate account id"},
		{name: "duplicate telegram id", body: replace("  - id: partner\n", "  - id: partner\n    telegram_user_id: 42\n"), want: "duplicate account telegram_user_id"},
		{name: "negative telegram id", body: replace("telegram_user_id: 42", "telegram_user_id: -1"), want: "telegram_user_id must be positive"},
		{name: "unsafe path id", body: replace("id: partner", "id: ../partner"), want: "account id"},
		{name: "slash in id", body: replace("id: partner", "id: a/b"), want: "account id"},
		{name: "dot id", body: replace("id: partner", "id: ."), want: "account id"},
		{name: "empty id", body: replace("id: partner", "id: ''"), want: "account id"},
		{name: "empty account list", body: replace("accounts:\n  - id: nigel\n    telegram_user_id: 42\n  - id: partner\n", "accounts: []\n"), want: "accounts must list at least one account"},
		{name: "stray role field", body: replace("  - id: partner\n", "  - id: partner\n    role: admin\n"), want: "field role not found"},
		{name: "retired google email", body: replace("  - id: partner\n", "  - id: partner\n    google_email: partner@example.com\n"), want: "migrate-local-login"},
		{name: "retired google login client", body: replace("web:\n", "web:\n  google_login:\n    client_id: x\n"), want: "migrate-local-login"},
		{name: "missing password account", body: replace("web:\n  password_account_id: nigel\n", ""), want: "web.password_account_id is required"},
		{name: "dangling password account", body: replace("password_account_id: nigel", "password_account_id: stranger"), want: "web.password_account_id"},
		{name: "missing environment login", body: accountConfig(), env: testSecrets(), want: "EGGY_UI_USER_EMAIL"},
		{name: "partial environment login", body: accountConfig(), env: func() map[string]string {
			env := accountSecrets()
			delete(env, "EGGY_UI_PASSWORD")
			return env
		}(), want: "EGGY_UI_PASSWORD"},
		{name: "alias collides with another account", body: accountConfig(), env: func() map[string]string {
			env := accountSecrets()
			env["EGGY_UI_USER_EMAIL"] = "PARTNER"
			return env
		}(), want: "collides with account"},
		{name: "overlong environment password", body: accountConfig(), env: func() map[string]string {
			env := accountSecrets()
			env["EGGY_UI_PASSWORD"] = strings.Repeat("x", 257)
			return env
		}(), want: "256 bytes"},
		{name: "legacy owner beside accounts", body: replace("accounts:", "owner:\n  id: 42\naccounts:"), want: "owner.id"},
		{name: "legacy telegram owner beside accounts", body: replace("accounts:", "telegram:\n  owner_id: 42\naccounts:"), want: "telegram.owner_id"},
		{name: "unknown migration owner", body: replace("accounts:", "migration_owner_id: stranger\naccounts:"), want: "migration_owner_id"},
		{name: "legacy binding names a stranger", body: strings.Replace(validConfig(), "agent:", "web:\n  password_account_id: stranger\nagent:", 1), want: "web.password_account_id"},
		{name: "migration owner without accounts", body: strings.Replace(validConfig(), "agent:", "migration_owner_id: nigel\nagent:", 1), want: "accounts must list at least one account"},
		{name: "expected email malformed", body: replace("agent:", "google:\n  expected_email: eggy\nagent:"), want: "google.expected_email"},
		{name: "google enabled in account mode needs expected email", body: replace("agent:", "google:\n  enabled: true\n  client_id: desktop\n  products: [gmail]\nagent:"), want: "google.expected_email"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.env
			if env == nil {
				env = accountSecrets()
			}
			_, _, err := loadText(t, tc.body, env)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestAccountConfigAcceptsAnyCount(t *testing.T) {
	body := strings.Replace(accountConfig(), "web:", "  - id: third\n  - id: fourth\nweb:", 1)
	cfg, _, err := loadText(t, body, accountSecrets())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Accounts) != 4 {
		t.Fatalf("len(Accounts) = %d", len(cfg.Accounts))
	}
}

func TestExpectedGoogleEmailIsNormalized(t *testing.T) {
	body := strings.Replace(accountConfig(), "agent:", "google:\n  expected_email: ' Eggy@Example.com '\nagent:", 1)
	cfg, _, err := loadText(t, body, accountSecrets())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Google.ExpectedEmail != "eggy@example.com" {
		t.Fatalf("ExpectedEmail = %q", cfg.Google.ExpectedEmail)
	}
}

func writeAccountConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(accountConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAccountMutations(t *testing.T) {
	path := writeAccountConfig(t)
	if err := AddAccount(path, AccountInput{ID: "third", TelegramUserID: 99}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if account, ok := cfg.Account("third"); !ok || account.TelegramUserID != 99 {
		t.Fatalf("added account = %#v, %v", account, ok)
	}
	if err := AddAccount(path, AccountInput{ID: "third"}); err == nil {
		t.Fatal("adding a duplicate id must fail")
	}
	if err := AddAccount(path, AccountInput{ID: "THIRD"}); err == nil {
		t.Fatal("adding a duplicate id differing by case must fail")
	}
	if err := AddAccount(path, AccountInput{ID: "fifth", TelegramUserID: 42}); err == nil {
		t.Fatal("adding a duplicate telegram sender must fail")
	}
	checked := errors.New("retired in the store")
	if err := AddAccountChecked(path, AccountInput{ID: "fourth"}, func(id string) error {
		if id != "fourth" {
			t.Fatalf("check saw %q", id)
		}
		return checked
	}); !errors.Is(err, checked) {
		t.Fatalf("store check must refuse the append: %v", err)
	}
	if cfg, _ := LoadDocument(path); len(cfg.Accounts) != 3 {
		t.Fatalf("refused account was written: %#v", cfg.Accounts)
	}
	if err := EditAccount(path, "third", AccountInput{TelegramUserID: 0}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = LoadDocument(path)
	if account, _ := cfg.Account("third"); account.TelegramUserID != 0 {
		t.Fatalf("edited account = %#v", account)
	}
	if err := EditAccount(path, "missing", AccountInput{}); err == nil {
		t.Fatal("editing an unknown account must fail")
	}
	if err := RemoveAccount(path, "third"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAccount(path, "third"); err == nil {
		t.Fatal("removing an unknown account must fail")
	}
	// The password account is bound to the environment credentials; taking
	// it out would leave those credentials signing in nobody.
	if err := RemoveAccount(path, "nigel"); err == nil || !strings.Contains(err.Error(), "environment credentials") {
		t.Fatalf("removing the bound account: %v", err)
	}
	if err := RemoveAccount(path, "partner"); err != nil {
		t.Fatal(err)
	}
	// nigel is now the last account as well as the bound one.
	if err := RemoveAccount(path, "nigel"); err == nil {
		t.Fatal("removing the last account must fail")
	}
}

func TestExpectedEmailMutation(t *testing.T) {
	path := writeAccountConfig(t)
	if err := SetExpectedGoogleEmail(path, " Eggy@Example.com "); err != nil {
		t.Fatal(err)
	}
	if err := SetExpectedGoogleEmail(path, "nope"); err == nil {
		t.Fatal("malformed expected email must fail")
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Google.ExpectedEmail != "eggy@example.com" {
		t.Fatalf("expected email = %q", cfg.Google.ExpectedEmail)
	}
}

func TestMigrationOwnerMutation(t *testing.T) {
	path := writeAccountConfig(t)
	if err := SetMigrationOwner(path, "stranger"); err == nil {
		t.Fatal("unknown migration owner must fail")
	}
	if err := SetMigrationOwner(path, "nigel"); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MigrationOwnerID != "nigel" {
		t.Fatalf("MigrationOwnerID = %q", cfg.MigrationOwnerID)
	}
}

func TestLoadRecoveryIdentityReadsAccountsFromABrokenConfig(t *testing.T) {
	// A stray key is what puts a deployment in safe mode; the accounts are
	// still readable around it.
	body := strings.Replace(accountConfig(), "agent:", "typo_section: 1\nagent:", 1)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadConfig(path, mapEnv(accountSecrets())); err == nil {
		t.Fatal("the broken config loaded")
	}
	identity, err := LoadRecoveryIdentity(path, mapEnv(accountSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	if !identity.AccountMode || len(identity.Config.Accounts) != 2 || identity.Config.PasswordAccountID() != "nigel" || identity.Secrets.UIPassword != "existing-owner-password" {
		t.Fatalf("identity=%+v", identity)
	}
	// Without the environment login nobody can be identified, and safe
	// mode must say so rather than fall back to anything.
	env := accountSecrets()
	delete(env, "EGGY_UI_PASSWORD")
	if identity, err := LoadRecoveryIdentity(path, mapEnv(env)); err == nil || !identity.AccountMode {
		t.Fatalf("missing secret: identity=%+v err=%v", identity, err)
	}
	// A legacy document binds its owner the same way.
	legacy := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(legacy, []byte("server:\n  public_base_url: https://eggy.example\nowner:\n  id: 42\nbroken: yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if identity, err := LoadRecoveryIdentity(legacy, mapEnv(accountSecrets())); err != nil || identity.AccountMode || identity.Config.PasswordAccountID() != "42" {
		t.Fatalf("legacy: identity=%+v err=%v", identity, err)
	}
}

func TestConvertToAccountsReplacesTheLegacyOwnerInOneWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	input := ConvertInput{
		Accounts:          []AccountInput{{ID: "nigel", TelegramUserID: 42}, {ID: "partner"}},
		PasswordAccountID: "nigel",
	}
	if err := ConvertToAccounts(path, input); err == nil {
		t.Fatal("conversion without a migration owner must fail")
	}
	input.MigrationOwnerID = "nigel"
	if err := ConvertToAccounts(path, input); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadConfig(path, mapEnv(accountSecrets()))
	if err != nil {
		t.Fatalf("converted config does not load: %v", err)
	}
	if !cfg.AccountMode() || cfg.Owner.ID != "" || cfg.Telegram.OwnerID != 0 || cfg.MigrationOwnerID != "nigel" || len(cfg.Accounts) != 2 {
		t.Fatalf("cfg=%+v", cfg)
	}
	if account, _ := cfg.Account("nigel"); account.TelegramUserID != 42 || cfg.PasswordAccountID() != "nigel" {
		t.Fatalf("nigel=%+v bound=%q", account, cfg.PasswordAccountID())
	}
	if err := ConvertToAccounts(path, input); err == nil {
		t.Fatal("converting twice must fail")
	}
}

func TestFirstBootGeneratesAnAccountsConfig(t *testing.T) {
	env := map[string]string{
		"EGGY_ACCOUNTS":              "nigel:42, partner",
		"EGGY_OWNER_ID":              "nigel",
		"EGGY_UI_USER_EMAIL":         "owner@example.com",
		"EGGY_UI_PASSWORD":           "existing-owner-password",
		"EGGY_GOOGLE_EXPECTED_EMAIL": "eggy@example.com",
		"EGGY_PUBLIC_BASE_URL":       "https://eggy.example",
		"DEEPSEEK_API_KEY":           "k",
		"TELEGRAM_BOT_TOKEN":         "t",
		"TELEGRAM_WEBHOOK_SECRET":    "s",
		"EGGY_ENCRYPTION_KEY":        "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, _, err := LoadOrCreateConfig(path, mapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AccountMode() || len(cfg.Accounts) != 2 || cfg.Owner.ID != "" || cfg.Telegram.OwnerID != 0 {
		t.Fatalf("cfg=%+v", cfg)
	}
	if account, _ := cfg.Account("nigel"); account.TelegramUserID != 42 {
		t.Fatalf("nigel=%+v", account)
	}
	if cfg.PasswordAccountID() != "nigel" || cfg.Google.ExpectedEmail != "eggy@example.com" {
		t.Fatalf("bound=%q expected=%q", cfg.PasswordAccountID(), cfg.Google.ExpectedEmail)
	}
	// Several accounts and no EGGY_OWNER_ID: the list's order must not pick.
	delete(env, "EGGY_OWNER_ID")
	if _, _, err := LoadOrCreateConfig(filepath.Join(t.TempDir(), "config.yaml"), mapEnv(env)); err == nil || !strings.Contains(err.Error(), "EGGY_OWNER_ID") {
		t.Fatalf("several accounts without a binding: %v", err)
	}
	// One account binds itself.
	env["EGGY_ACCOUNTS"] = "solo:42"
	cfg, _, err = LoadOrCreateConfig(filepath.Join(t.TempDir(), "config.yaml"), mapEnv(env))
	if err != nil || cfg.PasswordAccountID() != "solo" {
		t.Fatalf("single account: bound=%q err=%v", cfg.PasswordAccountID(), err)
	}
	// The old id:google_email form is refused with migration guidance.
	if _, err := firstBootAccounts("nigel:nigel@example.com:42"); err == nil || !strings.Contains(err.Error(), "migration") {
		t.Fatalf("old form accepted: %v", err)
	}
	if _, err := firstBootAccounts("nigel:abc"); err == nil {
		t.Fatal("malformed entry accepted")
	}
}
