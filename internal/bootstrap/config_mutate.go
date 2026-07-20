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

var errConfigSetRequiresVersion2 = errors.New("config.yaml is version 1; migrate to version 2 before using /config set")

func loadConfigDocument(path string) (Config, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, 0, fmt.Errorf("open config: %w", err)
	}
	var header struct {
		Version int `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return Config{}, 0, fmt.Errorf("decode config: %w", err)
	}
	switch header.Version {
	case 1:
		var document legacyConfigDocument
		if err := decodeKnownYAML(data, &document); err != nil {
			return Config{}, 0, fmt.Errorf("decode config: %w", err)
		}
		return normalizeLegacyConfig(document), 1, nil
	case 2:
		var document configV2Document
		if err := decodeKnownYAML(data, &document); err != nil {
			return Config{}, 0, fmt.Errorf("decode config: %w", err)
		}
		return normalizeV2Config(document), 2, nil
	default:
		return Config{}, 0, errors.New("version must be 1 or 2")
	}
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

func SetCodingAgent(path, alias, adapter, credentialEnv string) error {
	return filelock.With(path, func() error {
		cfg, version, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if version != 2 {
			return errConfigSetRequiresVersion2
		}
		if cfg.Coding.Agents == nil {
			cfg.Coding.Agents = map[string]CodingAgentConfig{}
		}
		cfg.Coding.Agents[alias] = CodingAgentConfig{Adapter: adapter, CredentialEnv: credentialEnv}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

func SetProvider(path, name, adapter, baseURL, apiKeyEnv string) error {
	return filelock.With(path, func() error {
		cfg, version, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if version != 2 {
			return errConfigSetRequiresVersion2
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

func SetModelAlias(path, alias, provider, modelID string) error {
	return filelock.With(path, func() error {
		cfg, version, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if version != 2 {
			return errConfigSetRequiresVersion2
		}
		if cfg.ModelAliases == nil {
			cfg.ModelAliases = map[string]ModelAliasConfig{}
		}
		cfg.ModelAliases[alias] = ModelAliasConfig{Provider: provider, Model: modelID}
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
		cfg, version, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if version != 2 {
			return errConfigSetRequiresVersion2
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

func GetCodingConfigText(path string) (string, error) {
	cfg, _, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	aliases := make([]string, 0, len(cfg.Coding.Agents))
	for alias := range cfg.Coding.Agents {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	lines := make([]string, 0, len(aliases)+1)
	lines = append(lines, "default_agent: "+cfg.Coding.DefaultAgent)
	for _, alias := range aliases {
		agent := cfg.Coding.Agents[alias]
		line := alias + "  adapter=" + agent.Adapter
		if agent.CredentialEnv != "" {
			line += "  credential_env=" + agent.CredentialEnv
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func GetProvidersConfigText(path string) (string, error) {
	cfg, _, err := loadConfigDocument(path)
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
	cfg, _, err := loadConfigDocument(path)
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
		lines = append(lines, fmt.Sprintf("%s  provider=%s  model=%s", alias, model.Provider, model.Model))
	}
	return strings.Join(lines, "\n"), nil
}

func GetCalendarConfigText(path string) (string, error) {
	cfg, _, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("enabled=%t  default_calendar=%s  timezone=%s", cfg.Calendar.Enabled, cfg.Calendar.DefaultCalendar, cfg.Calendar.Timezone), nil
}

// ShowConfigText re-marshals the whole config as YAML. Safe to expose in
// full: config.yaml never holds secret values, only environment-variable
// names (api_key_env, credential_env).
func ShowConfigText(path string) (string, error) {
	cfg, _, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return string(body), nil
}
