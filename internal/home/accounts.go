package home

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/nigelteosw/eggy/internal/fsutil/atomicfile"
)

// accountIDPattern is the bound an account ID must satisfy before it becomes
// a directory name. It matches the identifier rule config enforces, restated
// here because a path is built from it and this package must not trust that
// validation happened somewhere else.
var accountIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Accounts is the directory holding every account's private documents.
func (l Layout) Accounts() string { return filepath.Join(l.Root, "accounts") }

// AccountMemories is where one account's USER.md, MEMORY.md and WATCH.md
// live: <home>/accounts/<id>/memories. The ID is validated here, at the one
// place it turns into a path, so nothing upstream can steer a write outside
// the accounts directory.
func (l Layout) AccountMemories(accountID string) (string, error) {
	if !accountIDPattern.MatchString(accountID) {
		return "", fmt.Errorf("account id %q cannot name a directory", accountID)
	}
	return filepath.Join(l.Accounts(), accountID, "memories"), nil
}

// The document migration's phases, recorded in SQLite by bootstrap between
// filesystem steps so a boot that dies partway resumes at the step it was on
// rather than repeating one that already happened.
const (
	DocumentMigrationCopying  = "copying"
	DocumentMigrationCopied   = "copied"
	DocumentMigrationComplete = "complete"
)

// DocumentMigrationProgress is the durable record of how far the document
// migration got. It is an interface so this package stays unaware of SQLite.
type DocumentMigrationProgress interface {
	DocumentMigrationPhase() (string, error)
	RecordDocumentMigrationPhase(phase string) error
}

// privateDocuments are the files that move from the shared memories
// directory to the account's own. SOUL.md is not among them: it stays shared.
var privateDocuments = []string{"USER.md", "MEMORY.md", "WATCH.md"}

// MigrateAccountDocuments moves the pre-accounts memories directory to the
// named account. The sequence is:
//
//	record copying -> copy to a temporary account path -> verify bytes
//	-> atomic publish -> record copied -> archive original -> record complete
//
// Filesystem steps cannot join a database transaction, so each phase is
// recorded after the step it names and the whole thing is idempotent: a
// destination that already holds byte-identical copies is a retry and
// proceeds, a destination holding anything else is refused, and the original
// is never removed -- it is renamed to memories.migrated, the rollback.
func (l Layout) MigrateAccountDocuments(accountID string, progress DocumentMigrationProgress) error {
	phase, err := progress.DocumentMigrationPhase()
	if err != nil {
		return err
	}
	if phase == DocumentMigrationComplete {
		return nil
	}
	target, err := l.AccountMemories(accountID)
	if err != nil {
		return err
	}
	source := l.Memories()
	present, err := hasAnyPrivateDocument(source)
	if err != nil {
		return err
	}
	if !present {
		if phase == DocumentMigrationCopied {
			// The copy landed and the original was archived before the
			// record could say so.
			return progress.RecordDocumentMigrationPhase(DocumentMigrationComplete)
		}
		return nil
	}
	if phase != DocumentMigrationCopied {
		if err := progress.RecordDocumentMigrationPhase(DocumentMigrationCopying); err != nil {
			return err
		}
		if err := copyPrivateDocuments(source, target); err != nil {
			return err
		}
		if err := progress.RecordDocumentMigrationPhase(DocumentMigrationCopied); err != nil {
			return err
		}
	}
	if err := archiveDirectory(source); err != nil {
		return err
	}
	return progress.RecordDocumentMigrationPhase(DocumentMigrationComplete)
}

// RenameAccount moves one account's directory to a new ID, for a deployment
// converted from a single owner whose records were recorded under the old
// name. Nothing to move is fine; a destination that already exists is left
// alone and the old directory kept beside it, which the operator resolves.
func (l Layout) RenameAccount(oldID, newID string) error {
	from, err := l.AccountMemories(oldID)
	if err != nil {
		return err
	}
	to, err := l.AccountMemories(newID)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(from); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%s already exists; %s was left in place", to, from)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	// The old account directory is empty now; removing it keeps the home
	// from accumulating names nobody uses. Non-empty is left as it is.
	_ = os.Remove(filepath.Dir(from))
	return nil
}

func hasAnyPrivateDocument(dir string) (bool, error) {
	for _, name := range privateDocuments {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

// copyPrivateDocuments stages copies beside the target, verifies each one
// byte for byte, and publishes the staged directory with one rename. A
// target that already exists is reconciled file by file: a file already
// there must be identical (a completed earlier attempt), one that is missing
// is copied in, and anything else is somebody's live documents and is left
// alone.
func copyPrivateDocuments(source, target string) error {
	if _, err := os.Lstat(target); err == nil {
		return reconcileExisting(source, target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staging := target + ".tmp"
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return err
	}
	for _, name := range privateDocuments {
		body, err := os.ReadFile(filepath.Join(source, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := atomicfile.Write(filepath.Join(staging, name), body, 0o600); err != nil {
			return err
		}
		copied, err := os.ReadFile(filepath.Join(staging, name))
		if err != nil {
			return err
		}
		if !bytes.Equal(body, copied) {
			return fmt.Errorf("copy of %s did not verify", name)
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.Rename(staging, target)
}

func reconcileExisting(source, target string) error {
	for _, name := range privateDocuments {
		want, err := os.ReadFile(filepath.Join(source, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(target, name))
		if errors.Is(err, os.ErrNotExist) {
			if err := atomicfile.Write(filepath.Join(target, name), want, 0o600); err != nil {
				return err
			}
			continue
		}
		if err != nil || !bytes.Equal(want, got) {
			return fmt.Errorf("%s already exists with different content; it was not overwritten", filepath.Join(target, name))
		}
	}
	return nil
}

// archiveDirectory renames dir out of the way, never over an earlier archive.
func archiveDirectory(dir string) error {
	archive := dir + ".migrated"
	if _, err := os.Lstat(archive); err == nil {
		archive = fmt.Sprintf("%s.%d", archive, os.Getpid())
	}
	return os.Rename(dir, archive)
}
