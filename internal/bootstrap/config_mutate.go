package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nigelteosw/eggy/internal/adapters/filelock"
	"gopkg.in/yaml.v3"
)

func loadConfigDocument(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	var document configDocument
	if err := decodeKnownYAML(data, &document); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	cfg := normalizeConfig(document)
	if err := cfg.applyDefaults(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// writeConfigUnlocked persists cfg atomically. Callers must hold the path's
// filelock for the whole load-mutate-write sequence, not just this step, or
// concurrent writers can race: both read the old file, both mutate their own
// copy, and the second write silently discards the first writer's change.
func writeConfigUnlocked(path string, cfg Config) error {
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("persist config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("persist config: %w", err)
	}
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return fmt.Errorf("persist config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("persist config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}
	return os.Rename(temporaryPath, path)
}

func SetProvider(path, name, adapter, baseURL, apiKeyEnv string) error {
	return filelock.With(path, func() error {
		cfg, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if cfg.Providers == nil {
			cfg.Providers = map[string]ProviderConfig{}
		}
		cfg.Providers[name] = ProviderConfig{Adapter: adapter, BaseURL: baseURL, APIKeyEnv: apiKeyEnv}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

// SetModelAlias configures alias. reasoningEfforts is a comma-separated list
// of supported levels (e.g. "low,medium,high,max"); pass "" to leave the
// alias without a reasoning-effort option.
func SetModelAlias(path, alias, provider, modelID, reasoningEfforts string) error {
	return filelock.With(path, func() error {
		cfg, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if cfg.ModelAliases == nil {
			cfg.ModelAliases = map[string]ModelAliasConfig{}
		}
		var efforts []string
		if reasoningEfforts != "" {
			efforts = strings.Split(reasoningEfforts, ",")
		}
		cfg.ModelAliases[alias] = ModelAliasConfig{Provider: provider, Model: modelID, ReasoningEfforts: efforts}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

// SetCalendar patches CalendarConfig field-by-field: an empty string for
// enabled, defaultCalendar, or timezone means "leave that field unchanged."
// At least one must be non-empty.
func SetCalendar(path, enabled, defaultCalendar, timezone string) error {
	if enabled == "" && defaultCalendar == "" && timezone == "" {
		return errors.New("at least one of enabled, default_calendar, or timezone is required")
	}
	return filelock.With(path, func() error {
		cfg, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if enabled != "" {
			parsed, err := strconv.ParseBool(enabled)
			if err != nil {
				return fmt.Errorf("enabled must be true or false: %w", err)
			}
			cfg.Calendar.Enabled = parsed
		}
		if defaultCalendar != "" {
			cfg.Calendar.DefaultCalendar = defaultCalendar
		}
		if timezone != "" {
			cfg.Calendar.Timezone = timezone
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

func GetProvidersConfigText(path string) (string, error) {
	cfg, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "No providers configured.", nil
	}
	lines := make([]string, 0, len(names))
	for _, name := range names {
		provider := cfg.Providers[name]
		lines = append(lines, fmt.Sprintf("%s  adapter=%s  base_url=%s  api_key_env=%s", name, provider.Adapter, provider.BaseURL, provider.APIKeyEnv))
	}
	return strings.Join(lines, "\n"), nil
}

func GetModelAliasesConfigText(path string) (string, error) {
	cfg, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	aliases := make([]string, 0, len(cfg.ModelAliases))
	for alias := range cfg.ModelAliases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	if len(aliases) == 0 {
		return "No models configured.", nil
	}
	lines := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		model := cfg.ModelAliases[alias]
		line := fmt.Sprintf("%s  provider=%s  model=%s", alias, model.Provider, model.Model)
		if len(model.ReasoningEfforts) > 0 {
			line += "  reasoning_efforts=" + strings.Join(model.ReasoningEfforts, ",")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func GetCalendarConfigText(path string) (string, error) {
	cfg, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("enabled=%t  default_calendar=%s  timezone=%s", cfg.Calendar.Enabled, cfg.Calendar.DefaultCalendar, cfg.Calendar.Timezone), nil
}

// ShowConfigText re-marshals the whole config as YAML. Safe to expose in
// full: config.yaml never holds secret values, only environment-variable
// names (api_key_env, credential_env).
func ShowConfigText(path string) (string, error) {
	cfg, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return string(body), nil
}
