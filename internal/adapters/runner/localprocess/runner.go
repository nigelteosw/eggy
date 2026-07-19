package localprocess

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

var (
	ErrInvalidRunID = errors.New("invalid run id")
	ErrOutsideRoot  = errors.New("path is outside runner root")
	ErrEmptyCommand = errors.New("command argv is empty")
	ErrTimedOut     = errors.New("command timed out")
)

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)

type Runner struct {
	root           string
	allowed        map[string]bool
	defaultTimeout time.Duration
	maxOutput      int64
}

func New(root string, allowedEnvironment []string, defaultTimeout time.Duration, maxOutput int64) (*Runner, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if defaultTimeout <= 0 || maxOutput <= 0 {
		return nil, errors.New("runner timeout and output limit must be positive")
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(allowedEnvironment))
	for _, key := range allowedEnvironment {
		if key != "" {
			allowed[key] = true
		}
	}
	return &Runner{root: absolute, allowed: allowed, defaultTimeout: defaultTimeout, maxOutput: maxOutput}, nil
}

func (r *Runner) Create(ctx context.Context, runID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !runIDPattern.MatchString(runID) {
		return "", ErrInvalidRunID
	}
	path := filepath.Join(r.root, runID)
	if err := os.Mkdir(path, 0o700); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}
	return path, nil
}

func (r *Runner) Destroy(ctx context.Context, workspace string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.withinRoot(workspace); err != nil {
		return err
	}
	if filepath.Clean(workspace) == r.root {
		return ErrOutsideRoot
	}
	return os.RemoveAll(workspace)
}

func (r *Runner) Execute(ctx context.Context, command ports.Command) (ports.CommandResult, error) {
	return r.execute(ctx, command, nil)
}

func (r *Runner) ExecuteStreaming(ctx context.Context, command ports.Command, onLine func(string)) (ports.CommandResult, error) {
	return r.execute(ctx, command, onLine)
}

func (r *Runner) execute(ctx context.Context, command ports.Command, onLine func(string)) (ports.CommandResult, error) {
	if len(command.Argv) == 0 {
		return ports.CommandResult{}, ErrEmptyCommand
	}
	if err := ctx.Err(); err != nil {
		return ports.CommandResult{}, err
	}
	if err := r.withinRoot(command.Dir); err != nil {
		return ports.CommandResult{}, err
	}
	timeout := command.Timeout
	if timeout <= 0 {
		timeout = r.defaultTimeout
	}
	limit := command.MaxOutput
	if limit <= 0 || limit > r.maxOutput {
		limit = r.maxOutput
	}
	runContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command(command.Argv[0], command.Argv[1:]...)
	cmd.Dir = command.Dir
	cmd.Env = r.environment(command.Env)
	configureProcessGroup(cmd)
	stdout, stderr := &cappedBuffer{limit: limit}, &cappedBuffer{limit: limit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	var emitter *lineEmitter
	if onLine != nil {
		emitter = &lineEmitter{callback: onLine}
		cmd.Stdout = io.MultiWriter(stdout, emitter)
	}
	if err := cmd.Start(); err != nil {
		return ports.CommandResult{}, fmt.Errorf("start command: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
		if emitter != nil {
			emitter.Flush()
		}
	case <-runContext.Done():
		terminateProcessGroup(cmd)
		waitErr = <-done
		if emitter != nil {
			emitter.Flush()
		}
		result := commandResult(cmd, stdout, stderr)
		if errors.Is(ctx.Err(), context.Canceled) {
			return result, context.Canceled
		}
		return result, ErrTimedOut
	}
	result := commandResult(cmd, stdout, stderr)
	if waitErr != nil {
		return result, fmt.Errorf("command exited with code %d: %w", result.ExitCode, waitErr)
	}
	return result, nil
}

func (r *Runner) withinRoot(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(r.root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ErrOutsideRoot
	}
	return nil
}

func (r *Runner) environment(values map[string]string) []string {
	keys := make([]string, 0, len(r.allowed))
	for key := range r.allowed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		value, provided := values[key]
		if !provided {
			value = os.Getenv(key)
		}
		if value != "" {
			result = append(result, key+"="+value)
		}
	}
	return result
}

func commandResult(cmd *exec.Cmd, stdout, stderr *cappedBuffer) ports.CommandResult {
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return ports.CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode, OutputTruncated: stdout.truncated || stderr.truncated}
}

type cappedBuffer struct {
	mu        sync.Mutex
	data      bytes.Buffer
	limit     int64
	truncated bool
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	original := len(data)
	remaining := b.limit - int64(b.data.Len())
	if remaining <= 0 {
		b.truncated = true
		return original, nil
	}
	if int64(len(data)) > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.data.Write(data)
	return original, nil
}
func (b *cappedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.data.String() }

type lineEmitter struct {
	mu       sync.Mutex
	pending  bytes.Buffer
	callback func(string)
}

func (e *lineEmitter) Write(data []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	original := len(data)
	_, _ = e.pending.Write(data)
	for {
		value := e.pending.String()
		index := strings.IndexByte(value, '\n')
		if index < 0 {
			break
		}
		e.callback(strings.TrimSuffix(value[:index], "\r"))
		e.pending.Reset()
		_, _ = e.pending.WriteString(value[index+1:])
	}
	return original, nil
}

func (e *lineEmitter) Flush() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending.Len() == 0 {
		return
	}
	e.callback(strings.TrimSuffix(e.pending.String(), "\r"))
	e.pending.Reset()
}
