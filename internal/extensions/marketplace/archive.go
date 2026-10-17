package marketplace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	maxCompressed   = 20 << 20
	maxUncompressed = 64 << 20
	maxEntries      = 5000
	maxTextFile     = 256 << 10
)

// ErrArchiveRejected wraps every reason an archive is refused. A rejected
// archive is refused whole: nothing from it is installed.
var ErrArchiveRejected = errors.New("archive rejected")

// textExtensions is the allowlist of files kept as content. It matches the
// reference extensions the skills store accepts, so a fetched file is one the
// store could write.
var textExtensions = map[string]bool{
	".md": true, ".txt": true, ".json": true, ".yaml": true, ".yml": true, ".csv": true,
}

// Tree is one repository snapshot. Paths are slash-separated, with GitHub's
// top-level "<owner>-<repo>-<sha>/" directory removed. Files holds text content
// that may be installed; Skipped maps every other regular file to its size, so
// the review can say what was left out and why.
type Tree struct {
	Files   map[string][]byte
	Skipped map[string]int64
}

// readArchive reads a gzipped tar stream under hard limits. Both the compressed
// and the uncompressed sizes are checked, so a small archive that expands
// enormously is refused too.
func readArchive(r io.Reader) (Tree, error) {
	compressed, err := io.ReadAll(io.LimitReader(r, maxCompressed+1))
	if err != nil {
		return Tree{}, err
	}
	if len(compressed) > maxCompressed {
		return Tree{}, fmt.Errorf("%w: compressed size exceeds %d bytes", ErrArchiveRejected, maxCompressed)
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return Tree{}, fmt.Errorf("%w: %v", ErrArchiveRejected, err)
	}
	raw, err := io.ReadAll(io.LimitReader(gz, maxUncompressed+1))
	if err != nil {
		return Tree{}, fmt.Errorf("%w: %v", ErrArchiveRejected, err)
	}
	if len(raw) > maxUncompressed {
		return Tree{}, fmt.Errorf("%w: uncompressed size exceeds %d bytes", ErrArchiveRejected, maxUncompressed)
	}

	tree := Tree{Files: map[string][]byte{}, Skipped: map[string]int64{}}
	tr := tar.NewReader(bytes.NewReader(raw))
	for entries := 0; ; entries++ {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Tree{}, fmt.Errorf("%w: %v", ErrArchiveRejected, err)
		}
		if entries >= maxEntries {
			return Tree{}, fmt.Errorf("%w: more than %d entries", ErrArchiveRejected, maxEntries)
		}
		name, err := archivePath(header.Name)
		if err != nil {
			return Tree{}, err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeSymlink:
			return Tree{}, fmt.Errorf("%w: symlink %s", ErrArchiveRejected, name)
		case tar.TypeLink:
			return Tree{}, fmt.Errorf("%w: hardlink %s", ErrArchiveRejected, name)
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return Tree{}, fmt.Errorf("%w: device or pipe %s", ErrArchiveRejected, name)
		case tar.TypeReg, tar.TypeRegA:
		default:
			return Tree{}, fmt.Errorf("%w: entry type %q at %s", ErrArchiveRejected, header.Typeflag, name)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			return Tree{}, fmt.Errorf("%w: %v", ErrArchiveRejected, err)
		}
		if !isTextPath(name) || len(content) > maxTextFile {
			tree.Skipped[name] = int64(len(content))
			continue
		}
		tree.Files[name] = content
	}
	return tree, nil
}

// archivePath checks one entry name and removes GitHub's top-level directory.
// Any absolute path or ".." component refuses the whole archive, before the
// prefix is removed, so "../x" and "owner-repo-sha/../x" both fail.
func archivePath(name string) (string, error) {
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("%w: absolute path %s", ErrArchiveRejected, name)
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if part == ".." {
			return "", fmt.Errorf("%w: path escapes the archive: %s", ErrArchiveRejected, name)
		}
	}
	if len(parts) < 2 {
		return "", fmt.Errorf("%w: entry outside the repository directory: %s", ErrArchiveRejected, name)
	}
	return path.Clean(strings.Join(parts[1:], "/")), nil
}

func isTextPath(name string) bool {
	return path.Base(name) == "SKILL.md" || textExtensions[path.Ext(name)]
}
