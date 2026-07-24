package services

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestMemoryEmbeddingRunOnceEmbedsBoundedPendingRows(t *testing.T) {
	store := &fakeMemoryStore{pending: []ports.StoredMessage{
		{ID: 1, Content: "one"}, {ID: 2, Content: "two"}, {ID: 3, Content: "three"},
	}}
	embedder := &fakeEmbedder{vectors: map[string][]float32{"one": {1}, "two": {2}, "three": {3}}}
	worker := NewMemoryEmbeddingWorker(store, embedder, 2)

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := store.pendingLimits, []int{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pending limits = %v, want %v", got, want)
	}
	if got, want := embedder.inputs, []string{"one", "two"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("embedded = %v, want %v", got, want)
	}
	if got, want := store.setIDs, []int64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("set IDs = %v, want %v", got, want)
	}
}

func TestMemoryEmbeddingRunOnceStopsOnFirstEmbedderErrorAndResumes(t *testing.T) {
	store := &fakeMemoryStore{pending: []ports.StoredMessage{
		{ID: 1, Content: "one"}, {ID: 2, Content: "two"}, {ID: 3, Content: "three"},
	}}
	embedder := &fakeEmbedder{
		vectors: map[string][]float32{"one": {1}, "two": {2}, "three": {3}},
		errs:    map[string]error{"two": errors.New("provider unavailable")},
	}
	worker := NewMemoryEmbeddingWorker(store, embedder, 3)

	if err := worker.RunOnce(context.Background()); err == nil || err.Error() != "provider unavailable" {
		t.Fatalf("RunOnce error = %v, want provider error", err)
	}
	if got, want := store.setIDs, []int64{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("set IDs after failure = %v, want %v", got, want)
	}
	delete(embedder.errs, "two")
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := store.setIDs, []int64{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("set IDs after resume = %v, want %v", got, want)
	}
	if got, want := embedder.inputs, []string{"one", "two", "two", "three"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("embedded = %v, want %v", got, want)
	}
}

func TestMemoryEmbeddingRunOnceStopsOnFirstStoreError(t *testing.T) {
	store := &fakeMemoryStore{
		pending: []ports.StoredMessage{{ID: 1, Content: "one"}, {ID: 2, Content: "two"}, {ID: 3, Content: "three"}},
		setErrs: map[int64]error{2: errors.New("store unavailable")},
	}
	embedder := &fakeEmbedder{vectors: map[string][]float32{"one": {1}, "two": {2}, "three": {3}}}
	worker := NewMemoryEmbeddingWorker(store, embedder, 3)

	if err := worker.RunOnce(context.Background()); err == nil || err.Error() != "store unavailable" {
		t.Fatalf("RunOnce error = %v, want store error", err)
	}
	if got, want := store.setIDs, []int64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("set IDs = %v, want %v", got, want)
	}
	if got, want := embedder.inputs, []string{"one", "two"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("embedded = %v, want %v", got, want)
	}
}

func TestMemoryEmbeddingWorkerIsNilWithoutEmbedder(t *testing.T) {
	if worker := NewMemoryEmbeddingWorker(&fakeMemoryStore{}, nil, 1); worker != nil {
		t.Fatalf("worker = %#v, want nil", worker)
	}
}

func TestMemoryEmbeddingRunRunsImmediatelyAndReturnsRunOnceError(t *testing.T) {
	store := &fakeMemoryStore{pending: []ports.StoredMessage{{ID: 1, Content: "one"}}}
	embedder := &fakeEmbedder{errs: map[string]error{"one": errors.New("provider unavailable")}}
	worker := NewMemoryEmbeddingWorker(store, embedder, 1)

	if err := worker.Run(context.Background(), time.Hour); err == nil || err.Error() != "provider unavailable" {
		t.Fatalf("Run error = %v, want immediate provider error", err)
	}
}

type fakeMemoryStore struct {
	pending       []ports.StoredMessage
	pendingLimits []int
	setIDs        []int64
	setErrs       map[int64]error
	set           map[int64][]float32
	searchText    []ports.StoredMessage
	searchSimilar []ports.StoredMessage
	textCalls     []memorySearchCall
	similarCalls  []memorySimilarCall
}

type memorySearchCall struct {
	query string
	limit int
}

type memorySimilarCall struct {
	embedding []float32
	limit     int
}

func (s *fakeMemoryStore) WriteMessage(context.Context, ports.StoredMessage) error { return nil }

func (s *fakeMemoryStore) RecentMessages(context.Context, string, int) ([]ports.StoredMessage, error) {
	return nil, nil
}

func (s *fakeMemoryStore) ResetConversation(context.Context, string, time.Time) error { return nil }

func (s *fakeMemoryStore) SearchText(_ context.Context, query string, limit int) ([]ports.StoredMessage, error) {
	s.textCalls = append(s.textCalls, memorySearchCall{query: query, limit: limit})
	return append([]ports.StoredMessage(nil), s.searchText...), nil
}

func (s *fakeMemoryStore) SearchSimilar(_ context.Context, embedding []float32, limit int) ([]ports.StoredMessage, error) {
	s.similarCalls = append(s.similarCalls, memorySimilarCall{embedding: append([]float32(nil), embedding...), limit: limit})
	return append([]ports.StoredMessage(nil), s.searchSimilar...), nil
}

func (s *fakeMemoryStore) PendingEmbeddings(_ context.Context, limit int) ([]ports.StoredMessage, error) {
	s.pendingLimits = append(s.pendingLimits, limit)
	result := make([]ports.StoredMessage, 0, limit)
	for _, message := range s.pending {
		if _, done := s.set[message.ID]; done {
			continue
		}
		result = append(result, message)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (s *fakeMemoryStore) SetEmbedding(_ context.Context, id int64, embedding []float32) error {
	s.setIDs = append(s.setIDs, id)
	if err := s.setErrs[id]; err != nil {
		return err
	}
	if s.set == nil {
		s.set = map[int64][]float32{}
	}
	s.set[id] = append([]float32(nil), embedding...)
	return nil
}

type fakeEmbedder struct {
	vectors map[string][]float32
	errs    map[string]error
	inputs  []string
}

func (e *fakeEmbedder) Embed(_ context.Context, input string) ([]float32, error) {
	e.inputs = append(e.inputs, input)
	if err := e.errs[input]; err != nil {
		return nil, err
	}
	return append([]float32(nil), e.vectors[input]...), nil
}
