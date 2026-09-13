package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/plugins/filelock"
)

var (
	ErrSetupRequired         = errors.New("setup required")
	ErrSetupAlreadyCompleted = errors.New("setup already completed")
)

// SetupInput contains only ordinary configuration and the names of
// environment variables holding credentials. Secret values never cross the
// setup HTTP boundary or enter config.yaml.
type SetupInput struct {
	AccountID            string `json:"account_id"`
	GoogleEmail          string `json:"google_email"`
	PublicBaseURL        string `json:"public_base_url"`
	LoginClientID        string `json:"login_client_id"`
	LoginClientSecretEnv string `json:"login_client_secret_env"`
	ProviderName         string `json:"provider_name"`
	ProviderBaseURL      string `json:"provider_base_url"`
	ProviderAPIKeyEnv    string `json:"provider_api_key_env"`
	ModelAlias           string `json:"model_alias"`
	ModelID              string `json:"model_id"`
	TelegramEnabled      bool   `json:"telegram_enabled"`
}

// ValidateSetup builds and validates the exact candidate CompleteSetup will
// persist, including the configured capabilities' environment credentials.
func ValidateSetup(homePath string, input SetupInput, getenv func(string) string) (Config, error) {
	homePath = filepath.Clean(strings.TrimSpace(homePath))
	if homePath == "." || homePath == "" {
		return Config{}, errors.New("setup home path is required")
	}
	var telegram TelegramConfig
	if input.TelegramEnabled {
		enabled := true
		telegram.Enabled = &enabled
	}
	cfg := Config{
		Server: ServerConfig{
			Listen:              ":8080",
			PublicBaseURL:       strings.TrimSpace(input.PublicBaseURL),
			TelegramWebhookPath: "/webhooks/telegram",
		},
		DataDir:  homePath,
		Telegram: telegram,
		Accounts: []AccountConfig{{
			ID:          strings.TrimSpace(input.AccountID),
			GoogleEmail: normalizeEmail(input.GoogleEmail),
		}},
		Web: WebConfig{GoogleLogin: GoogleLoginConfig{
			ClientID:        strings.TrimSpace(input.LoginClientID),
			ClientSecretEnv: strings.TrimSpace(input.LoginClientSecretEnv),
		}},
		Agent: AgentConfig{DefaultModel: strings.TrimSpace(input.ModelAlias), Timezone: "Asia/Singapore"},
		Providers: map[string]ProviderConfig{
			strings.TrimSpace(input.ProviderName): {
				Adapter:   "openai_compatible",
				BaseURL:   strings.TrimSpace(input.ProviderBaseURL),
				APIKeyEnv: strings.TrimSpace(input.ProviderAPIKeyEnv),
			},
		},
		ModelAliases: map[string]ModelAliasConfig{
			strings.TrimSpace(input.ModelAlias): {
				Provider: strings.TrimSpace(input.ProviderName),
				Model:    strings.TrimSpace(input.ModelID),
			},
		},
		Repositories: []RepositoryConfig{},
		Runner: RunnerConfig{
			Root:           filepath.Join(homePath, "runs"),
			Timeout:        Duration(45 * time.Minute),
			Retention:      Duration(30 * time.Minute),
			MaxOutputBytes: 1 << 20,
			AllowedEnv:     []string{"PATH", "LANG", "LC_ALL", "TERM"},
		},
	}
	if err := cfg.applyDefaults(); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	secrets := SecretsFromEnv(getenv)
	secrets.ProviderAPIKeys = map[string]string{}
	for name, provider := range cfg.Providers {
		secrets.ProviderAPIKeys[name] = getenv(provider.APIKeyEnv)
	}
	secrets.GoogleLoginClientSecret = getenv(cfg.Web.GoogleLogin.ClientSecretEnv)
	if err := cfg.validateSecrets(secrets); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// CompleteSetup validates before taking the config lock, validates again
// while holding it, then atomically creates config.yaml without touching .env.
func CompleteSetup(homePath, configPath string, input SetupInput, getenv func(string) string) error {
	if _, err := ValidateSetup(homePath, input, getenv); err != nil {
		return err
	}
	return filelock.With(configPath, func() error {
		if _, err := os.Stat(configPath); err == nil {
			return ErrSetupAlreadyCompleted
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat config: %w", err)
		}
		cfg, err := ValidateSetup(homePath, input, getenv)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
			return fmt.Errorf("create config directory: %w", err)
		}
		if err := writeConfigUnlocked(configPath, cfg); err != nil {
			return err
		}
		return nil
	})
}

func hasHeadlessIdentity(getenv func(string) string) bool {
	for _, name := range []string{"EGGY_ACCOUNTS", "EGGY_OWNER_ID", "EGGY_TELEGRAM_OWNER_ID"} {
		if strings.TrimSpace(getenv(name)) != "" {
			return true
		}
	}
	return false
}

func existingHomeArtifact(configPath string) (string, bool) {
	root := filepath.Dir(configPath)
	for _, name := range []string{"eggy.db", "state.json", "auth.json", "cron", "accounts", "memory", "USER.md", "MEMORY.md"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return name, true
		}
	}
	return "", false
}
