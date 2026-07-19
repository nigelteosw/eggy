package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestRepositoriesRequestAddValidatesStagesAndPersistsOnApproval(t *testing.T) {
	store := newMemoryStore()
	runner := &fakeWorkspaceRunner{workspace: "/tmp/runs/check-1"}
	checker := &fakeRemoteChecker{}
	gateway := &fakeShippingGateway{}
	service := NewRepositoriesService(store, runner, checker, gateway, gateway, fullRepositoryCapabilities, func() string { return "check-1" })

	approval, err := service.RequestAdd(context.Background(), "eggy", "https://github.com/nigelteosw/eggy.git", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !checker.called || checker.repository.Name != "eggy" || checker.repository.BaseBranch != "main" {
		t.Fatalf("checker=%#v", checker)
	}
	if !runner.created || !runner.destroyed {
		t.Fatalf("scratch workspace not created/destroyed: runner=%#v", runner)
	}

	approval.Payload, _ = jsonMarshal(gateway.payload)
	result, err := service.ExecuteApproved(context.Background(), approval)
	if err != nil {
		t.Fatal(err)
	}
	repository, ok := result.(ports.Repository)
	if !ok || repository.Name != "eggy" || repository.BaseBranch != "main" || len(repository.ProtectedBranches) != 1 || repository.ProtectedBranches[0] != "main" {
		t.Fatalf("result=%#v", result)
	}

	state, _ := store.Load(context.Background())
	if state.Repositories["eggy"].CloneURL != "https://github.com/nigelteosw/eggy.git" {
		t.Fatalf("state=%#v", state.Repositories)
	}
	if gateway.authorized != approvals.AddRepository {
		t.Fatalf("gateway=%#v", gateway)
	}
}

func TestRepositoriesRequestAddRejectsDuplicateNameAndUnreachableRemote(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
	runner := &fakeWorkspaceRunner{workspace: "/tmp/runs/check-1"}
	gateway := &fakeShippingGateway{}

	service := NewRepositoriesService(store, runner, &fakeRemoteChecker{}, gateway, gateway, fullRepositoryCapabilities, func() string { return "check-1" })
	if _, err := service.RequestAdd(context.Background(), "eggy", "https://github.com/nigelteosw/eggy.git", "main", nil); err == nil {
		t.Fatal("expected duplicate name rejection")
	}

	unreachable := &fakeRemoteChecker{err: errors.New("not reachable")}
	service = NewRepositoriesService(store, runner, unreachable, gateway, gateway, fullRepositoryCapabilities, func() string { return "check-1" })
	if _, err := service.RequestAdd(context.Background(), "other", "https://github.com/nigelteosw/other.git", "main", nil); err == nil {
		t.Fatal("expected unreachable remote rejection")
	}
}

func TestRepositoriesRequestAddRejectsProviderWithoutShippingReadiness(t *testing.T) {
	store := newMemoryStore()
	service := NewRepositoriesService(store, &fakeWorkspaceRunner{}, &fakeRemoteChecker{}, &fakeShippingGateway{}, &fakeShippingGateway{}, ports.RepositoryCapabilities{Commit: true}, func() string { return "id" })
	if _, err := service.RequestAdd(context.Background(), "eggy", "https://github.com/nigelteosw/eggy.git", "main", nil); err == nil || !strings.Contains(err.Error(), "push and pull-request") {
		t.Fatalf("error=%v", err)
	}
}

func TestRepositoriesExecuteApprovedRequiresAuthorization(t *testing.T) {
	store := newMemoryStore()
	policy := &fakePolicy{err: approvals.ErrExpired}
	service := NewRepositoriesService(store, &fakeWorkspaceRunner{}, &fakeRemoteChecker{}, &fakeShippingGateway{}, policy, fullRepositoryCapabilities, func() string { return "id" })

	approval := approvals.Approval{ID: "approval-1", Action: approvals.AddRepository, Payload: mustMarshal(t, addRepositoryPayload{Name: "eggy", CloneURL: "https://github.com/nigelteosw/eggy.git", BaseBranch: "main", ProtectedBranches: []string{"main"}})}
	if _, err := service.ExecuteApproved(context.Background(), approval); !errors.Is(err, approvals.ErrExpired) {
		t.Fatalf("error=%v", err)
	}
	state, _ := store.Load(context.Background())
	if len(state.Repositories) != 0 {
		t.Fatalf("repository persisted despite failed authorization: %#v", state.Repositories)
	}
}

func TestRepositoriesRemoveAppliesImmediatelyUnlessRunActive(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}, "busy": {Name: "busy"}}
	store.state.CodingRuns = map[string]ports.CodingRun{"run-1": {ID: "run-1", Repository: "busy", Status: "running"}}
	service := NewRepositoriesService(store, &fakeWorkspaceRunner{}, &fakeRemoteChecker{}, &fakeShippingGateway{}, &fakeShippingGateway{}, fullRepositoryCapabilities, func() string { return "id" })

	if err := service.Remove(context.Background(), "eggy"); err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load(context.Background())
	if _, ok := state.Repositories["eggy"]; ok {
		t.Fatal("eggy was not removed")
	}

	if err := service.Remove(context.Background(), "busy"); err == nil {
		t.Fatal("expected removal to be blocked by the active run")
	}
	if err := service.Remove(context.Background(), "missing"); err == nil {
		t.Fatal("expected error removing an unconfigured repository")
	}
}

type fakeRemoteChecker struct {
	called     bool
	repository ports.Repository
	err        error
}

func (c *fakeRemoteChecker) CheckRemote(_ context.Context, repository ports.Repository, _ string) error {
	c.called = true
	c.repository = repository
	return c.err
}

func jsonMarshal(value any) ([]byte, error) { return json.Marshal(value) }

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
