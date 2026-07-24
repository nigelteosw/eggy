package bootstrap

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

func sortedModelAliases(s *CommandService) []string {
	aliases := append([]string(nil), s.modelAliases...)
	sort.Strings(aliases)
	return aliases
}

func handleModel(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.agentRuntime == nil {
		return CommandResult{State: ResultInfo, Title: "Model selection is not configured."}, nil
	}
	if len(req.Args) == 0 {
		activeAlias, err := s.agentRuntime.SelectedModel(ctx)
		if err != nil {
			return CommandResult{}, err
		}
		effort, err := s.agentRuntime.ReasoningEffort(ctx)
		if err != nil {
			return CommandResult{}, err
		}
		aliases := sortedModelAliases(s)
		rows := make([][]string, 0, len(aliases))
		for _, alias := range aliases {
			efforts := "—"
			if allowed := s.agentRuntime.ReasoningEfforts(alias); len(allowed) > 0 {
				efforts = strings.Join(allowed, ", ")
			}
			isActive := ""
			if alias == activeAlias {
				isActive = "yes"
			}
			rows = append(rows, []string{alias, efforts, isActive})
		}
		effortValue := effort
		if effortValue == "" {
			effortValue = "—"
		}
		return CommandResult{
			Fields:       []ResultField{{Label: "Active model", Value: activeAlias}, {Label: "Reasoning effort", Value: effortValue}},
			TableHeaders: []string{"Alias", "Allowed reasoning efforts", "Active"},
			TableRows:    rows,
			Next:         []string{"/model <alias>", "/model effort <level>", "/model default"},
		}, nil
	}
	alias := req.Args[0]
	if err := s.agentRuntime.SelectModel(ctx, alias); err != nil {
		return CommandResult{State: ResultError, Title: err.Error(), Detail: "Configured models: " + strings.Join(sortedModelAliases(s), ", ")}, nil
	}
	return CommandResult{Title: "Model set to " + alias + "."}, nil
}

func handleModelEffort(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.agentRuntime == nil {
		return CommandResult{State: ResultInfo, Title: "Model selection is not configured."}, nil
	}
	if len(req.Args) != 1 {
		return usageHelp(mustEntry("model effort"), "Expected exactly one level: low, medium, high, or max."), nil
	}
	if err := s.agentRuntime.SelectReasoningEffort(ctx, req.Args[0]); err != nil {
		return errorResult(err), nil
	}
	return CommandResult{Title: "Reasoning effort set to " + req.Args[0] + "."}, nil
}

func handleModelDefault(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.agentRuntime == nil {
		return CommandResult{State: ResultInfo, Title: "Model selection is not configured."}, nil
	}
	if err := s.agentRuntime.SelectModel(ctx, ""); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{Title: "Model reset to " + s.defaultModel + "."}, nil
}

func handleThinking(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.agentRuntime == nil {
		return CommandResult{State: ResultInfo, Title: "Thinking visibility is not configured."}, nil
	}
	if len(req.Args) > 0 {
		return usageHelp(mustEntry("thinking"), fmt.Sprintf("Unknown thinking subcommand %q.", req.Args[0])), nil
	}
	show, err := s.agentRuntime.ShowThinking(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	state := "hidden"
	if show {
		state = "shown"
	}
	return CommandResult{
		Title: "Thinking messages are currently " + state + ".",
		Next:  []string{"/thinking show", "/thinking hide"},
	}, nil
}

func handleThinkingShow(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.agentRuntime == nil {
		return CommandResult{State: ResultInfo, Title: "Thinking visibility is not configured."}, nil
	}
	if err := s.agentRuntime.SetShowThinking(ctx, true); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{Title: "Thinking messages will be shown."}, nil
}

func handleThinkingHide(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.agentRuntime == nil {
		return CommandResult{State: ResultInfo, Title: "Thinking visibility is not configured."}, nil
	}
	if err := s.agentRuntime.SetShowThinking(ctx, false); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{Title: "Thinking messages will be hidden."}, nil
}

func handleUsage(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.agentRuntime == nil {
		return CommandResult{State: ResultInfo, Title: "Usage tracking is not configured."}, nil
	}
	if len(req.Args) > 0 {
		return usageHelp(mustEntry("usage"), fmt.Sprintf("Unknown usage subcommand %q.", req.Args[0])), nil
	}
	usage, err := s.agentRuntime.Usage(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	aliases := sortedModelAliases(s)
	rows := make([][]string, 0, len(aliases))
	for _, alias := range aliases {
		value := usage[alias]
		rows = append(rows, []string{
			alias,
			fmt.Sprintf("%d", value.PromptTokens),
			fmt.Sprintf("%d", value.CompletionTokens),
			fmt.Sprintf("%d", value.CachedPromptTokens),
			fmt.Sprintf("%d", value.ReasoningTokens),
			fmt.Sprintf("%d", value.TotalTokens),
		})
	}
	return CommandResult{
		TableHeaders: []string{"Model", "Prompt", "Completion", "Cached", "Reasoning", "Total"},
		TableRows:    rows,
		Detail:       "Local totals are provider-reported and do not replace the provider billing dashboard.",
		Next:         []string{"/usage reset"},
	}, nil
}

func handleUsageReset(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.agentRuntime == nil {
		return CommandResult{State: ResultInfo, Title: "Usage tracking is not configured."}, nil
	}
	if err := s.agentRuntime.ResetUsage(ctx); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{Title: "Local provider-reported usage counters reset.", Detail: "Provider billing records are unchanged."}, nil
}
