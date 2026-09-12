package bootstrap

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
)

// accountTestConfig is appTestConfig converted to accounts: two people, one
// of them the historical owner, and the login client accounts require.
func accountTestConfig(dataDir string) config.Config {
	cfg := appTestConfig(dataDir)
	cfg.Owner = config.OwnerConfig{}
	cfg.Telegram = config.TelegramConfig{}
	cfg.Accounts = []config.AccountConfig{
		{ID: "nigel", GoogleEmail: "nigel@example.com", TelegramUserID: 42},
		{ID: "partner", GoogleEmail: "partner@example.com"},
	}
	cfg.Web.GoogleLogin = config.GoogleLoginConfig{ClientID: "web-client", ClientSecretEnv: "EGGY_GOOGLE_LOGIN_CLIENT_SECRET"}
	cfg.MigrationOwnerID = "nigel"
	return cfg
}

func accountTestSecrets() config.Secrets {
	secrets := appTestSecrets("provider-secret")
	secrets.GoogleLoginClientSecret = "login-secret"
	secrets.EncryptionKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	return secrets
}

func TestAccountModeMountsGoogleSignInAndLegacyModeDoesNot(t *testing.T) {
	probe := func(t *testing.T, cfg config.Config, secrets config.Secrets) int {
		t.Helper()
		app, err := NewApp(cfg, secrets, AppOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer app.database.Close()
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/google/start", nil))
		return response.Code
	}
	if code := probe(t, accountTestConfig(t.TempDir()), accountTestSecrets()); code != http.StatusFound {
		t.Fatalf("account mode start status=%d", code)
	}
	if code := probe(t, appTestConfig(t.TempDir()), appTestSecrets("provider-secret")); code != http.StatusNotFound {
		t.Fatalf("legacy mode start status=%d", code)
	}
}
