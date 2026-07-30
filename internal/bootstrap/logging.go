package bootstrap

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
)

// maxLogBytes caps each log file. On reaching it the file is rolled to
// <name>.1 and started fresh, so a long-running Eggy cannot fill its volume
// with logs and lose the state it is actually there to keep.
const maxLogBytes = 8 << 20

// NewLogger builds the process logger: human-readable lines on stderr, the
// same lines in <home>/logs/gateway.log, and errors alone in
// <home>/logs/errors.log so a problem is one file away rather than buried.
//
// Every writer is wrapped in a redactor, so a secret that reaches a log line
// through an error string or a URL never lands on disk in the clear.
// The logger is opened before the config is read, because a config that fails
// to load is itself something to log. At that point only the secrets with
// fixed variable names are known, so Logs.Redact takes the rest -- provider
// API keys and MCP bearer tokens, whose variable names config.yaml chooses --
// once the file parses.
func NewLogger(layout home.Layout, secrets config.Secrets) (*slog.Logger, *Logs, error) {
	if err := layout.Ensure(); err != nil {
		return nil, nil, err
	}
	values := secrets.Values()
	gateway, err := openLogFile(filepath.Join(layout.Logs(), home.GatewayLogName))
	if err != nil {
		return nil, nil, err
	}
	errorsFile, err := openLogFile(filepath.Join(layout.Logs(), home.ErrorsLogName))
	if err != nil {
		gateway.Close()
		return nil, nil, err
	}
	everything := newRedactor(io.MultiWriter(os.Stderr, gateway), values)
	errorsOnly := newRedactor(errorsFile, values)
	handler := newTeeHandler(
		slog.NewTextHandler(everything, &slog.HandlerOptions{Level: slog.LevelInfo}),
		slog.NewTextHandler(errorsOnly, &slog.HandlerOptions{Level: slog.LevelError}),
	)
	return slog.New(handler), &Logs{files: closers{gateway, errorsFile}, redactors: []*redactor{everything, errorsOnly}}, nil
}

// Logs owns the open log files and the redaction each one applies.
type Logs struct {
	files     closers
	redactors []*redactor
}

// Redact adds secrets discovered after the logger was opened. Every write from
// here on replaces them, including writes already in flight on other
// goroutines -- a redactor takes its lock per line.
func (l *Logs) Redact(values ...string) {
	if l == nil {
		return
	}
	for _, target := range l.redactors {
		target.add(values)
	}
}

func (l *Logs) Close() error {
	if l == nil {
		return nil
	}
	return l.files.Close()
}

func openLogFile(path string) (*rollingFile, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	return &rollingFile{path: path, file: file, size: info.Size()}, nil
}

// rollingFile appends until maxLogBytes, then renames the file to <name>.1
// and reopens. Exactly one previous generation is kept.
type rollingFile struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

func (r *rollingFile) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size+int64(len(data)) > maxLogBytes {
		if err := r.rollUnlocked(); err != nil {
			return 0, err
		}
	}
	written, err := r.file.Write(data)
	r.size += int64(written)
	return written, err
}

func (r *rollingFile) rollUnlocked() error {
	if err := r.file.Close(); err != nil {
		return err
	}
	if err := os.Rename(r.path, r.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	r.file, r.size = file, 0
	return nil
}

func (r *rollingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file.Close()
}

// redactor replaces known secret values on their way to a log file.
//
// It redacts per Write call, which is exactly one log record for every
// slog handler, so a secret can never be split across two calls and slip
// through.
type redactor struct {
	target  io.Writer
	mu      sync.Mutex
	secrets []string
}

func newRedactor(target io.Writer, secrets []string) *redactor {
	r := &redactor{target: target}
	r.add(secrets)
	return r
}

// add merges more secrets in, keeping the list longest first so a secret that
// contains another is redacted whole rather than leaving a suffix behind.
func (r *redactor) add(secrets []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// A fresh slice rather than an append-and-sort in place: Write holds only
	// the slice header, and sorting the array underneath it could let a secret
	// through on a line being written right now.
	merged := make([]string, 0, len(r.secrets)+len(secrets))
	merged = append(merged, r.secrets...)
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" {
			merged = append(merged, secret)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool { return len(merged[i]) > len(merged[j]) })
	r.secrets = merged
}

func (r *redactor) Write(data []byte) (int, error) {
	r.mu.Lock()
	secrets := r.secrets
	r.mu.Unlock()
	line := string(data)
	for _, secret := range secrets {
		line = strings.ReplaceAll(line, secret, "[redacted]")
	}
	if _, err := io.WriteString(r.target, line); err != nil {
		return 0, err
	}
	// Report the caller's length: a redacted line is shorter than what was
	// handed in, and a short write would read as an error to slog.
	return len(data), nil
}

// teeHandler fans one record out to several handlers, each applying its own
// level filter.
type teeHandler struct{ handlers []slog.Handler }

func newTeeHandler(handlers ...slog.Handler) slog.Handler { return &teeHandler{handlers: handlers} }

func (t *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range t.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (t *teeHandler) Handle(ctx context.Context, record slog.Record) error {
	var firstErr error
	for _, handler := range t.handlers {
		if !handler.Enabled(ctx, record.Level) {
			continue
		}
		if err := handler.Handle(ctx, record.Clone()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (t *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, 0, len(t.handlers))
	for _, handler := range t.handlers {
		next = append(next, handler.WithAttrs(attrs))
	}
	return &teeHandler{handlers: next}
}

func (t *teeHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, 0, len(t.handlers))
	for _, handler := range t.handlers {
		next = append(next, handler.WithGroup(name))
	}
	return &teeHandler{handlers: next}
}

type closers []io.Closer

func (c closers) Close() error {
	var firstErr error
	for _, closer := range c {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
