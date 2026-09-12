package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// accountConfig is validConfig with the legacy owner replaced by an explicit
// account list and the inbound login client it requires.
func accountConfig() string {
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
	env["EGGY_GOOGLE_LOGIN_CLIENT_SECRET"] = "login-secret"
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
	if accounts[1].GoogleEmail != "partner@example.com" {
		t.Fatalf("email must be normalized, got %q", accounts[1].GoogleEmail)
	}
	if secrets.GoogleLoginClientSecret != "login-secret" {
		t.Fatalf("login secret = %q", secrets.GoogleLoginClientSecret)
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
		{name: "duplicate email", body: replace("Partner@Example.com", " NIGEL@example.com "), want: "duplicate account google_email"},
		{name: "duplicate telegram id", body: replace("google_email: Partner@Example.com", "google_email: partner@example.com\n    telegram_user_id: 42"), want: "duplicate account telegram_user_id"},
		{name: "negative telegram id", body: replace("telegram_user_id: 42", "telegram_user_id: -1"), want: "telegram_user_id must be positive"},
		{name: "unsafe path id", body: replace("id: partner", "id: ../partner"), want: "account id"},
		{name: "slash in id", body: replace("id: partner", "id: a/b"), want: "account id"},
		{name: "dot id", body: replace("id: partner", "id: ."), want: "account id"},
		{name: "empty id", body: replace("id: partner", "id: ''"), want: "account id"},
		{name: "missing email", body: replace("google_email: Partner@Example.com", "google_email: ''"), want: "google_email"},
		{name: "malformed email", body: replace("Partner@Example.com", "partner"), want: "google_email"},
		{name: "empty account list", body: replace("accounts:\n  - id: nigel\n    google_email: nigel@example.com\n    telegram_user_id: 42\n  - id: partner\n    google_email: Partner@Example.com\n", "accounts: []\n"), want: "accounts must list at least one account"},
		{name: "stray role field", body: replace("google_email: Partner@Example.com", "google_email: partner@example.com\n    role: admin"), want: "field role not found"},
		{name: "missing login client id", body: replace("client_id: web-client.apps.googleusercontent.com", "client_id: ''"), want: "web.google_login.client_id"},
		{name: "missing login secret env", body: replace("client_secret_env: EGGY_GOOGLE_LOGIN_CLIENT_SECRET", "client_secret_env: ''"), want: "web.google_login.client_secret_env"},
		{name: "invalid login secret env", body: replace("client_secret_env: EGGY_GOOGLE_LOGIN_CLIENT_SECRET", "client_secret_env: lower-case"), want: "web.google_login.client_secret_env"},
		{name: "missing login secret value", body: accountConfig(), env: testSecrets(), want: "EGGY_GOOGLE_LOGIN_CLIENT_SECRET"},
		{name: "legacy owner beside accounts", body: replace("accounts:", "owner:\n  id: 42\naccounts:"), want: "owner.id"},
		{name: "legacy telegram owner beside accounts", body: replace("accounts:", "telegram:\n  owner_id: 42\naccounts:"), want: "telegram.owner_id"},
		{name: "unknown migration owner", body: replace("accounts:", "migration_owner_id: stranger\naccounts:"), want: "migration_owner_id"},
		{name: "legacy password login in account mode", body: accountConfig(), env: func() map[string]string {
			env := accountSecrets()
			env["EGGY_UI_USER_EMAIL"] = "owner@example.com"
			env["EGGY_UI_PASSWORD"] = "hunter2"
			return env
		}(), want: "EGGY_UI_PASSWORD"},
		{name: "login client without accounts", body: strings.Replace(validConfig(), "agent:", "web:\n  google_login:\n    client_id: x\n    client_secret_env: EGGY_GOOGLE_LOGIN_CLIENT_SECRET\nagent:", 1), want: "accounts must list at least one account"},
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
	body := strings.Replace(accountConfig(), "web:", "  - id: third\n    google_email: third@example.com\n  - id: fourth\n    google_email: fourth@example.com\nweb:", 1)
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
	if err := AddAccount(path, AccountInput{ID: "third", GoogleEmail: " Third@Example.com ", TelegramUserID: 99}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if account, ok := cfg.Account("third"); !ok || account.GoogleEmail != "third@example.com" || account.TelegramUserID != 99 {
		t.Fatalf("added account = %#v, %v", account, ok)
	}
	if err := AddAccount(path, AccountInput{ID: "third", GoogleEmail: "other@example.com"}); err == nil {
		t.Fatal("adding a duplicate id must fail")
	}
	if err := AddAccount(path, AccountInput{ID: "fifth", GoogleEmail: "nigel@example.com"}); err == nil {
		t.Fatal("adding a duplicate email must fail")
	}
	if err := EditAccount(path, "third", AccountInput{GoogleEmail: "third2@example.com", TelegramUserID: 0}, false); err != nil {
		t.Fatal(err)
	}
	cfg, _ = LoadDocument(path)
	if account, _ := cfg.Account("third"); account.GoogleEmail != "third2@example.com" || account.TelegramUserID != 0 {
		t.Fatalf("edited account = %#v", account)
	}
	// A bound identity is pinned to its email until the binding is reset;
	// editing the address must never silently re-enroll someone else.
	if err := EditAccount(path, "third", AccountInput{GoogleEmail: "hijack@example.com"}, true); err == nil {
		t.Fatal("changing a bound account's email must fail")
	}
	if err := EditAccount(path, "third", AccountInput{GoogleEmail: "third2@example.com", TelegramUserID: 5}, true); err != nil {
		t.Fatalf("bound account may still change its telegram id: %v", err)
	}
	if err := EditAccount(path, "missing", AccountInput{GoogleEmail: "x@example.com"}, false); err == nil {
		t.Fatal("editing an unknown account must fail")
	}
	if err := RemoveAccount(path, "third"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAccount(path, "third"); err == nil {
		t.Fatal("removing an unknown account must fail")
	}
	if err := RemoveAccount(path, "nigel"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAccount(path, "partner"); err == nil {
		t.Fatal("removing the last account must fail")
	}
}

func TestLoginAndExpectedEmailMutations(t *testing.T) {
	path := writeAccountConfig(t)
	if err := SetGoogleLogin(path, "new-client", "NEW_SECRET_ENV"); err != nil {
		t.Fatal(err)
	}
	if err := SetGoogleLogin(path, "", "NEW_SECRET_ENV"); err == nil {
		t.Fatal("blank client id must fail")
	}
	if err := SetGoogleLogin(path, "new-client", "bad env"); err == nil {
		t.Fatal("invalid env name must fail")
	}
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
	if cfg.Web.GoogleLogin.ClientID != "new-client" || cfg.Web.GoogleLogin.ClientSecretEnv != "NEW_SECRET_ENV" {
		t.Fatalf("login = %#v", cfg.Web.GoogleLogin)
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
	if !identity.AccountMode || len(identity.Config.Accounts) != 2 || identity.Secrets.GoogleLoginClientSecret != "login-secret" {
		t.Fatalf("identity=%+v", identity)
	}
	// Without the login secret nobody can be identified, and safe mode
	// must say so rather than fall back to anything.
	env := accountSecrets()
	delete(env, "EGGY_GOOGLE_LOGIN_CLIENT_SECRET")
	if identity, err := LoadRecoveryIdentity(path, mapEnv(env)); err == nil || !identity.AccountMode {
		t.Fatalf("missing secret: identity=%+v err=%v", identity, err)
	}
	// A legacy document is not account mode, and nothing else is checked.
	legacy := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(legacy, []byte("owner:\n  id: 42\nbroken: yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if identity, err := LoadRecoveryIdentity(legacy, mapEnv(nil)); err != nil || identity.AccountMode {
		t.Fatalf("legacy: identity=%+v err=%v", identity, err)
	}
}

func TestConvertToAccountsReplacesTheLegacyOwnerInOneWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	input := ConvertInput{
		Accounts:             []AccountInput{{ID: "nigel", GoogleEmail: "Nigel@Example.com", TelegramUserID: 42}, {ID: "partner", GoogleEmail: "partner@example.com"}},
		LoginClientID:        "web-client",
		LoginClientSecretEnv: "EGGY_GOOGLE_LOGIN_CLIENT_SECRET",
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
	if account, _ := cfg.Account("nigel"); account.GoogleEmail != "nigel@example.com" || account.TelegramUserID != 42 {
		t.Fatalf("nigel=%+v", account)
	}
	if err := ConvertToAccounts(path, input); err == nil {
		t.Fatal("converting twice must fail")
	}
}

func TestFirstBootGeneratesAnAccountsConfig(t *testing.T) {
	env := map[string]string{
		"EGGY_ACCOUNTS":                   "nigel:Nigel@Example.com:42, partner:partner@example.com",
		"EGGY_GOOGLE_LOGIN_CLIENT_ID":     "web-client",
		"EGGY_GOOGLE_LOGIN_CLIENT_SECRET": "login-secret",
		"EGGY_GOOGLE_EXPECTED_EMAIL":      "eggy@example.com",
		"EGGY_PUBLIC_BASE_URL":            "https://eggy.example",
		"DEEPSEEK_API_KEY":                "k",
		"TELEGRAM_BOT_TOKEN":              "t",
		"TELEGRAM_WEBHOOK_SECRET":         "s",
		"EGGY_ENCRYPTION_KEY":             "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, _, err := LoadOrCreateConfig(path, mapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AccountMode() || len(cfg.Accounts) != 2 || cfg.Owner.ID != "" || cfg.Telegram.OwnerID != 0 {
		t.Fatalf("cfg=%+v", cfg)
	}
	if account, _ := cfg.Account("nigel"); account.GoogleEmail != "nigel@example.com" || account.TelegramUserID != 42 {
		t.Fatalf("nigel=%+v", account)
	}
	if cfg.Web.GoogleLogin.ClientID != "web-client" || cfg.Web.GoogleLogin.ClientSecretEnv != "EGGY_GOOGLE_LOGIN_CLIENT_SECRET" || cfg.Google.ExpectedEmail != "eggy@example.com" {
		t.Fatalf("login=%+v expected=%q", cfg.Web.GoogleLogin, cfg.Google.ExpectedEmail)
	}
	delete(env, "EGGY_GOOGLE_LOGIN_CLIENT_ID")
	if _, _, err := LoadOrCreateConfig(filepath.Join(t.TempDir(), "config.yaml"), mapEnv(env)); err == nil {
		t.Fatal("accounts without a login client id must be refused")
	}
	if _, err := firstBootAccounts("nigel"); err == nil {
		t.Fatal("malformed entry accepted")
	}
}
