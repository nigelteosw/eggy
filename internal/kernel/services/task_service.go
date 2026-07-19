package services

import (
	"context"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/tasks"
	"github.com/nigelteosw/eggy/internal/ports"
)

type TaskService struct {
	store ports.StateStore
	now   func() time.Time
}

func NewTaskService(store ports.StateStore, now func() time.Time) *TaskService {
	return &TaskService{store: store, now: now}
}

func (s *TaskService) RecoverInterrupted(ctx context.Context) (int, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		for id, task := range state.Tasks {
			if task.Status != tasks.Running {
				continue
			}
			task.Status = tasks.Interrupted
			task.UpdatedAt = s.now()
			state.Tasks[id] = task
			count++
		}
		return nil
	})
	return count, err
}
