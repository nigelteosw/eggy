package terminal

import (
	"io"
	"os/exec"
	"sync"
	"syscall"
)

// StartFunc builds a confined command in dir; it is sandbox.Sandbox.Terminal.
type StartFunc func(dir string, argv []string) (*exec.Cmd, error)

// SandboxLauncher runs `hc connect` confined, as the sandbox user, in an
// account's directory. hc connect starts that directory's core when none is
// running; the core detaches into its own session, so ending this connection
// never ends it or its terminals.
type SandboxLauncher struct {
	Start StartFunc
}

// Connect starts hc connect and returns its pipes.
func (l SandboxLauncher) Connect(dir string) (Conn, error) {
	cmd, err := l.Start(dir, []string{"/usr/bin/hc", "connect"})
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &procConn{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

type procConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
	once   sync.Once
}

func (p *procConn) Read(b []byte) (int, error)  { return p.stdout.Read(b) }
func (p *procConn) Write(b []byte) (int, error) { return p.stdin.Write(b) }

// Close ends the client process only. Its process group is hc connect alone:
// the core it started has its own session.
func (p *procConn) Close() error {
	p.once.Do(func() {
		_ = p.stdin.Close()
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
		go func() { _ = p.cmd.Wait() }()
	})
	return nil
}
