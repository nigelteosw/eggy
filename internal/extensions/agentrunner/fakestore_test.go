package agentrunner

import (
	"context"
	"sort"
	"sync"

	"github.com/nigelteosw/eggy/internal/ports"
)

// memStore is an in-memory ports.AgentRunStore with the same contract as the
// SQLite one: account from the context, conditional revisions, owner on read.
type memStore struct {
	mu    sync.Mutex
	runs  map[string]ports.AgentRun
	repos map[string]map[string]bool // account -> repository -> enabled
	steps map[string][]ports.AgentRunEvent
}

func newMemStore() *memStore {
	return &memStore{runs: map[string]ports.AgentRun{}, repos: map[string]map[string]bool{}, steps: map[string][]ports.AgentRunEvent{}}
}

func owner(ctx context.Context) (string, error) {
	return accountOf(ctx)
}

func (m *memStore) CreateAgentRun(ctx context.Context, run ports.AgentRun) error {
	account, err := owner(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run.AccountID = account
	m.runs[run.ID] = run
	return nil
}

func (m *memStore) GetAgentRun(ctx context.Context, id string) (ports.AgentRun, error) {
	account, err := owner(ctx)
	if err != nil {
		return ports.AgentRun{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[id]
	if !ok || run.AccountID != account {
		return ports.AgentRun{}, ports.ErrAgentRunNotFound
	}
	return run, nil
}

func (m *memStore) ListAgentRuns(ctx context.Context, limit int) ([]ports.AgentRun, error) {
	account, err := owner(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ports.AgentRun
	for _, run := range m.runs {
		if run.AccountID == account {
			out = append(out, run)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memStore) UpdateAgentRun(ctx context.Context, run ports.AgentRun) (ports.AgentRun, error) {
	account, err := owner(ctx)
	if err != nil {
		return ports.AgentRun{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.runs[run.ID]
	if !ok || current.AccountID != account {
		return ports.AgentRun{}, ports.ErrAgentRunNotFound
	}
	if current.Revision != run.Revision {
		return ports.AgentRun{}, ports.ErrAgentRunConflict
	}
	run.Revision++
	run.AccountID = account
	m.runs[run.ID] = run
	return run, nil
}

func (m *memStore) SetRepositoryEnabled(ctx context.Context, repository string, enabled bool) error {
	account, err := owner(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.repos[account] == nil {
		m.repos[account] = map[string]bool{}
	}
	m.repos[account][repository] = enabled
	return nil
}

func (m *memStore) EnabledRepositories(ctx context.Context) ([]string, error) {
	account, err := owner(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for repo, on := range m.repos[account] {
		if on {
			out = append(out, repo)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *memStore) NextQueuedAgentRun(ctx context.Context) (ports.AgentRun, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best ports.AgentRun
	found := false
	for _, run := range m.runs {
		if run.State == ports.AgentRunQueued && (!found || run.CreatedAt.Before(best.CreatedAt)) {
			best, found = run, true
		}
	}
	return best, found, nil
}

func (m *memStore) AgentRunsInStates(ctx context.Context, states ...ports.AgentRunState) ([]ports.AgentRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ports.AgentRun
	for _, run := range m.runs {
		for _, s := range states {
			if run.State == s {
				out = append(out, run)
			}
		}
	}
	return out, nil
}

func (m *memStore) AppendAgentRunEvent(ctx context.Context, runID string, event ports.AgentRunEvent) error {
	account, err := owner(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if run, ok := m.runs[runID]; !ok || run.AccountID != account {
		return ports.ErrAgentRunNotFound
	}
	event.Seq = len(m.steps[runID]) + 1
	m.steps[runID] = append(m.steps[runID], event)
	return nil
}

func (m *memStore) ListAgentRunEvents(ctx context.Context, runID string) ([]ports.AgentRunEvent, error) {
	account, err := owner(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if run, ok := m.runs[runID]; !ok || run.AccountID != account {
		return nil, nil
	}
	return append([]ports.AgentRunEvent(nil), m.steps[runID]...), nil
}

func (m *memStore) DeleteAgentRun(ctx context.Context, id string) error {
	account, err := owner(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[id]
	if !ok || run.AccountID != account {
		return ports.ErrAgentRunNotFound
	}
	if !run.State.Finished() {
		return ports.ErrAgentRunActive
	}
	delete(m.runs, id)
	delete(m.steps, id)
	return nil
}
