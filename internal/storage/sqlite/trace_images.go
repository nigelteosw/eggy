package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

var _ ports.ImageStore = (*Store)(nil)

// SaveImage files a generated image under the trace of the turn that made it.
// Like a span, an image has no account column: it inherits ownership through
// its trace, so the insert happens only when that trace is the acting
// account's, and PruneTraces deletes it with the trace.
func (s *Store) SaveImage(ctx context.Context, traceID string, image ports.ContentPart) (string, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return "", err
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate image id: %w", err)
	}
	id := hex.EncodeToString(raw[:])
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO trace_images (id, trace_id, media_type, data, created_at)
		SELECT ?, ?, ?, ?, ?
		WHERE EXISTS (SELECT 1 FROM traces WHERE id = ? AND account_id = ?)
	`, id, traceID, image.MediaType, image.Data, time.Now().UnixNano(), traceID, account)
	if err != nil {
		return "", err
	}
	if inserted, err := result.RowsAffected(); err != nil {
		return "", err
	} else if inserted == 0 {
		return "", ports.ErrTraceNotFound
	}
	return id, s.tightenPrivateFiles()
}

// Image reads one of the acting account's images. Someone else's reads as
// missing, the same answer a pruned one gets.
func (s *Store) Image(ctx context.Context, id string) (ports.ContentPart, bool, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return ports.ContentPart{}, false, err
	}
	image := ports.ContentPart{Type: ports.ModalityImage}
	err = s.db.QueryRowContext(ctx, `
		SELECT i.media_type, i.data FROM trace_images i
		JOIN traces t ON t.id = i.trace_id
		WHERE i.id = ? AND t.account_id = ?
	`, id, account).Scan(&image.MediaType, &image.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ContentPart{}, false, nil
	}
	if err != nil {
		return ports.ContentPart{}, false, err
	}
	return image, true, nil
}
