// Every rule that decides whether a config is usable, in one place because
// they are one answer: Validate is what stands between a config edit from any
// surface and a deployment that starts in safe mode. config.go holds the shape
// and the defaults; this holds what makes a shape legal.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// Validate rejects a window that cannot work, rather than letting it silently
// suppress every beat.
func (h ActiveHours) Validate() error {
	start, end := strings.TrimSpace(h.Start), strings.TrimSpace(h.End)
	if start == "" && end == "" {
		return nil
	}
	if start == "" || end == "" {
		return errors.New("heartbeat.active_hours needs both start and end")
	}
	startMinutes, err := parseClock(start)
	if err != nil {
		return fmt.Errorf("heartbeat.active_hours.start: %w", err)
	}
	// 24:00 is accepted as an end so a window can run to midnight without
	// being written as the wrapped 00:00, which would mean the opposite.
	endMinutes, err := parseClock(end)
	if err != nil {
		return fmt.Errorf("heartbeat.active_hours.end: %w", err)
	}
	if startMinutes >= 24*60 {
		return errors.New("heartbeat.active_hours.start must be before 24:00")
	}
	if startMinutes == endMinutes {
		return errors.New("heartbeat.active_hours.start and end must differ")
	}
	return nil
}

// ValidateDocument reports whether data is a usable config.yaml, decoding it
// through the same strict path LoadConfig uses so an unknown key is caught
// rather than silently ignored. It stops at defaults plus Validate: an owner
// editing raw YAML may legitimately write settings this build does not read
// yet, and this is the check an editor runs before saving, not a loader.
func ValidateDocument(data []byte) error {
	var candidate Config
	if err := decodeKnownYAML(data, &candidate); err != nil {
		return fmt.Errorf("not valid YAML: %w", err)
	}
	if err := candidate.applyDefaults(); err != nil {
		return err
	}
	return candidate.Validate()
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Owner.ID) == "" {
		return errors.New("owner.id must be set")
	}
	// owner.id is the system-wide identity; Telegram is one optional channel
	// onto it. A negative owner_id is a typo rather than an omission, so it
	// is still rejected -- but omitting the block entirely is a web-only
	// deployment, not an error.
	if c.Telegram.OwnerID < 0 {
		return errors.New("telegram.owner_id must be positive when set")
	}
	if c.Telegram.Configured() && strconv.FormatInt(c.Telegram.OwnerID, 10) != c.Owner.ID {
		return errors.New("owner.id must match telegram.owner_id when Telegram is configured")
	}
	u, err := url.Parse(c.Server.PublicBaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("server.public_base_url must be an HTTP(S) URL")
	}
	if !strings.HasPrefix(c.Server.TelegramWebhookPath, "/") {
		return errors.New("server.telegram_webhook_path must begin with /")
	}
	if c.Server.TrustedProxyHops < 0 {
		return errors.New("server.trusted_proxy_hops must not be negative")
	}
	if err := c.validateProviders(); err != nil {
		return err
	}
	if c.Runner.Timeout.Value() <= 0 {
		return errors.New("runner.timeout must be positive")
	}
	if c.Runner.Retention.Value() <= 0 {
		return errors.New("runner.retention must be positive")
	}
	if c.Heartbeat.Interval < 0 {
		return errors.New("heartbeat.interval must not be negative")
	}
	if err := c.Heartbeat.ActiveHours.Validate(); err != nil {
		return err
	}
	if c.Runner.MaxOutputBytes <= 0 {
		return errors.New("runner.max_output_bytes must be positive")
	}
	if c.Runner.Root != "" && c.DataDir != "" && !pathWithin(c.DataDir, c.Runner.Root) {
		return errors.New("runner.root must be within data_dir for resumable implementation sessions")
	}
	if _, err := time.LoadLocation(c.Agent.Timezone); err != nil {
		return errors.New("agent.timezone must be a valid IANA timezone")
	}
	names := map[string]bool{}
	self := ""
	for _, repo := range c.Repositories {
		if repo.Self {
			if self != "" {
				return fmt.Errorf("repositories %q and %q both set self: exactly one repository is Eggy's own body", self, repo.Name)
			}
			self = repo.Name
		}
		if repo.Name == "" || names[repo.Name] {
			return fmt.Errorf("duplicate repository name %q", repo.Name)
		}
		names[repo.Name] = true
		if repo.CloneURL == "" {
			return fmt.Errorf("repository %q clone_url is required", repo.Name)
		}
		if !branchPattern.MatchString(repo.BaseBranch) {
			return fmt.Errorf("repository %q has invalid base branch", repo.Name)
		}
		for _, branch := range repo.ProtectedBranches {
			if !branchPattern.MatchString(branch) {
				return fmt.Errorf("repository %q has invalid protected branch %q", repo.Name, branch)
			}
		}
	}
	if err := c.validateMCP(); err != nil {
		return err
	}
	if err := c.validateGoogle(); err != nil {
		return err
	}
	if err := c.validateTavily(); err != nil {
		return err
	}
	// An unrecognized mode is refused rather than falling back to a working
	// one: an owner who wrote "readonly" meant to be asked about writes, and
	// quietly running them instead is the one failure this setting exists to
	// prevent.
	if mode := c.Approvals.Mode; mode != "" && !ports.ApprovalMode(mode).Valid() {
		return fmt.Errorf("approvals.mode %q must be strict, normal or auto", mode)
	}
	// Every tracing limit is a ceiling on durable state, so a stated one that
	// removes the ceiling is refused: the failure it would produce is a full
	// disk hours later, nowhere near the edit. Zero is "not stated" and picks
	// up the default in applyDefaults, exactly as every other optional
	// setting here does.
	if c.Tracing.KeepTurns < 0 {
		return errors.New("tracing.keep_turns must be at least 1")
	}
	if c.Tracing.Retention.Value() < 0 {
		return errors.New("tracing.retention must be positive")
	}
	if bytes := c.Tracing.MaxBodyBytes; bytes != 0 && bytes < 1024 {
		return errors.New("tracing.max_body_bytes must be at least 1024")
	}
	return nil
}

// validateTavily refuses a half-configured integration at load rather than at
// the first search, where it would present as a tool that mysteriously always
// fails. Defaults cover every field but the key, so in practice this catches
// the one thing the owner has to supply.
func (c Config) validateTavily() error {
	tavily := c.Tavily
	if !tavily.Enabled {
		return nil
	}
	if tavily.APIKeyEnv != "" && !environmentNamePattern.MatchString(tavily.APIKeyEnv) {
		return fmt.Errorf("tavily.api_key_env %q is not a valid environment variable name", tavily.APIKeyEnv)
	}
	// Validate is reachable without applyDefaults, so an empty depth is the
	// default rather than a rejection.
	if tavily.SearchDepth != "" && !slices.Contains(knownSearchDepths, tavily.SearchDepth) {
		return errors.New("tavily.search_depth must be one of: " + strings.Join(knownSearchDepths, ", "))
	}
	if tavily.ExtractDepth != "" && !slices.Contains(knownExtractDepths, tavily.ExtractDepth) {
		return errors.New("tavily.extract_depth must be one of: " + strings.Join(knownExtractDepths, ", "))
	}
	if tavily.MaxResults < 0 || tavily.MaxResults > 20 {
		return errors.New("tavily.max_results must be between 1 and 20")
	}
	// A budget too small to hold a useful answer is a misconfiguration, not a
	// preference: every result would come back truncated to nothing.
	if tavily.MaxOutputBytes != 0 && tavily.MaxOutputBytes < 4096 {
		return errors.New("tavily.max_output_bytes must be at least 4096")
	}
	if tavily.Timeout < 0 {
		return errors.New("tavily.timeout must not be negative")
	}
	return nil
}

// validateGoogle refuses a half-configured integration rather than letting it
// fail at the first tool call. An enabled Google with no client id authorizes
// nothing; an unknown product name is almost always a typo that would
// otherwise present as a missing tool with no explanation.
func (c Config) validateGoogle() error {
	google := c.Google
	if !google.Enabled {
		return nil
	}
	if strings.TrimSpace(google.ClientID) == "" {
		return errors.New("google.client_id is required when google.enabled is true")
	}
	if google.ClientSecretEnv != "" && !environmentNamePattern.MatchString(google.ClientSecretEnv) {
		return fmt.Errorf("google.client_secret_env %q is not a valid environment variable name", google.ClientSecretEnv)
	}
	if len(google.Products) == 0 {
		return errors.New("google.products must name at least one of: " + strings.Join(knownGoogleProducts, ", "))
	}
	// Normalized rather than compared as written: Validate is reachable without
	// applyDefaults (SetGoogle validates a candidate it built itself), so this
	// must not depend on canonicalization having already run.
	for _, product := range normalizeProducts(google.Products) {
		if !slices.Contains(knownGoogleProducts, product) {
			return fmt.Errorf("unknown google product %q; known products are %s", product, strings.Join(knownGoogleProducts, ", "))
		}
	}
	for _, scope := range google.Scopes {
		if !strings.HasPrefix(scope, "https://") {
			return fmt.Errorf("google scope %q must be a full https scope URL", scope)
		}
	}
	if google.MaxOutputBytes < 0 {
		return errors.New("google.max_output_bytes must not be negative")
	}
	return nil
}

// knownGoogleProducts is duplicated from the adapter's own list rather than
// imported: internal/config must not depend on a plugin package, and the
// adapter's Tools function is the one place that pins the pairing.
var knownGoogleProducts = []string{"calendar", "contacts", "docs", "drive", "gmail", "sheets"}

// normalizeProducts is the one spelling rule for a product name: lowercase,
// trimmed, and empties dropped. Order is the owner's and is preserved.
func normalizeProducts(products []string) []string {
	if len(products) == 0 {
		return products
	}
	normalized := make([]string, 0, len(products))
	for _, product := range products {
		if trimmed := strings.ToLower(strings.TrimSpace(product)); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return normalized
}

func (c Config) validateMCP() error {
	for name, server := range c.MCP.Servers {
		if !configuredNamePattern.MatchString(name) {
			return fmt.Errorf("invalid MCP server name %q", name)
		}
		switch server.Transport {
		case "streamable-http":
			u, err := url.Parse(server.URL)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return fmt.Errorf("MCP server %q URL must use HTTPS", name)
			}
			if u.User != nil {
				return fmt.Errorf("MCP server %q URL must not contain credentials", name)
			}
			if server.Command != "" || len(server.Args) > 0 || len(server.EnvAllowlist) > 0 {
				return fmt.Errorf("MCP server %q command, args, and env_allowlist apply only to the stdio transport", name)
			}
		case "stdio":
			if err := validateStdioMCP(name, server); err != nil {
				return err
			}
		default:
			return fmt.Errorf("MCP server %q has unsupported transport %q", name, server.Transport)
		}
		if server.Auth != "oauth" && server.Auth != "bearer-env" && server.Auth != "none" {
			return fmt.Errorf("MCP server %q has unsupported auth %q", name, server.Auth)
		}
		if server.Auth == "bearer-env" && !environmentNamePattern.MatchString(server.BearerTokenEnv) {
			return fmt.Errorf("MCP server %q bearer_token_env is invalid", name)
		}
		if server.OAuthClientSecretEnv != "" && !environmentNamePattern.MatchString(server.OAuthClientSecretEnv) {
			return fmt.Errorf("MCP server %q oauth_client_secret_env is invalid", name)
		}
		if (server.OAuthClientID != "" || server.OAuthClientSecretEnv != "") && server.Auth != "oauth" {
			return fmt.Errorf("MCP server %q oauth client credentials apply only to auth oauth", name)
		}
		if server.OAuthClientSecretEnv != "" && server.OAuthClientID == "" {
			return fmt.Errorf("MCP server %q sets oauth_client_secret_env without oauth_client_id", name)
		}
		if server.ConnectTimeout.Value() <= 0 || server.Timeout.Value() <= 0 || server.MaxOutputBytes <= 0 {
			return fmt.Errorf("MCP server %q timeouts and max_output_bytes must be positive", name)
		}
		if server.FailureThreshold < 0 || server.Cooldown < 0 {
			return fmt.Errorf("MCP server %q failure_threshold and cooldown must not be negative", name)
		}
		for _, filter := range [][]string{server.ToolFilter.Include, server.ToolFilter.Exclude} {
			seen := map[string]bool{}
			for _, tool := range filter {
				if strings.TrimSpace(tool) == "" {
					return fmt.Errorf("MCP server %q tool filters must not contain empty names", name)
				}
				if seen[tool] {
					return fmt.Errorf("MCP server %q has duplicate tool filter %q", name, tool)
				}
				seen[tool] = true
			}
		}
		approvals := map[string]bool{}
		for _, tool := range server.RequireApproval {
			if strings.TrimSpace(tool) == "" {
				return fmt.Errorf("MCP server %q require_approval must not contain empty names", name)
			}
			if approvals[tool] {
				return fmt.Errorf("MCP server %q has duplicate require_approval entry %q", name, tool)
			}
			approvals[tool] = true
		}
		// An excluded tool never reaches the catalog, so requiring approval for
		// it is a contradiction: the owner wrote down a gate for a call that
		// cannot happen, which most likely means the gate they wanted is on a
		// tool that is still ungated.
		for _, tool := range server.ToolFilter.Exclude {
			if approvals[tool] {
				return fmt.Errorf("MCP server %q requires approval for excluded tool %q", name, tool)
			}
		}
	}
	return nil
}

// validateStdioMCP checks the fields only a stdio server may set. A stdio
// server is a local subprocess, so it has no URL and no HTTP authorization
// mode: its authorization is whatever the allowlisted environment grants it.
func validateStdioMCP(name string, server MCPServerConfig) error {
	if server.URL != "" {
		return fmt.Errorf("MCP server %q url applies only to the streamable-http transport", name)
	}
	if strings.TrimSpace(server.Command) == "" {
		return fmt.Errorf("MCP server %q must set a command for the stdio transport", name)
	}
	if server.Auth != "none" {
		return fmt.Errorf("MCP server %q must use auth none for the stdio transport", name)
	}
	for _, arg := range server.Args {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("MCP server %q args must not contain empty values", name)
		}
	}
	seen := map[string]bool{}
	for _, variable := range server.EnvAllowlist {
		if !environmentNamePattern.MatchString(variable) {
			return fmt.Errorf("MCP server %q env_allowlist entry %q is invalid", name, variable)
		}
		if seen[variable] {
			return fmt.Errorf("MCP server %q has duplicate env_allowlist entry %q", name, variable)
		}
		seen[variable] = true
	}
	return nil
}

func pathWithin(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(absRoot, absPath)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// supportedModelAdapters is the wire format a provider speaks, not the vendor
// it belongs to. "openai_compatible" is OpenAI's Chat Completions shape, which
// OpenAI itself, DeepSeek, OpenRouter, Groq, and most hosted models serve --
// reaching any of them is a providers entry, not a new adapter.
//
// A vendor whose wire format genuinely differs (Anthropic's Messages API,
// with its top-level system prompt, content blocks, and required max_tokens)
// is what earns a new name here plus a case in bootstrap's newModelAdapter.
// Those two lists are deliberately kept in step: this one decides what config
// accepts, that one decides what config gets.
var supportedModelAdapters = []string{"openai_compatible"}

func (c Config) validateProviders() error {
	if !configuredNamePattern.MatchString(c.Agent.DefaultModel) {
		return errors.New("agent.default_model must name a configured model alias")
	}
	for name, provider := range c.Providers {
		if !configuredNamePattern.MatchString(name) {
			return fmt.Errorf("invalid provider name %q", name)
		}
		if !slices.Contains(supportedModelAdapters, provider.Adapter) {
			return fmt.Errorf("unsupported provider adapter %q; supported adapters are %s", provider.Adapter, strings.Join(supportedModelAdapters, ", "))
		}
		u, err := url.Parse(provider.BaseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fmt.Errorf("provider %q base_url must be an HTTP(S) URL", name)
		}
		if !environmentNamePattern.MatchString(provider.APIKeyEnv) {
			return fmt.Errorf("provider %q api_key_env is invalid", name)
		}
	}
	for alias, model := range c.ModelAliases {
		if !configuredNamePattern.MatchString(alias) {
			return fmt.Errorf("invalid model alias %q", alias)
		}
		if strings.TrimSpace(model.Model) == "" {
			return fmt.Errorf("model alias %q model is required", alias)
		}
		if _, ok := c.Providers[model.Provider]; !ok {
			return fmt.Errorf("model alias %q references unknown provider %q", alias, model.Provider)
		}
		for _, effort := range model.ReasoningEfforts {
			if !validReasoningEfforts[effort] {
				return fmt.Errorf("model alias %q has invalid reasoning effort %q", alias, effort)
			}
		}
	}
	if _, ok := c.ModelAliases[c.Agent.DefaultModel]; !ok {
		return fmt.Errorf("agent.default_model %q is not configured", c.Agent.DefaultModel)
	}
	return nil
}

// validateSecrets fails boot on a credential a configured capability needs but
// the environment does not hold. Each capability declares its own requirements
// through require, so a capability that is switched off costs nothing and a new
// one adds a line rather than a branch in a shared list.
func (c Config) validateSecrets(s Secrets) error {
	var missing string
	require := func(name, value string) {
		if missing == "" && strings.TrimSpace(value) == "" {
			missing = name
		}
	}
	// Telegram credentials are required only when Telegram is a channel for
	// this deployment; a web-only one must not have to invent them.
	if c.Telegram.Configured() {
		require("TELEGRAM_BOT_TOKEN", s.TelegramBotToken)
		require("TELEGRAM_WEBHOOK_SECRET", s.TelegramWebhookSecret)
	}
	usedProviders := map[string]bool{}
	for _, model := range c.ModelAliases {
		usedProviders[model.Provider] = true
	}
	for providerName := range usedProviders {
		require(c.Providers[providerName].APIKeyEnv, s.ProviderAPIKeys[providerName])
	}
	if len(c.Repositories) > 0 {
		require("GITHUB_TOKEN", s.GitHubToken)
	}
	for name, server := range c.MCP.Servers {
		if !server.Enabled {
			continue
		}
		if server.Auth == "oauth" {
			require("EGGY_ENCRYPTION_KEY", s.EncryptionKey)
		}
		if server.Auth == "bearer-env" {
			require(server.BearerTokenEnv, s.MCPBearerTokens[name])
		}
		if server.OAuthClientSecretEnv != "" {
			require(server.OAuthClientSecretEnv, s.MCPOAuthClientSecrets[name])
		}
	}
	if c.Google.Enabled {
		// The token lands in the same encrypted auth.json the MCP records use,
		// so the key is required for the same reason.
		require("EGGY_ENCRYPTION_KEY", s.EncryptionKey)
		if c.Google.ClientSecretEnv != "" {
			require(c.Google.ClientSecretEnv, s.GoogleClientSecret)
		}
	}
	if c.Tavily.Enabled && c.Tavily.APIKeyEnv != "" {
		require(c.Tavily.APIKeyEnv, s.TavilyAPIKey)
	}
	if strings.TrimSpace(s.UIUserEmail) != "" || strings.TrimSpace(s.UIPassword) != "" {
		require("EGGY_UI_USER_EMAIL", s.UIUserEmail)
		require("EGGY_UI_PASSWORD", s.UIPassword)
		require("EGGY_ENCRYPTION_KEY", s.EncryptionKey)
	}
	if missing != "" {
		return fmt.Errorf("required environment variable %s is missing", missing)
	}
	return nil
}
