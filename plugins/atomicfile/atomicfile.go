// Package atomicfile durably replaces a file's content: write to a sibling
// temp file, fsync it, rename over the target, then fsync the directory so
// the rename itself survives a crash. Every flat-file adapter store (state,
// implementation sessions, durable context, skills) shares this so a crash
// mid-write never leaves a torn or half-written file.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write atomically replaces path with data, creating its parent directory if
// needed.
func Write(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create directory for %s: %w", filepath.Base(path), err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", filepath.Base(path), err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	directory, err := os.Open(dir)
	if err == nil {
		err = directory.Sync()
		_ = directory.Close()
	}
	return err
}
