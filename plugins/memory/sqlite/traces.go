package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// Traces live in the same database file as messages and threads rather than
// a second one beside it. They are machine-managed durable state, which
// AGENTS.md already answers with SQLite, and a turn writes to both stores in
// the same moments -- a second file would buy nothing but a second thing to
// open, back up, and get out of sync.
//
// The tables are deliberately separate from messages: a trace is what Eggy
// did, a message is what the owner said, and the recall and search paths must
// never widen to include prompts nobody asked to re-read.

// StartTrace records a turn that has begun. The row is written before the
// first model call so a turn the process dies inside still leaves evidence
// behind; CompleteTrace fills in the rest.
func (s *Store) StartTrace(ctx context.Context, trace ports.Trace) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO traces (id, conversation_id, session, channel, source, kind, model, effort, input, output, error, usage, started_at, duration_ns, complete)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, ?, 0, 0)
		ON CONFLICT(id) DO NOTHING
	`,
		trace.ID, trace.ConversationID, trace.Session, trace.Channel, trace.Source, trace.Kind, trace.Model, trace.Effort,
		trace.Input, encodeUsage(trace.Usage), trace.StartedAt.UnixNano())
	return err
}

// AppendSpan records one step of a turn. A span whose trace row is gone
// (pruned while the turn ran) is dropped rather than orphaned.
func (s *Store) AppendSpan(ctx context.Context, span ports.TraceSpan) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO trace_spans (trace_id, sequence, kind, name, call_id, request, response, error, usage, started_at, duration_ns)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE EXISTS (SELECT 1 FROM traces WHERE id = ?)
	`,
		span.TraceID, span.Sequence, string(span.Kind), span.Name, span.CallID,
		span.Request, span.Response, span.Error, encodeUsage(span.Usage),
		span.StartedAt.UnixNano(), int64(span.Duration), span.TraceID)
	return err
}

// CompleteTrace stamps a turn's outcome onto its row. A trace ID that no
// longer exists is not an error: retention may have removed it while the turn
// it belongs to was still running, and failing the turn over that would be
// backwards.
func (s *Store) CompleteTrace(ctx context.Context, trace ports.Trace) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE traces SET output = ?, error = ?, usage = ?, duration_ns = ?, model = ?, complete = 1
		WHERE id = ?
	`, trace.Output, trace.Error, encodeUsage(trace.Usage), int64(trace.Duration), trace.Model, trace.ID)
	return err
}

// ListTraces returns the most recent traces, newest first, with a span count
// but no span bodies. The bodies are the whole cost of tracing; a list that
// loaded them would make opening the page as expensive as opening every turn
// on it.
func (s *Store) ListTraces(ctx context.Context, limit int) ([]ports.Trace, error) {
	if limit <= 0 {
		return nil, errors.New("trace list limit must be positive")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.conversation_id, t.session, t.channel, t.source, t.kind, t.model, t.effort,
		       t.input, t.output, t.error, t.usage, t.started_at, t.duration_ns, t.complete,
		       (SELECT COUNT(*) FROM trace_spans s WHERE s.trace_id = t.id)
		FROM traces t
		ORDER BY t.started_at DESC, t.rowid DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	traces := make([]ports.Trace, 0, limit)
	for rows.Next() {
		trace, err := scanTrace(rows)
		if err != nil {
			return nil, err
		}
		traces = append(traces, trace)
	}
	return traces, rows.Err()
}

// Trace returns one trace with every span it holds, oldest first.
func (s *Store) Trace(ctx context.Context, id string) (ports.Trace, []ports.TraceSpan, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT t.id, t.conversation_id, t.session, t.channel, t.source, t.kind, t.model, t.effort,
		       t.input, t.output, t.error, t.usage, t.started_at, t.duration_ns, t.complete,
		       (SELECT COUNT(*) FROM trace_spans s WHERE s.trace_id = t.id)
		FROM traces t WHERE t.id = ?
	`, id)
	trace, err := scanTrace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.Trace{}, nil, false, nil
	}
	if err != nil {
		return ports.Trace{}, nil, false, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, sequence, kind, name, call_id, request, response, error, usage, started_at, duration_ns
		FROM trace_spans WHERE trace_id = ? ORDER BY sequence, id
	`, id)
	if err != nil {
		return ports.Trace{}, nil, false, err
	}
	defer rows.Close()

	var spans []ports.TraceSpan
	for rows.Next() {
		var span ports.TraceSpan
		var kind, usage string
		var startedAt, duration int64
		if err := rows.Scan(&span.ID, &span.Sequence, &kind, &span.Name, &span.CallID,
			&span.Request, &span.Response, &span.Error, &usage, &startedAt, &duration); err != nil {
			return ports.Trace{}, nil, false, err
		}
		span.TraceID = id
		span.Kind = ports.TraceSpanKind(kind)
		span.Usage = decodeUsage(usage)
		span.StartedAt = time.Unix(0, startedAt).UTC()
		span.Duration = time.Duration(duration)
		spans = append(spans, span)
	}
	if err := rows.Err(); err != nil {
		return ports.Trace{}, nil, false, err
	}
	return trace, spans, true, nil
}

// PruneTraces enforces the retention budget, oldest first: anything that
// started before before goes, and of what is left only the newest keep traces
// are retained. Spans go with their trace -- SQLite is opened without foreign
// keys here, so the cascade is written out rather than assumed.
func (s *Store) PruneTraces(ctx context.Context, keep int, before time.Time) (int, error) {
	if keep < 0 {
		return 0, errors.New("trace retention count must not be negative")
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM traces
		WHERE (? > 0 AND started_at < ?)
		   OR id NOT IN (SELECT id FROM traces ORDER BY started_at DESC, rowid DESC LIMIT ?)
	`, before.UnixNano(), before.UnixNano(), keep)
	if err != nil {
		return 0, err
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM trace_spans WHERE trace_id NOT IN (SELECT id FROM traces)
	`); err != nil {
		return 0, err
	}
	return int(removed), s.tightenPrivateFiles()
}

// scanner is what QueryRow and Rows have in common, so one trace row is read
// back in one place instead of twice.
type scanner interface {
	Scan(dest ...any) error
}

func scanTrace(row scanner) (ports.Trace, error) {
	var trace ports.Trace
	var usage string
	var startedAt, duration int64
	var complete int
	if err := row.Scan(&trace.ID, &trace.ConversationID, &trace.Session, &trace.Channel, &trace.Source, &trace.Kind,
		&trace.Model, &trace.Effort, &trace.Input, &trace.Output, &trace.Error, &usage,
		&startedAt, &duration, &complete, &trace.Spans); err != nil {
		return ports.Trace{}, err
	}
	trace.Usage = decodeUsage(usage)
	trace.StartedAt = time.Unix(0, startedAt).UTC()
	trace.Duration = time.Duration(duration)
	trace.Complete = complete != 0
	return trace, nil
}

// Usage is stored as JSON rather than as five columns. It is read back whole
// and never queried on, and a provider that starts reporting a sixth counter
// should widen ports.ModelUsage without also being a schema migration here.
func encodeUsage(usage ports.ModelUsage) string {
	encoded, err := json.Marshal(usage)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func decodeUsage(encoded string) ports.ModelUsage {
	var usage ports.ModelUsage
	if err := json.Unmarshal([]byte(encoded), &usage); err != nil {
		return ports.ModelUsage{}
	}
	return usage
}
