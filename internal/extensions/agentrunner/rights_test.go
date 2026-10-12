//go:build linux

package agentrunner

import (
	"testing"

	"golang.org/x/sys/unix"
)

// A file or device cannot carry directory-only rights: Landlock answers such a
// rule with EINVAL, which is what stopped the probe on /dev/null.
func TestRightsForFilesDropsDirectoryRights(t *testing.T) {
	access := uint64(fsReadExec | unix.LANDLOCK_ACCESS_FS_WRITE_FILE)
	got := rightsFor(access, false)
	if got&dirOnlyRights != 0 {
		t.Fatalf("a file kept directory-only rights: %#x", got)
	}
	if got&unix.LANDLOCK_ACCESS_FS_READ_FILE == 0 || got&unix.LANDLOCK_ACCESS_FS_WRITE_FILE == 0 {
		t.Fatalf("a file lost its file rights: %#x", got)
	}
	if rightsFor(access, true) != access {
		t.Fatal("a directory's rights were changed")
	}
}
