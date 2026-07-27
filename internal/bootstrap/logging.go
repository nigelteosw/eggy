package bootstrap

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/home"
)

// Log file names inside <home>/logs.
const (
	GatewayLogName = "gateway.log"
	ErrorsLogName  = "errors.log"
)

// maxLogBytes caps each log file. On reaching it the file is rolled to
// <name>.1 and started fresh, so a long-running Eggy cannot fill its volume
// with logs and lose the state it is actually there to keep.
const maxLogBytes = 8 << 20

// Values returns every secret Eggy currently holds, for redaction. Empty
// values are skipped: redacting "" would replace every byte of every line.
func (s Secrets) Values() []string {
	values := []string{
		s.TelegramBotToken, s.TelegramWebhookSecret, s.GitHubToken,
		s.GoogleClientID, s.GoogleClientSecret, s.EncryptionKey,
		s.WebSearchAPIKey, s.UIPassword,
	}
	for _, key := range s.ProviderAPIKeys {
		values = append(values, key)
	}
	for _, token := range s.MCPBearerTokens {
		values = append(values, token)
	}
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			kept = append(kept, value)
		}
	}
	return kept
}

// NewLogger builds the process logger: human-readable lines on stderr, the
// same lines in <home>/logs/gateway.log, and errors alone in
// <home>/logs/errors.log so a problem is one file away rather than buried.
//
// Every writer is wrapped in a redactor, so a secret that reaches a log line
// through an error string or a URL never lands on disk in the clear.
func NewLogger(layout home.Layout, secrets Secrets) (*slog.Logger, io.Closer, error) {
	if err := layout.Ensure(); err != nil {
		return nil, nil, err
	}
	values := secrets.Values()
	gateway, err := openLogFile(filepath.Join(layout.Logs(), GatewayLogName))
	if err != nil {
		return nil, nil, err
	}
	errorsFile, err := openLogFile(filepath.Join(layout.Logs(), ErrorsLogName))
	if err != nil {
		gateway.Close()
		return nil, nil, err
	}
	everything := newRedactor(io.MultiWriter(os.Stderr, gateway), values)
	handler := newTeeHandler(
		slog.NewTextHandler(everything, &slog.HandlerOptions{Level: slog.LevelInfo}),
		slog.NewTextHandler(newRedactor(errorsFile, values), &slog.HandlerOptions{Level: slog.LevelError}),
	)
	return slog.New(handler), closers{gateway, errorsFile}, nil
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
	secrets []string
}

func newRedactor(target io.Writer, secrets []string) io.Writer {
	if len(secrets) == 0 {
		return target
	}
	// Longest first, so a secret that contains another is redacted whole
	// rather than leaving a suffix behind.
	ordered := append([]string(nil), secrets...)
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && len(ordered[j]) > len(ordered[j-1]); j-- {
			ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
		}
	}
	return &redactor{target: target, secrets: ordered}
}

func (r *redactor) Write(data []byte) (int, error) {
	line := string(data)
	for _, secret := range r.secrets {
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
