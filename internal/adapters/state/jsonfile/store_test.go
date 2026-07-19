package jsonfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestStoreCreatesAndAtomicallyUpdatesVersionedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	store := Open(path)
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != CurrentSchemaVersion || state.Version != 0 {
		t.Fatalf("unexpected initial state %#v", state)
	}
	updated, err := store.Update(context.Background(), 0, func(s *ports.State) error {
		s.SelectedRepository = "eggy"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 1 || updated.SelectedRepository != "eggy" {
		t.Fatalf("unexpected updated state %#v", updated)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil || len(onDisk) == 0 {
		t.Fatalf("state not persisted: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".state.json-*"))
	if len(matches) != 0 {
		t.Fatalf("temporary files left behind: %v", matches)
	}
}

func TestStoreRejectsStaleVersionWithoutMutation(t *testing.T) {
	store := Open(filepath.Join(t.TempDir(), "state.json"))
	if _, err := store.Update(context.Background(), 0, func(s *ports.State) error { return nil }); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err := store.Update(context.Background(), 0, func(s *ports.State) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrVersionConflict) || called {
		t.Fatalf("expected pre-mutation conflict, got err=%v called=%v", err, called)
	}
}

func TestStoreSerializesConcurrentUpdates(t *testing.T) {
	store := Open(filepath.Join(t.TempDir(), "state.json"))
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Update(context.Background(), 0, func(s *ports.State) error { return nil })
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestIndependentStoreInstancesUseProcessLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	stores := []*Store{Open(path), Open(path)}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, store := range stores {
		go func(store *Store) {
			<-start
			_, err := store.Update(context.Background(), 0, func(*ports.State) error { return nil })
			results <- err
		}(store)
	}
	close(start)
	var successes, conflicts int
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, ErrVersionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}
