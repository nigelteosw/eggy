//go:build !linux

package agentrunner

import (
	"context"
	"fmt"
	"os"
)

// Sandbox is unavailable off Linux. Its methods exist so the package builds
// everywhere; each one refuses, and bootstrap registers no bash tool.
type Sandbox struct{}

// NewSandbox reports the sandbox as unavailable.
func NewSandbox() (*Sandbox, error) { return nil, ErrSandboxUnsupported }

// Run refuses every command.
func (s *Sandbox) Run(context.Context, SandboxSpec) (SandboxResult, error) {
	return SandboxResult{}, ErrSandboxUnsupported
}

// Probe refuses: nothing can be verified here.
func (s *Sandbox) Probe(context.Context, string, int) error { return ErrSandboxUnsupported }

// PrepareRunDir refuses.
func (s *Sandbox) PrepareRunDir(string) error { return ErrSandboxUnsupported }

// LockDataDir refuses.
func (s *Sandbox) LockDataDir(string) error { return ErrSandboxUnsupported }

// SandboxExecMain never confines anything here, so it fails closed: the
// command is refused rather than run unrestricted.
func SandboxExecMain(args []string) int {
	_, _ = fmt.Fprintln(os.Stderr, "sandbox-exec: "+ErrSandboxUnsupported.Error())
	return sandboxExecExitFailed
}
