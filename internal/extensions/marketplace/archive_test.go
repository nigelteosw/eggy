package marketplace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"errors"
	"io"
	"strconv"
	"testing"
)

type archiveEntry struct {
	name     string
	typeflag byte
	linkname string
	body     []byte
	size     int64
}

func buildArchive(t *testing.T, entries []archiveEntry) io.Reader {
	t.Helper()
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		size := int64(len(e.body))
		if e.size > 0 {
			size = e.size
		}
		h := &tar.Header{Name: e.name, Typeflag: typeflag, Linkname: e.linkname, Mode: 0o644, Size: size}
		if typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg && len(e.body) > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
		if typeflag == tar.TypeReg && e.size > 0 {
			// A sparse-style stream: write zeros up to the declared size.
			if _, err := io.CopyN(tw, zeroReader{}, e.size-int64(len(e.body))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &raw
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestReadArchiveStripsPrefix(t *testing.T) {
	r := buildArchive(t, []archiveEntry{
		{name: "o-r-abc/skills/pdf/SKILL.md", body: []byte("---\nname: pdf\n---\n")},
	})
	tree, err := readArchive(r)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := tree.Files["skills/pdf/SKILL.md"]
	if !ok {
		t.Fatalf("SKILL.md not under stripped path; files=%v", keys(tree.Files))
	}
	if !bytes.Contains(got, []byte("name: pdf")) {
		t.Fatalf("content lost: %q", got)
	}
}

func TestReadArchiveSkipsNonText(t *testing.T) {
	r := buildArchive(t, []archiveEntry{
		{name: "o-r-abc/skills/pdf/scripts/fill.py", body: []byte("print(1)")},
	})
	tree, err := readArchive(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tree.Files["skills/pdf/scripts/fill.py"]; ok {
		t.Fatal("non-text file kept as content")
	}
	if size, ok := tree.Skipped["skills/pdf/scripts/fill.py"]; !ok || size != int64(len("print(1)")) {
		t.Fatalf("Skipped = %v, want fill.py at %d bytes", tree.Skipped, len("print(1)"))
	}
}

func TestReadArchiveOversizedTextIsSkipped(t *testing.T) {
	r := buildArchive(t, []archiveEntry{
		{name: "o-r-abc/skills/big/reference.md", size: maxTextFile + 1},
	})
	tree, err := readArchive(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tree.Files["skills/big/reference.md"]; ok {
		t.Fatal("oversized text kept as content")
	}
	if _, ok := tree.Skipped["skills/big/reference.md"]; !ok {
		t.Fatalf("oversized text not reported as skipped: %v", keys(tree.Skipped))
	}
}

func TestReadArchiveRejects(t *testing.T) {
	cases := []struct {
		name    string
		entries []archiveEntry
	}{
		{"symlink", []archiveEntry{{name: "o-r-abc/skills/x/ref.md", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"}}},
		{"hardlink", []archiveEntry{{name: "o-r-abc/skills/x/ref.md", typeflag: tar.TypeLink, linkname: "o-r-abc/other"}}},
		{"dotdot", []archiveEntry{{name: "../x", body: []byte("x")}}},
		{"dotdot after prefix", []archiveEntry{{name: "o-r-abc/../x", body: []byte("x")}}},
		{"absolute", []archiveEntry{{name: "/abs/x", body: []byte("x")}}},
		{"char device", []archiveEntry{{name: "o-r-abc/dev", typeflag: tar.TypeChar}}},
		{"too many entries", manyEntries(maxEntries + 1)},
		{"uncompressed over limit", []archiveEntry{{name: "o-r-abc/huge.txt", size: maxUncompressed + 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readArchive(buildArchive(t, tc.entries))
			if !errors.Is(err, ErrArchiveRejected) {
				t.Fatalf("err = %v, want ErrArchiveRejected", err)
			}
		})
	}
}

func TestReadArchiveRejectsCompressedOverLimit(t *testing.T) {
	junk := make([]byte, maxCompressed+1)
	if _, err := rand.Read(junk); err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	raw.Write(junk)
	_, err := readArchive(&raw)
	if !errors.Is(err, ErrArchiveRejected) {
		t.Fatalf("err = %v, want ErrArchiveRejected", err)
	}
}

func manyEntries(n int) []archiveEntry {
	entries := make([]archiveEntry, 0, n)
	for i := range n {
		entries = append(entries, archiveEntry{name: "o-r-abc/f" + strconv.Itoa(i) + ".txt", body: []byte("x")})
	}
	return entries
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
