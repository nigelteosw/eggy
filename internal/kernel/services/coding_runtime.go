package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/nigelteosw/eggy/internal/ports"
)

type CodingAgentRuntime struct {
	store        ports.StateStore
	defaultAlias string
	agents       map[string]ports.CodingAgent
	aliases      []string

	mu         sync.Mutex
	activeRuns map[string]ports.CodingAgent
}

func NewCodingAgentRuntime(store ports.StateStore, defaultAlias string, agents map[string]ports.CodingAgent) (*CodingAgentRuntime, error) {
	if store == nil {
		return nil, errors.New("coding agent state store is required")
	}
	registered := make(map[string]ports.CodingAgent, len(agents))
	aliases := make([]string, 0, len(agents))
	for alias, agent := range agents {
		if alias == "" {
			return nil, errors.New("coding agent alias must not be empty")
		}
		if agent == nil {
			return nil, fmt.Errorf("coding agent alias %q has no agent", alias)
		}
		registered[alias] = agent
		aliases = append(aliases, alias)
	}
	if _, ok := registered[defaultAlias]; !ok {
		return nil, fmt.Errorf("default coding agent alias %q is not configured", defaultAlias)
	}
	sort.Strings(aliases)
	return &CodingAgentRuntime{
		store:        store,
		defaultAlias: defaultAlias,
		agents:       registered,
		aliases:      aliases,
		activeRuns:   make(map[string]ports.CodingAgent),
	}, nil
}

func (r *CodingAgentRuntime) Selected(ctx context.Context) (string, error) {
	state, err := r.store.Load(ctx)
	if err != nil {
		return "", err
	}
	alias := state.Coding.SelectedAgent
	if alias == "" {
		alias = r.defaultAlias
	}
	if _, ok := r.agents[alias]; !ok {
		return "", fmt.Errorf("selected coding agent alias %q is not configured", alias)
	}
	return alias, nil
}

func (r *CodingAgentRuntime) Select(ctx context.Context, alias string) error {
	if alias != "" {
		if _, ok := r.agents[alias]; !ok {
			return fmt.Errorf("coding agent alias %q is not configured; available aliases: %v", alias, r.aliases)
		}
	}
	return r.update(ctx, func(state *ports.State) {
		state.Coding.SelectedAgent = alias
	})
}

func (r *CodingAgentRuntime) Aliases() []string {
	return append([]string(nil), r.aliases...)
}

func (r *CodingAgentRuntime) Run(ctx context.Context, request ports.CodingRequest, progress func(ports.CodingProgress)) (ports.CodingResult, error) {
	alias, err := r.Selected(ctx)
	if err != nil {
		return ports.CodingResult{}, err
	}
	agent := r.agents[alias]
	r.mu.Lock()
	if _, exists := r.activeRuns[request.RunID]; exists {
		r.mu.Unlock()
		return ports.CodingResult{}, fmt.Errorf("coding run %q is already active", request.RunID)
	}
	r.activeRuns[request.RunID] = agent
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.activeRuns, request.RunID)
		r.mu.Unlock()
	}()
	return agent.Run(ctx, request, progress)
}

func (r *CodingAgentRuntime) Interrupt(runID string) error {
	r.mu.Lock()
	agent, ok := r.activeRuns[runID]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("coding run %q is not active", runID)
	}
	return agent.Interrupt(runID)
}

func (r *CodingAgentRuntime) update(ctx context.Context, mutate func(*ports.State)) error {
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
	return fmt.Errorf("coding agent state update remained conflicted after %d attempts", maxAttempts)
}
