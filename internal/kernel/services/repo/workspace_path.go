package repo

import (
	"errors"
	"path/filepath"
	"strings"
)

var ErrPathOutsideWorkspace = errors.New("path escapes the workspace")

// resolveWorkspacePath joins path onto workspace and refuses any result whose
// *lexical* form escapes it. It stops a path-traversal mistake, not an
// attacker.
//
// The containment is deliberately lexical: filepath.Abs + filepath.Rel with no
// EvalSymlinks, so a symlink inside the checkout pointing at / resolves
// outside the workspace and passes this check. That is not an oversight to be
// fixed here, because the boundary does not exist at this layer anyway -- the
// terminal tool hands an arbitrary command string to sh -c in the same
// workspace, and that child can read and write anywhere Eggy's own user can.
// Adding EvalSymlinks would harden one door in a room with no walls, and would
// advertise a guarantee the process cannot keep.
//
// The actual threat model: configured repositories are trusted code, run as
// Eggy's own user with Eggy's own filesystem access. Nothing here defends
// against a hostile checkout. If that assumption ever stops holding, the fix
// is process isolation (see "Evaluate stronger execution isolation" in
// TODO.md), not a stricter path join.
func resolveWorkspacePath(workspace, path string) (string, error) {
	if path == "" {
		return "", errors.New("path must not be empty")
	}
	absoluteWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	absoluteJoined, err := filepath.Abs(filepath.Join(workspace, path))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(absoluteWorkspace, absoluteJoined)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrPathOutsideWorkspace
	}
	return absoluteJoined, nil
}
