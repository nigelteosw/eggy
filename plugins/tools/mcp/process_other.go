//go:build !unix

package mcp

import "os/exec"

// Process groups are a POSIX concept. Elsewhere the best available cleanup is
// killing the child Eggy started, which is what the SDK's transport already
// attempts on close.
func configureProcessGroup(*exec.Cmd) {}

func terminateProcessGroup(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
