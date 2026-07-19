package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/nigelteosw/eggy/internal/ports"
)

type AgentRuntime struct {
	store        ports.StateStore
	defaultAlias string
	aliases      map[string]struct{}
}

func NewAgentRuntime(store ports.StateStore, defaultAlias string, aliases []string) *AgentRuntime {
	known := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		known[alias] = struct{}{}
	}
	return &AgentRuntime{store: store, defaultAlias: defaultAlias, aliases: known}
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
	for range 8 {
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
	return errors.New("state update remained conflicted after 8 attempts")
}
