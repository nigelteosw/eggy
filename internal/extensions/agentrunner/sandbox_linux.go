//go:build linux

package agentrunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Landlock rule types and the net-port attribute, from include/uapi/linux/landlock.h.
// x/sys defines the access flags but not these two rule kinds.
const (
	landlockRulePathBeneath = 1
	landlockRuleNetPort     = 2
)

type landlockNetPortAttr struct {
	AllowedAccess uint64
	Port          uint64
}

// minLandlockABI is the lowest ABI the sandbox accepts. ABI 6 is the one that
// covers the net and signal/socket scoping the design relies on; an older
// kernel is refused rather than confined less than the spec promises.
const minLandlockABI = 6

// The filesystem rights a rule may grant, and the subset a read-only tree needs.
const (
	fsReadExec = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR
	fsAll      = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM | unix.LANDLOCK_ACCESS_FS_REFER |
		unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
)

// readOnlyTrees are the system paths a command may read and execute: the
// base system and the toolchains the image bakes in.
var readOnlyTrees = []string{"/usr", "/bin", "/lib", "/lib64", "/etc", "/usr/local/go", "/usr/local/node", "/proc/self"}

// writableDevices are the only device nodes a command may write.
var writableDevices = []string{"/dev/null"}

// webPorts are the only TCP ports a command may connect to: dependency
// downloads over HTTPS and HTTP. Binding is not granted at all.
var webPorts = []uint64{443, 80}

// Sandbox runs commands as the coder user, confined by Landlock.
type Sandbox struct {
	coderUID uint32
	coderGID uint32
	self     string
}

// NewSandbox looks up the coder account and records this executable, which
// the sandbox-exec entry point re-runs as.
func NewSandbox() (*Sandbox, error) {
	u, err := user.Lookup("coder")
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, err
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &Sandbox{coderUID: uint32(uid), coderGID: uint32(gid), self: self}, nil
}

// PrepareRunDir creates a run's directories, owned by coder and private to it.
func (s *Sandbox) PrepareRunDir(runDir string) error {
	if err := makeRunDirs(runDir); err != nil {
		return err
	}
	return filepath.WalkDir(runDir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, int(s.coderUID), int(s.coderGID))
	})
}

// Run executes spec.Argv as coder inside spec.RunDir, through the sandbox-exec
// entry point. The command runs in its own process group, which is killed on
// timeout, and its output is capped.
func (s *Sandbox) Run(ctx context.Context, spec SandboxSpec) (SandboxResult, error) {
	if err := validRunDir(spec.RunDir); err != nil {
		return SandboxResult{}, err
	}
	if len(spec.Argv) == 0 {
		return SandboxResult{}, errors.New("sandbox: empty command")
	}
	timeout, capBytes := spec.Timeout, spec.OutputCap
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	if capBytes <= 0 {
		capBytes = defaultOutputCap
	}
	args := append([]string{"sandbox-exec", spec.RunDir, "--"}, spec.Argv...)
	cmd := exec.Command(s.self, args...)
	cmd.Dir = filepath.Join(spec.RunDir, "ws")
	cmd.Env = runEnv(spec.RunDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:    true,
		Credential: &syscall.Credential{Uid: s.coderUID, Gid: s.coderGID},
	}
	out := &cappedBuffer{limit: capBytes}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		return SandboxResult{}, fmt.Errorf("sandbox: start: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan struct{})
	var timedOut bool
	var mu sync.Mutex
	go func() {
		select {
		case <-ctx.Done():
			mu.Lock()
			timedOut = true
			mu.Unlock()
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		case <-done:
		}
	}()
	waitErr := cmd.Wait()
	close(done)
	mu.Lock()
	didTimeOut := timedOut
	mu.Unlock()
	result := SandboxResult{Output: out.String(), Truncated: out.truncated, TimedOut: didTimeOut, ExitCode: -1}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if waitErr != nil && cmd.ProcessState == nil {
		return result, fmt.Errorf("sandbox: wait: %w", waitErr)
	}
	return result, nil
}

// cappedBuffer keeps the first limit bytes and reports whether it dropped any.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.limit - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		b.truncated = true
		p = p[:room]
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Probe proves the sandbox works before any command is trusted with it. The
// exec check must succeed, which proves sandbox-exec ran at all. Each denial
// check must fail with an ordinary error, not with the entry point's own
// failure code, so a broken sandbox can never pass as a working one.
func (s *Sandbox) Probe(ctx context.Context, dataDir string, eggyPort int) error {
	id, err := newProbeID()
	if err != nil {
		return err
	}
	runDir := filepath.Join(dataDir, "agent-runs", ".probe-"+id)
	if err := s.PrepareRunDir(runDir); err != nil {
		return fmt.Errorf("sandbox probe: prepare: %w", err)
	}
	defer os.RemoveAll(runDir)

	// A root-only file that exists, so reading it can only fail because of the
	// sandbox, not because it is missing.
	secret := filepath.Join(dataDir, ".probe-secret-"+id)
	if err := os.WriteFile(secret, []byte("probe"), 0o600); err != nil {
		return fmt.Errorf("sandbox probe: write secret: %w", err)
	}
	defer os.Remove(secret)
	outside := filepath.Join(dataDir, ".probe-write-"+id)
	defer os.Remove(outside)

	checks := []struct {
		name string
		argv []string
		want string // "ok" needs exit 0; "denied" needs a plain non-zero exit
	}{
		{"exec", []string{"true"}, "ok"},
		{"read root-only file", []string{"cat", secret}, "denied"},
		{"write outside run", []string{"sh", "-c", "touch " + outside}, "denied"},
		{"connect to eggy port", []string{"bash", "-c", fmt.Sprintf("exec 3<>/dev/tcp/127.0.0.1/%d", eggyPort)}, "denied"},
		{"signal eggyd", []string{"sh", "-c", fmt.Sprintf("kill -0 %d", os.Getpid())}, "denied"},
	}
	for _, check := range checks {
		result, err := s.Run(ctx, SandboxSpec{RunDir: runDir, Argv: check.argv, Timeout: 30 * time.Second})
		if err != nil {
			return fmt.Errorf("sandbox probe: %s: %w", check.name, err)
		}
		switch check.want {
		case "ok":
			if result.ExitCode != 0 {
				return fmt.Errorf("sandbox probe: %s: exit %d: %s", check.name, result.ExitCode, result.Output)
			}
		case "denied":
			if result.ExitCode == 0 || result.ExitCode == sandboxExecExitFailed || result.TimedOut {
				return fmt.Errorf("sandbox probe: %s was not denied (exit %d)", check.name, result.ExitCode)
			}
		}
	}
	return nil
}

// LockDataDir makes everything under dataDir root-only, except that the run
// directories stay private to coder. It runs at boot, before any run exists,
// and only when eggyd is root.
func (s *Sandbox) LockDataDir(dataDir string) error {
	if os.Geteuid() != 0 {
		return errors.New("sandbox: locking the data directory needs root")
	}
	if err := os.Chmod(dataDir, 0o711); err != nil {
		return err
	}
	runs := filepath.Join(dataDir, "agent-runs")
	if err := os.MkdirAll(runs, 0o711); err != nil {
		return err
	}
	return filepath.WalkDir(dataDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dataDir {
			return nil
		}
		if path == runs {
			return os.Chmod(runs, 0o711)
		}
		if within(path, runs) {
			// Run directories and their contents belong to coder; interrupted
			// runs keep their workspaces across restarts.
			if err := os.Lchown(path, int(s.coderUID), int(s.coderGID)); err != nil {
				return err
			}
			if entry.IsDir() {
				return os.Chmod(path, 0o700)
			}
			return nil
		}
		if err := os.Lchown(path, 0, 0); err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		if entry.Type().IsRegular() {
			return os.Chmod(path, 0o600)
		}
		return nil
	})
}

// within reports whether path is dir or below it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}

// SandboxExecMain is the sandbox-exec entry point, run inside the child
// process as coder. It confines itself and then replaces itself with the
// command. Any failure exits 125 before the command could run unconfined.
func SandboxExecMain(args []string) int {
	// Landlock restricts the calling thread only, and Go may move a goroutine
	// to another thread between confine and exec. Pinning keeps the restriction
	// and the exec on the same thread, so the command cannot run unconfined.
	runtime.LockOSThread()
	runDir, argv, err := parseSandboxExec(args)
	if err != nil {
		return failSandbox(err)
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return failSandbox(err)
	}
	if err := confine(runDir); err != nil {
		return failSandbox(err)
	}
	if err := unix.Exec(path, argv, runEnv(runDir)); err != nil {
		return failSandbox(err)
	}
	return sandboxExecExitFailed
}

func failSandbox(err error) int {
	_, _ = fmt.Fprintln(os.Stderr, "sandbox-exec: "+err.Error())
	return sandboxExecExitFailed
}

// confine applies the ruleset to the calling process. It is one-way: once
// restricted, the process and everything it execs stays restricted.
func confine(runDir string) error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	if err := setLimits(); err != nil {
		return err
	}
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return fmt.Errorf("landlock unavailable: %w", errno)
	}
	if abi < minLandlockABI {
		return fmt.Errorf("landlock ABI %d is below the required %d", abi, minLandlockABI)
	}
	attr := unix.LandlockRulesetAttr{
		Access_fs:  fsAll,
		Access_net: unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP,
		Scoped:     unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET | unix.LANDLOCK_SCOPE_SIGNAL,
	}
	rulesetFD, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("landlock_create_ruleset: %w", errno)
	}
	defer unix.Close(int(rulesetFD))

	for _, tree := range readOnlyTrees {
		if err := addPathRule(rulesetFD, tree, fsReadExec); err != nil {
			return err
		}
	}
	for _, device := range writableDevices {
		if err := addPathRule(rulesetFD, device, fsReadExec|unix.LANDLOCK_ACCESS_FS_WRITE_FILE); err != nil {
			return err
		}
	}
	// The run directory is the only writable tree.
	if err := addPathRule(rulesetFD, runDir, fsAll); err != nil {
		return err
	}
	for _, port := range webPorts {
		net := landlockNetPortAttr{AllowedAccess: unix.LANDLOCK_ACCESS_NET_CONNECT_TCP, Port: port}
		if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, rulesetFD, landlockRuleNetPort, uintptr(unsafe.Pointer(&net)), 0, 0, 0); errno != 0 {
			return fmt.Errorf("landlock net rule %d: %w", port, errno)
		}
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, rulesetFD, 0, 0); errno != 0 {
		return fmt.Errorf("landlock_restrict_self: %w", errno)
	}
	return nil
}

// addPathRule grants rights on one path, skipping a read-only tree the image
// does not have, so an older image still confines correctly.
func addPathRule(rulesetFD uintptr, path string, access uint64) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer unix.Close(fd)
	attr := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(fd)}
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, rulesetFD, landlockRulePathBeneath, uintptr(unsafe.Pointer(&attr)), 0, 0, 0); errno != 0 {
		return fmt.Errorf("landlock rule %s: %w", path, errno)
	}
	return nil
}

// setLimits bounds what a command can consume. The numbers are the design's
// constants: processes, file size and address space.
func setLimits() error {
	limits := []struct {
		resource int
		value    uint64
	}{
		{unix.RLIMIT_NPROC, 512},
		{unix.RLIMIT_FSIZE, 1 << 30},
		{unix.RLIMIT_AS, 8 << 30},
	}
	for _, l := range limits {
		if err := unix.Setrlimit(l.resource, &unix.Rlimit{Cur: l.value, Max: l.value}); err != nil {
			return fmt.Errorf("setrlimit %d: %w", l.resource, err)
		}
	}
	return nil
}
