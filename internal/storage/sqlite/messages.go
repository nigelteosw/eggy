package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/nigelteosw/eggy/internal/ports"
)

// WriteMessage persists one durable conversation message, scoped to the
// acting account and message.ConversationID (a web thread's own ID, or
// Telegram's fixed thread). Best-effort bumps the owning thread's updated_at
// for sidebar ordering; a no-op when ConversationID doesn't match one of the
// account's threads rows (e.g. Telegram's fixed thread, which is never listed
// there).
func (s *Store) WriteMessage(ctx context.Context, message ports.StoredMessage) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO messages (account_id, conversation_id, role, content, source, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, account, message.ConversationID, message.Role, message.Content, message.Source, message.CreatedAt.UnixNano())
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE threads SET updated_at = ? WHERE account_id = ? AND id = ?`, message.CreatedAt.UnixNano(), account, message.ConversationID); err != nil {
		return err
	}
	return s.tightenPrivateFiles()
}

// RecentMessages returns conversationID's most recent messages, oldest
// first, bounded to limit, excluding anything at or before the
// conversation's last reset (see ResetConversation).
func (s *Store) RecentMessages(ctx context.Context, conversationID string, limit int) ([]ports.StoredMessage, error) {
	if limit <= 0 {
		return nil, errors.New("recent messages limit must be positive")
	}
	account, err := accountOf(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.role, m.content, m.source, m.created_at
		FROM messages m
		LEFT JOIN conversation_resets r ON r.account_id = m.account_id AND r.conversation_id = m.conversation_id
		WHERE m.account_id = ? AND m.conversation_id = ? AND (r.cleared_at IS NULL OR m.created_at > r.cleared_at)
		ORDER BY m.id DESC
		LIMIT ?
	`, account, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []ports.StoredMessage
	for rows.Next() {
		var message ports.StoredMessage
		var createdAt int64
		if err := rows.Scan(&message.ID, &message.Role, &message.Content, &message.Source, &createdAt); err != nil {
			return nil, err
		}
		message.ConversationID = conversationID
		message.CreatedAt = time.Unix(0, createdAt).UTC()
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	return messages, nil
}

// ResetConversation clears conversationID's live turn-context window as of
// at: later RecentMessages calls only see messages recorded after this
// point. Durable history is untouched -- SearchText keeps
// finding everything.
func (s *Store) ResetConversation(ctx context.Context, conversationID string, at time.Time) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO conversation_resets (account_id, conversation_id, cleared_at) VALUES (?, ?, ?)
		ON CONFLICT(account_id, conversation_id) DO UPDATE SET cleared_at = excluded.cleared_at
	`, account, conversationID, at.UnixNano())
	return err
}

// ConversationResetAt reports when conversationID was last cleared. It reads
// the same row ResetConversation writes, so "which stretch of the
// conversation is this" has one answer whether the asker is the context
// window or the traces panel.
func (s *Store) ConversationResetAt(ctx context.Context, conversationID string) (time.Time, bool, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	var clearedAt int64
	err = s.db.QueryRowContext(ctx, `
		SELECT cleared_at FROM conversation_resets WHERE account_id = ? AND conversation_id = ?
	`, account, conversationID).Scan(&clearedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.Unix(0, clearedAt).UTC(), true, nil
}

// SearchText returns the account's keyword matches ordered by FTS5
// relevance, then newest message for equal relevance. The account predicate
// sits on the joined messages row: the FTS index is shared, and what makes
// recall private is that no row of another account's survives the join.
func (s *Store) SearchText(ctx context.Context, query string, limit int) ([]ports.StoredMessage, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("memory text search query is required")
	}
	if limit <= 0 {
		return nil, errors.New("memory text search limit must be positive")
	}
	account, err := accountOf(ctx)
	if err != nil {
		return nil, err
	}
	ftsQuery := literalFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.role, m.content, m.source, m.created_at
		FROM messages_fts
		JOIN messages AS m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH ? AND m.account_id = ?
		ORDER BY bm25(messages_fts), m.created_at DESC
		LIMIT ?
	`, ftsQuery, account, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func literalFTSQuery(query string) string {
	var tokens []string
	var token strings.Builder
	hasBase := false
	flush := func() {
		if token.Len() > 0 && hasBase {
			escaped := strings.ReplaceAll(token.String(), `"`, `""`)
			tokens = append(tokens, `"`+escaped+`"`)
		}
		token.Reset()
		hasBase = false
	}
	for _, value := range query {
		switch {
		case unicode.IsLetter(value), unicode.IsNumber(value):
			token.WriteRune(value)
			hasBase = true
		case unicode.IsMark(value):
			token.WriteRune(value)
		default:
			flush()
		}
	}
	flush()
	return strings.Join(tokens, " AND ")
}
