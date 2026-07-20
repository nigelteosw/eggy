package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/filelock"
	"gopkg.in/yaml.v3"
)

func LoadOrCreateConfig(path string, getenv func(string) string) (Config, Secrets, error) {
	if _, err := os.Stat(path); err == nil {
		return LoadConfig(path, getenv)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, Secrets{}, fmt.Errorf("stat config: %w", err)
	}
	if err := initializeConfig(path, getenv); err != nil {
		return Config{}, Secrets{}, err
	}
	return LoadConfig(path, getenv)
}

func initializeConfig(path string, getenv func(string) string) error {
	return filelock.With(path, func() error {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat config: %w", err)
		}
		cfg, err := firstBootConfig(getenv)
		if err != nil {
			return fmt.Errorf("generate config: %w", err)
		}
		body, err := yaml.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("marshal generated config: %w", err)
		}
		temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
		if err != nil {
			return fmt.Errorf("persist generated config: %w", err)
		}
		temporaryPath := temporary.Name()
		defer os.Remove(temporaryPath)
		if err := temporary.Chmod(0o600); err != nil {
			temporary.Close()
			return fmt.Errorf("persist generated config: %w", err)
		}
		if _, err := temporary.Write(body); err != nil {
			temporary.Close()
			return fmt.Errorf("persist generated config: %w", err)
		}
		if err := temporary.Sync(); err != nil {
			temporary.Close()
			return fmt.Errorf("persist generated config: %w", err)
		}
		if err := temporary.Close(); err != nil {
			return fmt.Errorf("persist generated config: %w", err)
		}
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("persist generated config: %w", err)
		}
		if err := os.Rename(temporaryPath, path); err != nil {
			return fmt.Errorf("persist generated config: %w", err)
		}
		return nil
	})
}

func firstBootConfig(getenv func(string) string) (Config, error) {
	ownerValue := strings.TrimSpace(getenv("EGGY_TELEGRAM_OWNER_ID"))
	if ownerValue == "" {
		return Config{}, errors.New("EGGY_TELEGRAM_OWNER_ID is required")
	}
	ownerID, err := strconv.ParseInt(ownerValue, 10, 64)
	if err != nil || ownerID <= 0 {
		return Config{}, errors.New("EGGY_TELEGRAM_OWNER_ID must be a positive integer")
	}
	publicBaseURL := strings.TrimSpace(getenv("EGGY_PUBLIC_BASE_URL"))
	if publicBaseURL == "" {
		domain := strings.TrimSpace(getenv("RAILWAY_PUBLIC_DOMAIN"))
		if domain == "" {
			return Config{}, errors.New("EGGY_PUBLIC_BASE_URL is required when RAILWAY_PUBLIC_DOMAIN is unavailable")
		}
		publicBaseURL = "https://" + domain
	}
	cfg := Config{
		Version: 2,
		Server: ServerConfig{
			Listen:              ":8080",
			PublicBaseURL:       publicBaseURL,
			TelegramWebhookPath: "/webhooks/telegram",
		},
		DataDir:  "/data",
		Telegram: TelegramConfig{OwnerID: ownerID},
		Agent:    AgentConfig{DefaultModel: "deepseek-pro"},
		Providers: map[string]ProviderConfig{
			"deepseek": {Adapter: "openai_compatible", BaseURL: "https://api.deepseek.com", APIKeyEnv: "DEEPSEEK_API_KEY"},
		},
		ModelAliases: map[string]ModelAliasConfig{
			"deepseek-pro": {Provider: "deepseek", Model: "deepseek-v4-pro"},
		},
		Repositories: []RepositoryConfig{},
		Runner: RunnerConfig{
			Root:           "/data/runs",
			Timeout:        Duration(45 * time.Minute),
			Retention:      Duration(30 * time.Minute),
			MaxOutputBytes: 1 << 20,
			AllowedEnv:     []string{"PATH", "LANG", "LC_ALL", "TERM"},
		},
		ImplementationSessions: ImplementationSessionConfig{
			ContextBudgetChars: 96000,
			RecentMessages:     16,
			OutputExcerptChars: 8192,
		},
		Scheduler: SchedulerConfig{
			HeartbeatCadence: Duration(30 * time.Minute),
			QuietHours: QuietHoursConfig{
				Start:    "22:00",
				End:      "07:00",
				Timezone: "Asia/Singapore",
			},
			MinimumProactiveInterval: Duration(2 * time.Hour),
			WeeklyProactiveLimit:     5,
		},
		Calendar: CalendarConfig{Enabled: false, DefaultCalendar: "primary", Timezone: "Asia/Singapore"},
	}
	if repositoryURL := strings.TrimSpace(getenv("EGGY_REPOSITORY_URL")); repositoryURL != "" {
		name := strings.TrimSpace(getenv("EGGY_REPOSITORY_NAME"))
		if name == "" {
			name = "eggy"
		}
		baseBranch := strings.TrimSpace(getenv("EGGY_REPOSITORY_BASE_BRANCH"))
		if baseBranch == "" {
			baseBranch = "main"
		}
		protectedBranches, err := firstBootProtectedBranches(getenv("EGGY_REPOSITORY_PROTECTED_BRANCHES"), baseBranch)
		if err != nil {
			return Config{}, err
		}
		cfg.Repositories = []RepositoryConfig{{Name: name, CloneURL: repositoryURL, BaseBranch: baseBranch, ProtectedBranches: protectedBranches}}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func firstBootProtectedBranches(raw, baseBranch string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{baseBranch}, nil
	}
	branches := make([]string, 0)
	for _, branch := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(branch); trimmed != "" {
			branches = append(branches, trimmed)
		}
	}
	if len(branches) == 0 {
		return nil, errors.New("EGGY_REPOSITORY_PROTECTED_BRANCHES must contain at least one branch")
	}
	return branches, nil
}
