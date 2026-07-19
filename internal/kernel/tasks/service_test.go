package tasks_test

import (
	"context"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/services"
	taskspkg "github.com/nigelteosw/eggy/internal/kernel/tasks"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestRecoverMarksRunningTasksInterrupted(t *testing.T) {
	now := time.Date(2026, 7, 19, 2, 0, 0, 0, time.UTC)
	store := &taskStore{state: ports.State{SchemaVersion: 1, Tasks: map[string]taskspkg.Task{
		"running": {ID: "running", Status: taskspkg.Running},
		"done":    {ID: "done", Status: taskspkg.Succeeded},
	}}}
	service := services.NewTaskService(store, func() time.Time { return now })
	count, err := service.RecoverInterrupted(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || store.state.Tasks["running"].Status != taskspkg.Interrupted || !store.state.Tasks["running"].UpdatedAt.Equal(now) {
		t.Fatalf("unexpected recovery %#v", store.state.Tasks)
	}
	if store.state.Tasks["done"].Status != taskspkg.Succeeded {
		t.Fatal("completed task changed")
	}
}

type taskStore struct{ state ports.State }

func (s *taskStore) Load(context.Context) (ports.State, error) { return s.state, nil }
func (s *taskStore) Update(_ context.Context, expected uint64, fn func(*ports.State) error) (ports.State, error) {
	if err := fn(&s.state); err != nil {
		return ports.State{}, err
	}
	s.state.Version++
	return s.state, nil
}
