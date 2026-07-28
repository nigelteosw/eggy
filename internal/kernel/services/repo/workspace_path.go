package repo

import (
	"errors"
	"path/filepath"
	"strings"
)

var ErrPathOutsideWorkspace = errors.New("path escapes the workspace")

// resolveWorkspacePath joins path onto workspace and refuses any result
// that escapes it, so a primitive can never touch a file outside the
// checkout the session is bound to.
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
