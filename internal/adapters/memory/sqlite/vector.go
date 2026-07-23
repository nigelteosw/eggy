package sqlite

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// PendingEmbeddings returns the newest messages that have not been embedded.
func (s *Store) PendingEmbeddings(ctx context.Context, limit int) ([]ports.StoredMessage, error) {
	if limit <= 0 {
		return nil, errors.New("pending embeddings limit must be positive")
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, role, content, source, created_at
		FROM messages
		WHERE embedding IS NULL
		   OR embedding_profile IS NULL
		   OR embedding_profile <> ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, s.profile, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectMessages(rows)
}

// SetEmbedding stores an embedding as a little-endian float32 BLOB.
func (s *Store) SetEmbedding(ctx context.Context, id int64, embedding []float32) error {
	encoded, err := encodeVector(embedding)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE messages
		SET embedding = ?, embedding_profile = ?
		WHERE id = ?
	`, encoded, s.profile, id)
	if err != nil {
		return err
	}
	return s.tightenPrivateFiles()
}

// SearchSimilar ranks only the newest configured number of embedded messages
// by cosine similarity, retaining no more than limit results while scoring.
func (s *Store) SearchSimilar(ctx context.Context, embedding []float32, limit int) ([]ports.StoredMessage, error) {
	if err := validateVector(embedding); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, errors.New("similarity search limit must be positive")
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, role, content, source, created_at, embedding
		FROM messages
		WHERE embedding IS NOT NULL
		  AND embedding_profile = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, s.profile, s.candidateLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var top []scoredMessage
	for rows.Next() {
		var message ports.StoredMessage
		var createdAt int64
		var encoded []byte
		if err := rows.Scan(&message.ID, &message.Role, &message.Content, &message.Source, &createdAt, &encoded); err != nil {
			return nil, err
		}
		message.CreatedAt = time.Unix(0, createdAt).UTC()

		candidate, err := decodeVector(encoded)
		if err != nil {
			return nil, err
		}
		score, err := cosineSimilarity(embedding, candidate)
		if err != nil {
			return nil, err
		}
		top = insertTop(top, scoredMessage{message: message, score: score}, limit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	messages := make([]ports.StoredMessage, len(top))
	for index, result := range top {
		messages[index] = result.message
	}
	return messages, nil
}

func collectMessages(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]ports.StoredMessage, error) {
	var messages []ports.StoredMessage
	for rows.Next() {
		var message ports.StoredMessage
		var createdAt int64
		if err := rows.Scan(&message.ID, &message.Role, &message.Content, &message.Source, &createdAt); err != nil {
			return nil, err
		}
		message.CreatedAt = time.Unix(0, createdAt).UTC()
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func encodeVector(vector []float32) ([]byte, error) {
	if err := validateVector(vector); err != nil {
		return nil, err
	}

	encoded := make([]byte, len(vector)*4)
	for index, value := range vector {
		binary.LittleEndian.PutUint32(encoded[index*4:], math.Float32bits(value))
	}
	return encoded, nil
}

func decodeVector(encoded []byte) ([]float32, error) {
	if len(encoded) == 0 || len(encoded)%4 != 0 {
		return nil, errors.New("stored embedding is not a float32 BLOB")
	}

	vector := make([]float32, len(encoded)/4)
	for index := range vector {
		vector[index] = math.Float32frombits(binary.LittleEndian.Uint32(encoded[index*4:]))
	}
	if err := validateVector(vector); err != nil {
		return nil, fmt.Errorf("stored embedding is invalid: %w", err)
	}
	return vector, nil
}

func validateVector(vector []float32) error {
	if len(vector) == 0 {
		return errors.New("embedding vector must not be empty")
	}

	var normSquared float64
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return errors.New("embedding vector must contain only finite values")
		}
		normSquared += float64(value) * float64(value)
	}
	if normSquared == 0 {
		return errors.New("embedding vector norm must not be zero")
	}
	return nil
}

func cosineSimilarity(left, right []float32) (float64, error) {
	if len(left) != len(right) {
		return 0, errors.New("embedding vector dimensions do not match")
	}

	var dot, leftNormSquared, rightNormSquared float64
	for index, value := range left {
		rightValue := right[index]
		dot += float64(value) * float64(rightValue)
		leftNormSquared += float64(value) * float64(value)
		rightNormSquared += float64(rightValue) * float64(rightValue)
	}
	if leftNormSquared == 0 || rightNormSquared == 0 {
		return 0, errors.New("embedding vector norm must not be zero")
	}
	return dot / math.Sqrt(leftNormSquared*rightNormSquared), nil
}

type scoredMessage struct {
	message ports.StoredMessage
	score   float64
}

func insertTop(top []scoredMessage, candidate scoredMessage, limit int) []scoredMessage {
	index := len(top)
	for current, existing := range top {
		if candidate.score > existing.score {
			index = current
			break
		}
	}
	if index == len(top) {
		if len(top) < limit {
			return append(top, candidate)
		}
		return top
	}

	top = append(top, scoredMessage{})
	copy(top[index+1:], top[index:])
	top[index] = candidate
	if len(top) > limit {
		top = top[:limit]
	}
	return top
}
