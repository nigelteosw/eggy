package agentrunner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Offsets are 1-based, as search reports lines. Offset 2 is the second line.
func TestReadOffsetIsOneBased(t *testing.T) {
	w, run, ws := newWorkspaceForTools(t, &fakeRunner{}, nil)
	if err := os.WriteFile(filepath.Join(ws, "f.txt"), []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := findTool(t, CodingTools(w, run, false), "read")
	out, err := call(t, read, `{"path":"f.txt","offset":2,"limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "two") {
		t.Fatalf("offset 2 read %q, want the second line first", out)
	}
	if !strings.Contains(out, "Use offset=3 to continue") {
		t.Fatalf("a read that stops short does not say where to continue: %q", out)
	}
}

// A file read in parts ends with the last line and no continuation note.
func TestReadFinalPartHasNoContinuation(t *testing.T) {
	w, run, ws := newWorkspaceForTools(t, &fakeRunner{}, nil)
	if err := os.WriteFile(filepath.Join(ws, "f.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := call(t, findTool(t, CodingTools(w, run, false), "read"), `{"path":"f.txt","offset":2}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "two" {
		t.Fatalf("final part = %q, want just the last line", out)
	}
}

// A read is cut by bytes as well as lines, and the note says so and where to
// continue, so a long line cannot silently hide the rest of the file.
func TestReadCutByBytesSaysWhereToContinue(t *testing.T) {
	w, run, ws := newWorkspaceForTools(t, &fakeRunner{}, nil)
	var b strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&b, "line %04d %s\n", i, strings.Repeat("x", 60))
	}
	if err := os.WriteFile(filepath.Join(ws, "big.txt"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := call(t, findTool(t, CodingTools(w, run, false), "read"), `{"path":"big.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > maxReadBytes+200 {
		t.Fatalf("read returned %d bytes, over the %d-byte limit", len(out), maxReadBytes)
	}
	if !strings.Contains(out, "Use offset=") {
		t.Fatalf("byte-cut read gives no continuation: %q", out[len(out)-120:])
	}
}

// A single line over the byte limit cannot be read whole; the result says how
// to read it instead of returning nothing.
func TestReadOverlongLineSaysHowToReadIt(t *testing.T) {
	w, run, ws := newWorkspaceForTools(t, &fakeRunner{}, nil)
	if err := os.WriteFile(filepath.Join(ws, "min.js"), []byte(strings.Repeat("a", maxReadBytes+10)), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := call(t, findTool(t, CodingTools(w, run, false), "read"), `{"path":"min.js"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sed -n") {
		t.Fatalf("overlong line gives no way to read it: %q", out)
	}
}

func TestReadPastTheEndIsAnError(t *testing.T) {
	w, run, ws := newWorkspaceForTools(t, &fakeRunner{}, nil)
	_ = os.WriteFile(filepath.Join(ws, "f.txt"), []byte("one\n"), 0o600)
	if _, err := call(t, findTool(t, CodingTools(w, run, false), "read"), `{"path":"f.txt","offset":9}`); err == nil {
		t.Fatal("offset past the end accepted")
	}
}

// A cut command output says what it kept and where the whole of it is, so the
// model knows it has not seen everything.
func TestFormatResultNamesTheFullOutputWhenCut(t *testing.T) {
	out := formatResult(SandboxResult{Output: "FAIL", Truncated: true, FullOutputPath: "/runs/x/tmp/output-1.log", ExitCode: 1})
	if !strings.Contains(out, "/runs/x/tmp/output-1.log") || !strings.HasSuffix(out, "[exit 1]") {
		t.Fatalf("formatted = %q", out)
	}
	if plain := formatResult(SandboxResult{Output: "ok", ExitCode: 0}); strings.Contains(plain, "cut") {
		t.Fatalf("whole output marked as cut: %q", plain)
	}
}

// bash and run_check ask the sandbox for the whole output to be kept in the
// run's tmp directory, which the model's own commands can read.
func TestBashAndChecksKeepFullOutputInTmp(t *testing.T) {
	runner := &fakeRunner{}
	w, run, _ := newWorkspaceForTools(t, runner, [][]string{{"go", "test"}})
	tools := CodingTools(w, run, true)
	if _, err := call(t, findTool(t, tools, "bash"), `{"command":"true"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, findTool(t, tools, "run_check"), `{"index":0}`); err != nil {
		t.Fatal(err)
	}
	for _, spec := range runner.specs {
		if spec.FullOutputDir != filepath.Join(w.Dir(runIDA), "tmp") {
			t.Fatalf("full output dir = %q", spec.FullOutputDir)
		}
	}
}
