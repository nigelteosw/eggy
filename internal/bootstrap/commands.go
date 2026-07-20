package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

type CommandService struct {
	config             Config
	store              ports.StateStore
	context            ports.ContextStore
	conversation       *services.ConversationService
	coding             *services.CodingService
	repositories       *services.RepositoriesService
	agentRuntime       *services.AgentRuntime
	codingRuntime      *services.CodingAgentRuntime
	channel            ports.Channel
	owner              string
	defaultModel       string
	defaultCodingAgent string
	configPath         string
	modelAliases       []string
	now                func() time.Time
}

func (s *CommandService) Execute(ctx context.Context, input string) (string, bool, error) {
	fields := strings.Fields(strings.TrimSpace(input))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "", false, nil
	}
	switch fields[0] {
	case "/status":
		result, err := services.NewStatusTool(s.store).Execute(ctx, json.RawMessage(`{}`))
		return string(result), true, err
	case "/repositories":
		if s.repositories == nil {
			return "Repositories are not configured.", true, nil
		}
		if len(fields) == 1 {
			registered, err := s.repositories.List(ctx)
			if err != nil {
				return "", true, err
			}
			names := make([]string, 0, len(registered))
			for name := range registered {
				names = append(names, name)
			}
			sort.Strings(names)
			if len(names) == 0 {
				return "No repositories configured.", true, nil
			}
			return strings.Join(names, "\n"), true, nil
		}
		switch fields[1] {
		case "add":
			if len(fields) < 4 || len(fields) > 6 {
				return "Usage: /repositories add <name> <clone_url> [base_branch] [protected_branches]", true, nil
			}
			name, cloneURL := fields[2], fields[3]
			baseBranch := ""
			if len(fields) >= 5 {
				baseBranch = fields[4]
			}
			var protectedBranches []string
			if len(fields) == 6 {
				for _, branch := range strings.Split(fields[5], ",") {
					if trimmed := strings.TrimSpace(branch); trimmed != "" {
						protectedBranches = append(protectedBranches, trimmed)
					}
				}
			}
			approval, err := s.repositories.RequestAdd(ctx, name, cloneURL, baseBranch, protectedBranches)
			if err != nil {
				return err.Error(), true, nil
			}
			if s.channel != nil {
				if err := s.channel.DeliverApproval(ctx, s.owner, approval); err != nil {
					return "", true, err
				}
			}
			return "Add request for " + name + " staged, awaiting approval.", true, nil
		case "remove":
			if len(fields) != 3 {
				return "Usage: /repositories remove <name>", true, nil
			}
			if err := s.repositories.Remove(ctx, fields[2]); err != nil {
				return err.Error(), true, nil
			}
			return "Removed " + fields[2] + ".", true, nil
		default:
			return "Usage: /repositories [add <name> <clone_url> [base_branch] [protected_branches]|remove <name>]", true, nil
		}
	case "/runs":
		state, err := s.store.Load(ctx)
		if err != nil {
			return "", true, err
		}
		if len(state.CodingRuns) == 0 {
			return "No coding runs.", true, nil
		}
		ids := make([]string, 0, len(state.CodingRuns))
		for id := range state.CodingRuns {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		lines := make([]string, 0, len(ids))
		for _, id := range ids {
			run := state.CodingRuns[id]
			lines = append(lines, fmt.Sprintf("%s  %s  %s", id, run.Status, run.Repository))
		}
		return strings.Join(lines, "\n"), true, nil
	case "/stop":
		if len(fields) != 2 {
			return "Usage: /stop <run-id>", true, nil
		}
		if s.coding == nil {
			return "Coding is not configured.", true, nil
		}
		if err := s.coding.Stop(fields[1]); err != nil {
			return "", true, err
		}
		return "Stop requested for " + fields[1] + ".", true, nil
	case "/schedules":
		state, err := s.store.Load(ctx)
		if err != nil {
			return "", true, err
		}
		if len(state.Schedules) == 0 {
			return "No schedules.", true, nil
		}
		ids := make([]string, 0, len(state.Schedules))
		for id := range state.Schedules {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		lines := make([]string, 0, len(ids))
		for _, id := range ids {
			schedule := state.Schedules[id]
			lines = append(lines, fmt.Sprintf("%s  %s  next %s", id, schedule.Kind, schedule.NextRun.Format("2006-01-02 15:04 MST")))
		}
		return strings.Join(lines, "\n"), true, nil
	case "/memory":
		if s.context == nil {
			return "Memory is not configured.", true, nil
		}
		context, err := s.context.Load(ctx)
		return context.Memory, true, err
	case "/model":
		if s.agentRuntime == nil {
			return "Model selection is not configured.", true, nil
		}
		aliases := append([]string(nil), s.modelAliases...)
		sort.Strings(aliases)
		if len(fields) == 1 {
			active, err := s.agentRuntime.SelectedModel(ctx)
			if err != nil {
				return "", true, err
			}
			return "Active model: " + active + "\nConfigured models:\n" + strings.Join(aliases, "\n"), true, nil
		}
		if len(fields) != 2 {
			return "Usage: /model [alias|default]", true, nil
		}
		if fields[1] == "default" {
			if err := s.agentRuntime.SelectModel(ctx, ""); err != nil {
				return "", true, err
			}
			return "Model reset to " + s.defaultModel + ".", true, nil
		}
		if err := s.agentRuntime.SelectModel(ctx, fields[1]); err != nil {
			return err.Error() + ". Configured models: " + strings.Join(aliases, ", "), true, nil
		}
		return "Model set to " + fields[1] + ".", true, nil
	case "/coding_agent":
		if s.codingRuntime == nil {
			return "Coding agent selection is not configured.", true, nil
		}
		aliases := s.codingRuntime.Aliases()
		if len(fields) == 1 {
			active, err := s.codingRuntime.Selected(ctx)
			if err != nil {
				return "", true, err
			}
			return "Active coding agent: " + active + "\nAvailable coding agents:\n" + strings.Join(aliases, "\n"), true, nil
		}
		if len(fields) != 2 {
			return "Usage: /coding_agent [alias|default]", true, nil
		}
		if fields[1] == "default" {
			if err := s.codingRuntime.Select(ctx, ""); err != nil {
				return "", true, err
			}
			return "Coding agent reset to " + s.defaultCodingAgent + ".", true, nil
		}
		if err := s.codingRuntime.Select(ctx, fields[1]); err != nil {
			return err.Error() + ". Available coding agents: " + strings.Join(aliases, ", "), true, nil
		}
		return "Coding agent set to " + fields[1] + ".", true, nil
	case "/config":
		if s.configPath == "" {
			return "Config file management is not configured.", true, nil
		}
		if len(fields) < 2 {
			return "Usage: /config get <coding|providers|models|calendar|path>|set <coding_agent|provider|model|calendar> ...", true, nil
		}
		switch fields[1] {
		case "get":
			if len(fields) != 3 {
				return "Usage: /config get <coding|providers|models|calendar|path>", true, nil
			}
			switch fields[2] {
			case "coding":
				text, err := GetCodingConfigText(s.configPath)
				if err != nil {
					return err.Error(), true, nil
				}
				return text, true, nil
			case "providers":
				text, err := GetProvidersConfigText(s.configPath)
				if err != nil {
					return err.Error(), true, nil
				}
				return text, true, nil
			case "models":
				text, err := GetModelAliasesConfigText(s.configPath)
				if err != nil {
					return err.Error(), true, nil
				}
				return text, true, nil
			case "calendar":
				text, err := GetCalendarConfigText(s.configPath)
				if err != nil {
					return err.Error(), true, nil
				}
				return text, true, nil
			case "path":
				return s.configPath, true, nil
			default:
				return "Usage: /config get <coding|providers|models|calendar|path>", true, nil
			}
		case "set":
			if len(fields) < 3 {
				return "Usage: /config set <coding_agent|provider|model|calendar> ...", true, nil
			}
			switch fields[2] {
			case "coding_agent":
				values, err := parseConfigFlags(fields[3:])
				if err != nil {
					return err.Error(), true, nil
				}
				usage := "Usage: /config set coding_agent alias=<alias> adapter=<codex_cli|claude_cli> [credential_env=<ENV_NAME>]"
				for key := range values {
					if key != "alias" && key != "adapter" && key != "credential_env" {
						return usage, true, nil
					}
				}
				alias, adapter := values["alias"], values["adapter"]
				if alias == "" || adapter == "" {
					return usage, true, nil
				}
				if err := SetCodingAgent(s.configPath, alias, adapter, values["credential_env"]); err != nil {
					return err.Error(), true, nil
				}
				return "Set coding agent " + alias + ". Restart Eggy for this to take effect.", true, nil
			case "provider":
				values, err := parseConfigFlags(fields[3:])
				if err != nil {
					return err.Error(), true, nil
				}
				usage := "Usage: /config set provider name=<name> adapter=openai_compatible base_url=<url> api_key_env=<ENV_NAME>"
				for key := range values {
					if key != "name" && key != "adapter" && key != "base_url" && key != "api_key_env" {
						return usage, true, nil
					}
				}
				name, adapter, baseURL, apiKeyEnv := values["name"], values["adapter"], values["base_url"], values["api_key_env"]
				if name == "" || adapter == "" || baseURL == "" || apiKeyEnv == "" {
					return usage, true, nil
				}
				if err := SetProvider(s.configPath, name, adapter, baseURL, apiKeyEnv); err != nil {
					return err.Error(), true, nil
				}
				return "Set provider " + name + ". Restart Eggy for this to take effect.", true, nil
			case "model":
				values, err := parseConfigFlags(fields[3:])
				if err != nil {
					return err.Error(), true, nil
				}
				usage := "Usage: /config set model alias=<alias> provider=<provider> model=<model_id>"
				for key := range values {
					if key != "alias" && key != "provider" && key != "model" {
						return usage, true, nil
					}
				}
				alias, provider, modelID := values["alias"], values["provider"], values["model"]
				if alias == "" || provider == "" || modelID == "" {
					return usage, true, nil
				}
				if err := SetModelAlias(s.configPath, alias, provider, modelID); err != nil {
					return err.Error(), true, nil
				}
				return "Set model " + alias + ". Restart Eggy for this to take effect.", true, nil
			case "calendar":
				values, err := parseConfigFlags(fields[3:])
				if err != nil {
					return err.Error(), true, nil
				}
				for key := range values {
					if key != "enabled" && key != "default_calendar" && key != "timezone" {
						return "Usage: /config set calendar [enabled=<true|false>] [default_calendar=<id>] [timezone=<IANA timezone>]", true, nil
					}
				}
				if err := SetCalendar(s.configPath, values["enabled"], values["default_calendar"], values["timezone"]); err != nil {
					return err.Error(), true, nil
				}
				return "Set calendar. Restart Eggy for this to take effect.", true, nil
			default:
				return "Usage: /config set <coding_agent|provider|model|calendar> ...", true, nil
			}
		default:
			return "Usage: /config get <coding|providers|models|calendar|path>|set <coding_agent|provider|model|calendar> ...", true, nil
		}
	case "/usage":
		if s.agentRuntime == nil {
			return "Usage tracking is not configured.", true, nil
		}
		if len(fields) == 2 && fields[1] == "reset" {
			if err := s.agentRuntime.ResetUsage(ctx); err != nil {
				return "", true, err
			}
			return "Local provider-reported usage counters reset. Provider billing records are unchanged.", true, nil
		}
		if len(fields) != 1 {
			return "Usage: /usage [reset]", true, nil
		}
		usage, err := s.agentRuntime.Usage(ctx)
		if err != nil {
			return "", true, err
		}
		aliases := append([]string(nil), s.modelAliases...)
		sort.Strings(aliases)
		lines := make([]string, 0, len(aliases)+1)
		for _, alias := range aliases {
			value := usage[alias]
			lines = append(lines, fmt.Sprintf("%s prompt=%d completion=%d total=%d cached=%d reasoning=%d", alias, value.PromptTokens, value.CompletionTokens, value.TotalTokens, value.CachedPromptTokens, value.ReasoningTokens))
		}
		lines = append(lines, "Local totals are provider-reported and do not replace the provider billing dashboard.")
		return strings.Join(lines, "\n"), true, nil
	case "/new":
		if err := s.conversation.Reset(ctx); err != nil {
			return "", true, err
		}
		return "Started a new conversation. Durable memory is unchanged.", true, nil
	case "/calendar_auth":
		if !s.config.Calendar.Enabled {
			return "Calendar is not configured.", true, nil
		}
		tokenBytes := make([]byte, 24)
		if _, err := rand.Read(tokenBytes); err != nil {
			return "", true, err
		}
		token := base64.RawURLEncoding.EncodeToString(tokenBytes)
		digest := sha256.Sum256([]byte(token))
		state, err := s.store.Load(ctx)
		if err != nil {
			return "", true, err
		}
		now := time.Now
		if s.now != nil {
			now = s.now
		}
		_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
			state.Calendar.EnrollmentDigest = hex.EncodeToString(digest[:])
			state.Calendar.EnrollmentExpires = now().Add(10 * time.Minute)
			return nil
		})
		if err != nil {
			return "", true, err
		}
		return s.config.Server.PublicBaseURL + "/auth/google?enrollment=" + url.QueryEscape(token), true, nil
	default:
		return "", false, nil
	}
}

// parseConfigFlags parses "key=value" tokens into a map, splitting each on
// the first "=" only so a value containing "=" (a base_url query string,
// for instance) still parses correctly.
func parseConfigFlags(tokens []string) (map[string]string, error) {
	values := make(map[string]string, len(tokens))
	for _, token := range tokens {
		key, value, ok := strings.Cut(token, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid flag %q: expected key=value", token)
		}
		values[key] = value
	}
	return values, nil
}
