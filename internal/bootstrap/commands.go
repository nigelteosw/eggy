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
	config       Config
	store        ports.StateStore
	context      ports.ContextStore
	conversation *services.ConversationService
	coding       *services.CodingService
	agentRuntime *services.AgentRuntime
	defaultModel string
	modelAliases []string
	now          func() time.Time
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
		names := make([]string, 0, len(s.config.Repositories))
		for _, repository := range s.config.Repositories {
			names = append(names, repository.Name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return "No repositories configured.", true, nil
		}
		return strings.Join(names, "\n"), true, nil
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
