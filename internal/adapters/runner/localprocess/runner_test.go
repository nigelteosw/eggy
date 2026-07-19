package localprocess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestRunnerWorkspaceLifecycleAndTraversalProtection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runs")
	runner, err := New(root, []string{"PATH"}, 5*time.Second, 1024)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := runner.Create(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(workspace) != root {
		t.Fatalf("workspace=%q", workspace)
	}
	if _, err := runner.Create(context.Background(), "../escape"); !errors.Is(err, ErrInvalidRunID) {
		t.Fatalf("traversal error=%v", err)
	}
	if err := runner.Destroy(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace still exists: %v", err)
	}
	if err := runner.Destroy(context.Background(), filepath.Dir(root)); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside destroy error=%v", err)
	}
}

func TestRunnerSanitizesEnvironmentAndCapsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process test")
	}
	runner, _ := New(filepath.Join(t.TempDir(), "runs"), []string{"PATH", "SAFE"}, 5*time.Second, 9)
	workspace, _ := runner.Create(context.Background(), "run-1")
	result, err := runner.Execute(context.Background(), ports.Command{
		Argv: []string{"/bin/sh", "-c", `printf '%s|%s|1234567890' "$SAFE" "$SECRET"`}, Dir: workspace,
		Env: map[string]string{"SAFE": "visible", "SECRET": "hidden"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "visible||" || !result.OutputTruncated {
		t.Fatalf("result=%#v", result)
	}
}

func TestRunnerTimeoutAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process test")
	}
	runner, _ := New(filepath.Join(t.TempDir(), "runs"), []string{"PATH"}, 50*time.Millisecond, 1024)
	workspace, _ := runner.Create(context.Background(), "run-1")
	started := time.Now()
	_, err := runner.Execute(context.Background(), ports.Command{Argv: []string{"/bin/sh", "-c", "sleep 2"}, Dir: workspace})
	if !errors.Is(err, ErrTimedOut) || time.Since(started) > time.Second {
		t.Fatalf("timeout err=%v elapsed=%v", err, time.Since(started))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runner.Execute(ctx, ports.Command{Argv: []string{"/bin/echo", "no"}, Dir: workspace})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}

func TestRunnerRejectsInvalidCommandDirectory(t *testing.T) {
	runner, _ := New(filepath.Join(t.TempDir(), "runs"), []string{"PATH"}, time.Second, 1024)
	_, err := runner.Execute(context.Background(), ports.Command{Argv: []string{"/bin/echo", "bad"}, Dir: t.TempDir()})
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("error=%v", err)
	}
	_, err = runner.Execute(context.Background(), ports.Command{Dir: strings.Repeat("x", 2)})
	if !errors.Is(err, ErrEmptyCommand) {
		t.Fatalf("empty error=%v", err)
	}
}

func TestRunnerStreamsCompleteOutputLines(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process test")
	}
	runner, _ := New(filepath.Join(t.TempDir(), "runs"), []string{"PATH"}, time.Second, 1024)
	workspace, _ := runner.Create(context.Background(), "run-1")
	var lines []string
	result, err := runner.ExecuteStreaming(context.Background(), ports.Command{Argv: []string{"/bin/sh", "-c", "printf 'first\\nsec'; printf 'ond\\n'"}, Dir: workspace}, func(line string) { lines = append(lines, line) })
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "first" || lines[1] != "second" || result.Stdout != "first\nsecond\n" {
		t.Fatalf("lines=%q result=%#v", lines, result)
	}
}
