package agentrunner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// editFile writes body, runs one edit call on it, and returns the file after.
func editFile(t *testing.T, body, input string) (string, error) {
	t.Helper()
	w, run, ws := newWorkspaceForTools(t, &fakeRunner{}, nil)
	edit := findTool(t, CodingTools(w, run, false), "edit")
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := call(t, edit, input)
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(after), err
}

// Several disjoint edits in one call each match the original file, so one call
// can change every place a function is used.
func TestEditAppliesSeveralEditsAgainstTheOriginal(t *testing.T) {
	got, err := editFile(t, "a := old()\nb := old()\nc := 1\n",
		`{"path":"f.txt","edits":[{"old":"a := old()","new":"a := new()"},{"old":"b := old()","new":"b := new()"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "a := new()\nb := new()\nc := 1\n" {
		t.Fatalf("file = %q", got)
	}
}

// Edits that touch the same text are refused, so the model merges them rather
// than relying on the order they are applied in.
func TestEditRejectsOverlappingEdits(t *testing.T) {
	_, err := editFile(t, "abcdef\n", `{"path":"f.txt","edits":[{"old":"abcd","new":"X"},{"old":"cdef","new":"Y"}]}`)
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("err = %v, want an overlap refusal", err)
	}
}

// Text that differs only in quotes, dashes, or trailing spaces still matches,
// and the lines around it keep their exact bytes.
func TestEditLenientMatchKeepsOtherLinesExact(t *testing.T) {
	body := "say “hi” now   \nkeep this  \t\n"
	got, err := editFile(t, body, `{"path":"f.txt","edits":[{"old":"say \"hi\" now","new":"say bye"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "say bye   \nkeep this  \t\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestEditExactMatchIsPreferredOverLenient(t *testing.T) {
	// The exact text occurs once; the lenient form would also match the curly
	// quote line. Exact wins, so only the exact line changes.
	body := "x = “1”\nx = \"1\"\n"
	got, err := editFile(t, body, `{"path":"f.txt","edits":[{"old":"x = \"1\"","new":"x = 2"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "x = “1”\nx = 2\n" {
		t.Fatalf("file = %q", got)
	}
}

// Multi-line text with tab indentation must match exactly, and a partial line
// at either end of the match is replaced without touching the rest of it.
func TestEditMultiLinePartialLines(t *testing.T) {
	body := "func a() {\n\treturn 1\n}\nfunc b() {}\n"
	got, err := editFile(t, body, `{"path":"f.txt","edits":[{"old":"\treturn 1\n}\nfunc b() {}","new":"\treturn 2\n}\nfunc b() { }"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "func a() {\n\treturn 2\n}\nfunc b() { }\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestEditKeepsCRLFAndByteOrderMark(t *testing.T) {
	body := "\uFEFFone\r\ntwo\r\n"
	got, err := editFile(t, body, `{"path":"f.txt","edits":[{"old":"two","new":"2"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "\uFEFFone\r\n2\r\n" {
		t.Fatalf("file = %q", got)
	}
}

// Two matches are an error that tells the model what to add, not a guess.
func TestEditRefusesAmbiguousText(t *testing.T) {
	_, err := editFile(t, "x\nx\n", `{"path":"f.txt","edits":[{"old":"x","new":"y"}]}`)
	if err == nil || !strings.Contains(err.Error(), "must be unique") {
		t.Fatalf("err = %v, want a uniqueness refusal", err)
	}
}

// A miss says to re-read, because the usual cause is text copied from a
// summary rather than from the file.
func TestEditMissSaysToReread(t *testing.T) {
	_, err := editFile(t, "x\n", `{"path":"f.txt","edits":[{"old":"absent","new":"y"}]}`)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "re-read") {
		t.Fatalf("err = %v, want a re-read hint", err)
	}
}

func TestEditRefusesANoOp(t *testing.T) {
	_, err := editFile(t, "x\n", `{"path":"f.txt","edits":[{"old":"x","new":"x"}]}`)
	if err == nil {
		t.Fatal("identical replacement accepted")
	}
}

// The single-edit form the model learned first still works.
func TestEditAcceptsTheLegacySingleEditForm(t *testing.T) {
	got, err := editFile(t, "hello\n", `{"path":"f.txt","old":"hello","new":"bye"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "bye\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestEditRequiresAtLeastOneReplacement(t *testing.T) {
	_, err := editFile(t, "x\n", `{"path":"f.txt","edits":[]}`)
	if err == nil {
		t.Fatal("empty edits accepted")
	}
}
