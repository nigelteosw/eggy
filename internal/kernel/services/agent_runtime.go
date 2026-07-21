package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

type AgentRuntime struct {
	store        ports.StateStore
	defaultAlias string
	aliases      map[string]struct{}
	efforts      map[string][]string
}

// NewAgentRuntime constructs an AgentRuntime. efforts maps a model alias to
// the reasoning-effort levels it supports (e.g. "deepseek-pro": {"low",
// "medium", "high", "max"}); aliases absent from efforts, or mapped to an
// empty slice, don't support the option.
func NewAgentRuntime(store ports.StateStore, defaultAlias string, aliases []string, efforts map[string][]string) *AgentRuntime {
	known := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		known[alias] = struct{}{}
	}
	return &AgentRuntime{store: store, defaultAlias: defaultAlias, aliases: known, efforts: efforts}
}

func (r *AgentRuntime) SelectedModel(ctx context.Context) (string, error) {
	state, err := r.store.Load(ctx)
	if err != nil {
		return "", err
	}
	if state.Agent.SelectedModel == "" {
		return r.defaultAlias, nil
	}
	if _, ok := r.aliases[state.Agent.SelectedModel]; !ok {
		return "", fmt.Errorf("selected model alias %q is not configured", state.Agent.SelectedModel)
	}
	return state.Agent.SelectedModel, nil
}

func (r *AgentRuntime) SelectModel(ctx context.Context, alias string) error {
	if alias != "" {
		if _, ok := r.aliases[alias]; !ok {
			return fmt.Errorf("model alias %q is not configured", alias)
		}
	}
	return r.update(ctx, func(state *ports.State) { state.Agent.SelectedModel = alias })
}

// ReasoningEfforts returns the reasoning-effort levels alias supports, or nil
// if it doesn't support the option.
func (r *AgentRuntime) ReasoningEfforts(alias string) []string {
	return r.efforts[alias]
}

// ReasoningEffort returns the effort level configured for the currently
// selected model. It returns "" whenever nothing has been set, or when a
// previously stored value no longer applies to the currently selected model
// (e.g. after switching to a model that doesn't support that level).
func (r *AgentRuntime) ReasoningEffort(ctx context.Context) (string, error) {
	alias, err := r.SelectedModel(ctx)
	if err != nil {
		return "", err
	}
	state, err := r.store.Load(ctx)
	if err != nil {
		return "", err
	}
	if !containsEffort(r.efforts[alias], state.Agent.ReasoningEffort) {
		return "", nil
	}
	return state.Agent.ReasoningEffort, nil
}

// SelectReasoningEffort sets the reasoning effort for the currently selected
// model, rejecting levels that model doesn't support.
func (r *AgentRuntime) SelectReasoningEffort(ctx context.Context, effort string) error {
	alias, err := r.SelectedModel(ctx)
	if err != nil {
		return err
	}
	allowed := r.efforts[alias]
	if len(allowed) == 0 {
		return fmt.Errorf("model %q does not support a reasoning effort option", alias)
	}
	if !containsEffort(allowed, effort) {
		return fmt.Errorf("model %q supports reasoning effort %s, not %q", alias, strings.Join(allowed, "|"), effort)
	}
	return r.update(ctx, func(state *ports.State) { state.Agent.ReasoningEffort = effort })
}

func containsEffort(allowed []string, effort string) bool {
	for _, candidate := range allowed {
		if candidate == effort {
			return true
		}
	}
	return false
}

func (r *AgentRuntime) RecordUsage(ctx context.Context, alias string, usage ports.ModelUsage) error {
	if _, ok := r.aliases[alias]; !ok {
		return fmt.Errorf("model alias %q is not configured", alias)
	}
	if usage == (ports.ModelUsage{}) {
		return nil
	}
	return r.update(ctx, func(state *ports.State) {
		if state.Agent.Usage == nil {
			state.Agent.Usage = map[string]ports.ModelUsage{}
		}
		state.Agent.Usage[alias] = state.Agent.Usage[alias].Add(usage)
	})
}

func (r *AgentRuntime) Usage(ctx context.Context) (map[string]ports.ModelUsage, error) {
	state, err := r.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]ports.ModelUsage, len(state.Agent.Usage))
	for alias, usage := range state.Agent.Usage {
		result[alias] = usage
	}
	return result, nil
}

func (r *AgentRuntime) ResetUsage(ctx context.Context) error {
	return r.update(ctx, func(state *ports.State) { state.Agent.Usage = nil })
}

func (r *AgentRuntime) update(ctx context.Context, mutate func(*ports.State)) error {
	const maxAttempts = 32
	for range maxAttempts {
		if err := ctx.Err(); err != nil {
			return err
		}
		state, err := r.store.Load(ctx)
		if err != nil {
			return err
		}
		_, err = r.store.Update(ctx, state.Version, func(state *ports.State) error {
			mutate(state)
			return nil
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, ports.ErrStateVersionConflict) {
			return err
		}
	}
	return fmt.Errorf("state update remained conflicted after %d attempts", maxAttempts)
}
