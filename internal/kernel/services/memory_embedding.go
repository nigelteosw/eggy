package services

import (
	"context"
	"errors"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

const defaultMemoryEmbeddingBatchSize = 100

// MemoryEmbeddingWorker keeps durable conversation embeddings up to date
// without adding provider latency to ordinary conversation turns.
type MemoryEmbeddingWorker struct {
	store     ports.MemoryStore
	embedder  ports.Embedder
	batchSize int
}

// NewMemoryEmbeddingWorker creates a worker only when semantic embeddings are
// configured. A nil result makes the feature's absence explicit to bootstrap.
func NewMemoryEmbeddingWorker(store ports.MemoryStore, embedder ports.Embedder, batchSize int) *MemoryEmbeddingWorker {
	if embedder == nil {
		return nil
	}
	if batchSize <= 0 {
		batchSize = defaultMemoryEmbeddingBatchSize
	}
	return &MemoryEmbeddingWorker{store: store, embedder: embedder, batchSize: batchSize}
}

// RunOnce embeds one bounded batch. Provider calls are at least once: if a
// provider call succeeds but persisting its vector fails, the row remains
// pending and a later run calls the provider again. The worker stops at the
// first error so it never advances past an unpersisted row in the batch.
func (w *MemoryEmbeddingWorker) RunOnce(ctx context.Context) error {
	if w == nil {
		return nil
	}
	messages, err := w.store.PendingEmbeddings(ctx, w.batchSize)
	if err != nil {
		return err
	}
	for _, message := range messages {
		embedding, err := w.embedder.Embed(ctx, message.Content)
		if err != nil {
			return err
		}
		if err := w.store.SetEmbedding(ctx, message.ID, embedding); err != nil {
			return err
		}
	}
	return nil
}

// Run performs a batch immediately, then once per interval until cancellation.
// A batch error is returned to let bootstrap log and retry it later.
func (w *MemoryEmbeddingWorker) Run(ctx context.Context, interval time.Duration) error {
	if w == nil {
		return nil
	}
	if interval <= 0 {
		return errors.New("memory embedding interval must be positive")
	}
	if err := w.RunOnce(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil {
				return err
			}
		}
	}
}
