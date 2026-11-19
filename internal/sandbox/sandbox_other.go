//go:build !linux

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Sandbox is unavailable off Linux. Its methods exist so the package builds
// everywhere; each one refuses, and bootstrap registers no bash tool.
type Sandbox struct{}

// New reports the sandbox as unavailable.
func New() (*Sandbox, error) { return nil, ErrUnsupported }

// Run refuses every command.
func (s *Sandbox) Run(context.Context, Spec) (Result, error) {
	return Result{}, ErrUnsupported
}

// Probe refuses: nothing can be verified here.
func (s *Sandbox) Probe(context.Context, string, int) error { return ErrUnsupported }

// PrepareRunDir refuses.
func (s *Sandbox) PrepareRunDir(string) error { return ErrUnsupported }

// Terminal refuses.
func (s *Sandbox) Terminal(string, []string) (*exec.Cmd, error) { return nil, ErrUnsupported }

// LockDataDir refuses.
func (s *Sandbox) LockDataDir(string) error { return ErrUnsupported }

// ExecMain never confines anything here, so it fails closed: the
// command is refused rather than run unrestricted.
func ExecMain(args []string) int {
	_, _ = fmt.Fprintln(os.Stderr, "sandbox-exec: "+ErrUnsupported.Error())
	return sandboxExecExitFailed
}
