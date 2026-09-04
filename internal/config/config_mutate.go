package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/plugins/filelock"
	"gopkg.in/yaml.v3"
)

// LoadDocument reads the config file as written and applies defaults, without
// the environment and secret overrides LoadConfig layers on top. Callers that
// display or mutate the stored document want this; callers that need the
// effective runtime configuration want LoadConfig.
func LoadDocument(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	var cfg Config
	if err := decodeKnownYAML(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
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
	return writeFileAtomic(path, body)
}

// writeFileAtomic replaces path with body through a same-directory temporary
// file, so a crash mid-write leaves the previous config intact rather than a
// truncated one the next start cannot parse.
func writeFileAtomic(path string, body []byte) error {
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

// mutate is the one config write path: it takes the file lock, loads the
// stored document, hands it to apply, validates the result, and writes it
// atomically. Every Set* helper below is its apply body and nothing else.
//
// It exists because "every config write goes through internal/config under one
// file lock with the same validation" was a rule nine functions each spelled
// out by hand. All nine held it; the tenth is where it breaks, and it breaks
// silently -- a setter that forgets Validate writes a config.yaml the owner
// only discovers at the next restart, in safe mode. This makes the invariant
// the mechanism rather than the convention.
//
// apply may return an error to refuse the write, which is how a setter
// rejects input it can check before validation sees it.
func mutate(path string, apply func(cfg *Config) error) error {
	return filelock.With(path, func() error {
		cfg, err := LoadDocument(path)
		if err != nil {
			return err
		}
		if err := apply(&cfg); err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

// ReplaceConfig writes body over the config file, but only once body has been
// proved loadable. Validation runs against a temporary file through LoadConfig
// itself rather than a reimplementation of it, so what is accepted here is
// exactly what the next start accepts -- including the environment overrides
// and required secrets, which a plain decode would not catch.
//
// The owner's bytes are written verbatim: this is the repair path for a config
// the daemon could not parse, and re-marshalling would throw away the comments
// and ordering of the file they are trying to fix.
func ReplaceConfig(path string, body []byte, getenv func(string) string) error {
	return filelock.With(path, func() error {
		candidate, err := os.CreateTemp(filepath.Dir(path), ".config-candidate-*.tmp")
		if err != nil {
			return fmt.Errorf("stage config: %w", err)
		}
		candidatePath := candidate.Name()
		defer os.Remove(candidatePath)
		if err := candidate.Chmod(0o600); err != nil {
			candidate.Close()
			return fmt.Errorf("stage config: %w", err)
		}
		if _, err := candidate.Write(body); err != nil {
			candidate.Close()
			return fmt.Errorf("stage config: %w", err)
		}
		if err := candidate.Close(); err != nil {
			return fmt.Errorf("stage config: %w", err)
		}
		if _, _, err := LoadConfig(candidatePath, getenv); err != nil {
			return err
		}
		return writeFileAtomic(path, body)
	})
}

// ProviderInput is the set of provider fields a surface can set. It is a
// struct for the same reason MCPServerInput is: adding discovery made this the
// fifth positional string, and a run of same-typed strings is how a base URL
// ends up in the api_key_env field.
type ProviderInput struct {
	Name      string
	Adapter   string
	BaseURL   string
	APIKeyEnv string
	// DiscoverModels carries the same three states the field itself has:
	// nil leaves the key out entirely and takes the default (on), and a
	// pointer writes the owner's explicit choice down.
	DiscoverModels *bool
}

func SetProvider(path string, input ProviderInput) error {
	return mutate(path, func(cfg *Config) error {
		if cfg.Providers == nil {
			cfg.Providers = map[string]ProviderConfig{}
		}
		cfg.Providers[input.Name] = ProviderConfig{
			Adapter: input.Adapter, BaseURL: input.BaseURL, APIKeyEnv: input.APIKeyEnv,
			DiscoverModels: input.DiscoverModels,
		}
		return nil
	})
}

// SetModelAlias configures alias. reasoningEfforts is a comma-separated list
// of supported levels (e.g. "low,medium,high,max"); pass "" to leave the
// alias without a reasoning-effort option.
func SetModelAlias(path, alias, provider, modelID, reasoningEfforts string) error {
	return mutate(path, func(cfg *Config) error {
		if cfg.ModelAliases == nil {
			cfg.ModelAliases = map[string]ModelAliasConfig{}
		}
		var efforts []string
		if reasoningEfforts != "" {
			efforts = strings.Split(reasoningEfforts, ",")
		}
		cfg.ModelAliases[alias] = ModelAliasConfig{Provider: provider, Model: modelID, ReasoningEfforts: efforts}
		return nil
	})
}

// MCPServerInput is the set of MCP server fields an owner can set from a
// surface -- the web settings panel or the Telegram /mcp command. It is a
// struct rather than a parameter list because transport made it the seventh
// positional string, and a run of same-typed positional strings is how a URL
// ends up in the auth field.
type MCPServerInput struct {
	Name                 string
	URL                  string
	Transport            string
	Auth                 string
	BearerTokenEnv       string
	OAuthClientID        string
	OAuthClientSecretEnv string
	Enabled              bool
}

// SetMCPServer upserts one MCP server definition from the fields a surface can
// set. An empty Transport keeps whatever the server already had and defaults a
// new server to streamable-http. A stdio server is a local subprocess with a
// command line and an environment allowlist, which belongs in reviewed
// configuration rather than a chat message or a web form: writing one is
// refused here in either direction rather than silently rewritten into an HTTP
// server. Advanced fields no surface exposes (oauth_scopes, tool_filter,
// timeouts) are preserved untouched when editing an existing server; a
// brand-new server gets the same sane defaults Config.applyDefaults would give
// it, so it validates immediately instead of only becoming valid after the
// next config load.
func SetMCPServer(path string, input MCPServerInput) error {
	name := input.Name
	return mutate(path, func(cfg *Config) error {
		if cfg.MCP.Servers == nil {
			cfg.MCP.Servers = map[string]MCPServerConfig{}
		}
		server := cfg.MCP.Servers[name]
		if server.Transport == "stdio" || input.Transport == "stdio" {
			return fmt.Errorf("MCP server %q uses the stdio transport; edit it in config.yaml", name)
		}
		server.URL = input.URL
		if input.Transport != "" {
			server.Transport = input.Transport
		}
		if server.Transport == "" {
			server.Transport = "streamable-http"
		}
		server.Auth = input.Auth
		server.BearerTokenEnv = input.BearerTokenEnv
		// An omitted client id keeps whatever the server already had, so
		// editing a URL does not silently drop a hand-registered client. A
		// stored value that no longer validates is the exception: keeping it
		// would make every later edit fail on a field the owner cannot see or
		// clear from any surface, so an unusable one is dropped instead.
		if input.OAuthClientID != "" {
			server.OAuthClientID = input.OAuthClientID
		}
		switch {
		case input.OAuthClientSecretEnv != "":
			server.OAuthClientSecretEnv = input.OAuthClientSecretEnv
		case !environmentNamePattern.MatchString(server.OAuthClientSecretEnv):
			server.OAuthClientSecretEnv = ""
		}
		if server.OAuthClientID == "" {
			// The secret env is meaningless without the client it belongs to,
			// and validation rejects the pair, so clearing one clears both.
			server.OAuthClientSecretEnv = ""
		}
		server.Enabled = input.Enabled
		if server.ConnectTimeout == 0 {
			server.ConnectTimeout = Duration(10 * time.Second)
		}
		if server.Timeout == 0 {
			server.Timeout = Duration(time.Minute)
		}
		if server.MaxOutputBytes == 0 {
			server.MaxOutputBytes = 128 << 10
		}
		cfg.MCP.Servers[name] = server
		return nil
	})
}

// SetMCPServerEnabled flips one server's enabled flag without touching
// anything else about it. It is the one MCP edit a stdio server also accepts:
// turning a subprocess server off needs no knowledge of its command or args,
// so refusing it here would force a file edit for the safest possible change.
func SetMCPServerEnabled(path, name string, enabled bool) error {
	return mutate(path, func(cfg *Config) error {
		server, ok := cfg.MCP.Servers[name]
		if !ok {
			return fmt.Errorf("MCP server %q is not configured", name)
		}
		server.Enabled = enabled
		cfg.MCP.Servers[name] = server
		return nil
	})
}

// RemoveMCPServer deletes one MCP server's config entry. Persisted OAuth
// credentials are retained so re-adding the server does not silently revoke
// its authorization.
func RemoveMCPServer(path, name string) error {
	return mutate(path, func(cfg *Config) error {
		if _, ok := cfg.MCP.Servers[name]; !ok {
			return fmt.Errorf("MCP server %q is not configured", name)
		}
		delete(cfg.MCP.Servers, name)
		return nil
	})
}

// GetMCPServersConfig returns the configured MCP servers keyed by name.
func GetMCPServersConfig(path string) (map[string]MCPServerConfig, error) {
	cfg, err := LoadDocument(path)
	if err != nil {
		return nil, err
	}
	return cfg.MCP.Servers, nil
}

// GoogleInput is the set of Google fields a surface can set. Scopes, timeout,
// and max_output_bytes are deliberately absent: narrowing a consent screen and
// bounding a response are reviewed decisions, and both are preserved untouched
// when a surface edits the rest.
type GoogleInput struct {
	Enabled         bool
	ClientID        string
	ClientSecretEnv string
	Products        []string
	// RequireApproval carries three states through one pointer, because the
	// setting itself has three and collapsing any two would silently change
	// what asks. Nil leaves the stored list alone, like every other field
	// here. A pointer to a nil slice removes the key, so each tool's own
	// classification decides -- including actions a later version adds. A
	// pointer to a list replaces it, and a pointer to an empty list is the
	// owner saying nothing should ask.
	RequireApproval *[]string
}

// SetGoogle writes the Google section from the fields a surface can set.
//
// It is upsert-shaped like SetMCPServer for the same reason: the owner editing
// from a phone or a browser is amending one integration, not authoring the
// section, so anything they cannot see must survive the write. Validation runs
// before the file is touched, so an unknown product name or a client id left
// blank is rejected with the existing config still in place.
func SetGoogle(path string, input GoogleInput) error {
	return mutate(path, func(cfg *Config) error {
		google := cfg.Google
		google.Enabled = input.Enabled
		if input.ClientID != "" {
			google.ClientID = input.ClientID
		}
		if input.ClientSecretEnv != "" {
			google.ClientSecretEnv = input.ClientSecretEnv
		}
		if len(input.Products) > 0 {
			// Sorted on the way in so the written file has a stable order a
			// surface can round-trip; the spelling rule itself is shared.
			products := normalizeProducts(input.Products)
			slices.Sort(products)
			google.Products = products
		}
		if input.RequireApproval != nil {
			// A pointer to a nil slice is the request to remove the key, so it
			// is stored as no pointer at all rather than as an empty list.
			if *input.RequireApproval == nil {
				google.RequireApproval = nil
			} else {
				google.RequireApproval = input.RequireApproval
			}
		}
		cfg.Google = google
		return nil
	})
}

// SetAppearance writes the owner's web panel theme.
//
// The theme is validated against the two names the stylesheet defines rather
// than stored as free text: an unrecognised value would render as neither
// theme, and the failure would surface as an unstyled page instead of as a
// rejected form.
func SetAppearance(path, theme string) error {
	trimmed := strings.TrimSpace(theme)
	if trimmed != ThemeDark && trimmed != ThemeLight {
		return fmt.Errorf("appearance.theme must be %q or %q", ThemeDark, ThemeLight)
	}
	return mutate(path, func(cfg *Config) error {
		cfg.Appearance.Theme = trimmed
		return nil
	})
}

// SetHeartbeat writes the heartbeat section from the fields a surface can set.
//
// include_recent_history is deliberately not among them. It relaxes the
// standing rule that an unprompted turn carries no ambient history, and a
// safety invariant should cost more than a tap on a phone to relax: it stays
// file-only, for the same reason a stdio MCP server's command line does.
//
// Blank active-hours bounds clear the window, following interval rather than
// instruction: turning quiet hours off is a likely reason to open this form,
// and a blank field that silently preserved the old window would be wrong in
// exactly that case.
//
// An empty Interval means off, and off is spelled by writing a zero interval
// rather than by leaving the section as it was: turning the heartbeat off is
// the single most likely reason an owner opens this form, so a blank field
// that silently preserved the old interval would be the wrong default in
// exactly the case that matters. Instruction keeps the upsert shape the other
// setters use -- blank leaves the configured wording alone, since clearing it
// falls back to the built-in prompt anyway.
func SetHeartbeat(path, interval, instruction, activeStart, activeEnd string) error {
	parsed := Duration(0)
	if trimmed := strings.TrimSpace(interval); trimmed != "" {
		value, err := time.ParseDuration(trimmed)
		if err != nil {
			return fmt.Errorf("heartbeat.interval: %w", err)
		}
		parsed = Duration(value)
	}
	return mutate(path, func(cfg *Config) error {
		cfg.Heartbeat.Interval = parsed
		if trimmed := strings.TrimSpace(instruction); trimmed != "" {
			cfg.Heartbeat.Instruction = trimmed
		}
		cfg.Heartbeat.ActiveHours = ActiveHours{Start: strings.TrimSpace(activeStart), End: strings.TrimSpace(activeEnd)}
		if err := cfg.Heartbeat.ActiveHours.Validate(); err != nil {
			return err
		}
		// A heartbeat with nowhere to deliver is refused here rather than
		// saved and ignored, so the owner learns it at the form instead of
		// from a warning in a log they will not read.
		if parsed > 0 && !cfg.Telegram.Configured() {
			return errors.New("heartbeat needs a configured telegram channel, since that is where unprompted output goes")
		}
		return nil
	})
}

// SetTracing saves the tracing section from the panel's form. Every field
// arrives as the text the owner typed, and a blank one means "leave the
// default in place" rather than zero -- a blank retention box must not be a
// request to keep traces for no time at all.
//
// There is no separate reset entry point: restoring defaults is this function
// called with blanks, which is what the card's button sends. A second path
// that wrote defaults could disagree with this one about what a default is.
func SetTracing(path, enabled, keepTurns, retention, maxBodyBytes string) error {
	parsedKeep := 0
	if trimmed := strings.TrimSpace(keepTurns); trimmed != "" {
		value, err := strconv.Atoi(trimmed)
		if err != nil {
			return fmt.Errorf("tracing.keep_turns: %w", err)
		}
		parsedKeep = value
	}
	parsedRetention := Duration(0)
	if trimmed := strings.TrimSpace(retention); trimmed != "" {
		value, err := time.ParseDuration(trimmed)
		if err != nil {
			return fmt.Errorf("tracing.retention: %w", err)
		}
		parsedRetention = Duration(value)
	}
	parsedBytes := int64(0)
	if trimmed := strings.TrimSpace(maxBodyBytes); trimmed != "" {
		value, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return fmt.Errorf("tracing.max_body_bytes: %w", err)
		}
		parsedBytes = value
	}
	return mutate(path, func(cfg *Config) error {
		// Written out rather than left absent, so the file says what the
		// owner chose. Absent and true mean the same thing to the loader, but
		// only one of them survives the owner reading their own config later.
		on := strings.TrimSpace(enabled) != "false"
		cfg.Tracing.Enabled = &on
		cfg.Tracing.KeepTurns = parsedKeep
		cfg.Tracing.Retention = parsedRetention
		cfg.Tracing.MaxBodyBytes = parsedBytes
		// A blank field is a request for the default, and applyDefaults is
		// where every default already lives, so the zeroes left above are
		// filled in by the one function that knows them. It runs before
		// mutate's Validate rather than after it, which is strictly stronger:
		// a value the owner actually typed is never zero, so defaulting can
		// only fill in blanks, and what gets written is then what was checked.
		return cfg.applyDefaults()
	})
}

func GetProvidersConfigText(path string) (string, error) {
	cfg, err := LoadDocument(path)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	slices.Sort(names)
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

// ProviderNames is the configured provider names, sorted. It exists so a
// surface can offer the owner a choice among them without re-deriving the
// sort or holding a second copy of the config.
func ProviderNames(path string) ([]string, error) {
	cfg, err := LoadDocument(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func GetModelAliasesConfigText(path string) (string, error) {
	cfg, err := LoadDocument(path)
	if err != nil {
		return "", err
	}
	aliases := make([]string, 0, len(cfg.ModelAliases))
	for alias := range cfg.ModelAliases {
		aliases = append(aliases, alias)
	}
	slices.Sort(aliases)
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

// ReadConfigText returns the config file exactly as stored, comments and all.
// Safe to expose to the authenticated owner for the same reason ShowConfigText
// is: config.yaml holds environment-variable names, never secret values.
func ReadConfigText(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("open config: %w", err)
	}
	return string(body), nil
}

// ShowConfigText re-marshals the whole config as YAML. Safe to expose in
// full: config.yaml never holds secret values, only environment-variable
// names (api_key_env, credential_env).
func ShowConfigText(path string) (string, error) {
	cfg, err := LoadDocument(path)
	if err != nil {
		return "", err
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return string(body), nil
}
