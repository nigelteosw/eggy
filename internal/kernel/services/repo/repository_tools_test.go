package repo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestRepositoryListHidesCredentialsAndSortsByName(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{
		"zeta": {Name: "zeta", BaseBranch: "main", CloneURL: "https://token@example.com/zeta.git"},
		"eggy": {Name: "eggy", BaseBranch: "main", ProtectedBranches: []string{"main"}},
	}
	tools := NewRepositoryTools(store)
	listed, err := tools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || strings.Index(string(listed), `"eggy"`) > strings.Index(string(listed), `"zeta"`) || strings.Contains(string(listed), "CloneURL") {
		t.Fatalf("listed=%s err=%v", listed, err)
	}
}

func TestRepositoryListReportsNotConfigured(t *testing.T) {
	tools := NewRepositoryTools(newMemoryStore())
	result, err := tools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || !strings.Contains(string(result), `"status":"not_configured"`) {
		t.Fatalf("result=%s err=%v", result, err)
	}
}
