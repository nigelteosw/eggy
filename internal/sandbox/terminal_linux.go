//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// TerminalUIDBase is the first uid an account's terminals run as. Each account
// gets its own uid, never coder's: everything a terminal makes, its ptys
// above all, is protected by ordinary ownership from every other account's
// programs, which Landlock alone cannot do for /dev/pts. The per-uid process
// limit is then per account too.
const TerminalUIDBase = 200000

// allocMu serializes uid allocation, so two accounts preparing at once
// cannot both take the next free uid.
var allocMu sync.Mutex

// PrepareTerminalDir makes an account's terminal directory: root-owned and
// traversable, with ws/, home/ and tmp/ owned by the account's own uid,
// allocated on first use and read back from home/'s owner after that. It
// never walks the subtrees, whose programs may be running: only the three
// directories it creates are chowned, and only when they are new.
func (s *Sandbox) PrepareTerminalDir(dir string) error {
	if err := validRunDir(dir); err != nil {
		return err
	}
	allocMu.Lock()
	defer allocMu.Unlock()
	if err := os.MkdirAll(dir, 0o711); err != nil {
		return err
	}
	if err := os.Lchown(dir, 0, 0); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o711); err != nil {
		return err
	}
	uid, err := terminalOwner(dir)
	if errors.Is(err, os.ErrNotExist) {
		uid, err = nextTerminalUID(filepath.Dir(dir))
	}
	if err != nil {
		return err
	}
	for _, sub := range agentWritable {
		path := filepath.Join(dir, sub)
		if err := os.Mkdir(path, 0o700); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return err
		}
		if err := os.Lchown(path, int(uid), int(uid)); err != nil {
			return err
		}
	}
	return ensureUser(uid)
}

// terminalOwner is the uid that owns dir's home/, which must be a terminal
// uid. Anything else (root, coder) is refused rather than run as.
func terminalOwner(dir string) (uint32, error) {
	info, err := os.Lstat(filepath.Join(dir, "home"))
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return 0, fmt.Errorf("sandbox: %s/home is not a directory", dir)
	}
	if st.Uid < TerminalUIDBase {
		return 0, fmt.Errorf("sandbox: %s/home belongs to uid %d, not a terminal user", dir, st.Uid)
	}
	return st.Uid, nil
}

// nextTerminalUID is one past the highest uid any account directory under
// root uses. The directories are the record; there is no second one.
func nextTerminalUID(root string) (uint32, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	next := uint32(TerminalUIDBase)
	for _, e := range entries {
		if uid, err := terminalOwner(filepath.Join(root, e.Name())); err == nil && uid >= next {
			next = uid + 1
		}
	}
	return next, nil
}

// ensureUser gives uid a passwd and group entry, so programs that look up
// their own user (Node's os.userInfo, bash's prompt) work. The home field is
// "/" so the entry, which every program can read, names no directory.
func ensureUser(uid uint32) error {
	id := strconv.FormatUint(uint64(uid), 10)
	name := "eggy-" + id
	if err := ensureLine("/etc/passwd", ":"+id+":", name+":x:"+id+":"+id+":Eggy terminal:/:/bin/bash"); err != nil {
		return err
	}
	return ensureLine("/etc/group", name+":", name+":x:"+id+":")
}

func ensureLine(file, marker, line string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	for _, existing := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(existing, "eggy-") && strings.Contains(existing, marker) {
			return nil
		}
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	prefix := ""
	if len(data) > 0 && data[len(data)-1] != '\n' {
		prefix = "\n"
	}
	if _, err := f.WriteString(prefix + line + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Terminal returns argv confined in terminal mode in dir, as the account's own
// uid; dir must have been prepared with PrepareTerminalDir. It has no timeout
// and no output capture: the caller wires its pipes, starts it, and kills its
// process group when done.
func (s *Sandbox) Terminal(dir string, argv []string) (*exec.Cmd, error) {
	if err := validRunDir(dir); err != nil {
		return nil, err
	}
	if len(argv) == 0 {
		return nil, errors.New("sandbox: empty command")
	}
	uid, err := terminalOwner(dir)
	if err != nil {
		return nil, err
	}
	return s.command(dir, true, argv, uid, uid), nil
}
