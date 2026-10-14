package agentrunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// replacement is one exact-text change within a file.
type replacement struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// editArgs is the edit call. Edits is the form the tool offers. Old and New
// are the single-edit form the model learned first, and are still accepted.
type editArgs struct {
	Path  string        `json:"path"`
	Edits []replacement `json:"edits"`
	Old   *string       `json:"old"`
	New   *string       `json:"new"`
}

// editTool applies one or more replacements to one file. Each old text is
// matched against the file as it was when the call began, so the edits cannot
// change where a later one lands.
func editTool(ws string, in json.RawMessage) (string, error) {
	var args editArgs
	if err := json.Unmarshal(in, &args); err != nil {
		return "", err
	}
	path, err := resolveInside(ws, args.Path)
	if err != nil {
		return "", err
	}
	edits := args.Edits
	if args.Old != nil {
		legacy := replacement{Old: *args.Old}
		if args.New != nil {
			legacy.New = *args.New
		}
		edits = append(edits, legacy)
	}
	if len(edits) == 1 && edits[0].Old == "" {
		return createFile(path, args.Path, edits[0].New)
	}
	if len(edits) == 0 {
		return "", errors.New("edit needs at least one replacement in edits")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	// The matching works on LF text without a byte-order mark. Both are put
	// back afterwards, so an edit never changes a file's line endings or BOM.
	text := string(raw)
	bom := strings.HasPrefix(text, "\uFEFF")
	text = strings.TrimPrefix(text, "\uFEFF")
	crlf := lineEndingIsCRLF(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	updated, err := applyReplacements(text, edits, args.Path)
	if err != nil {
		return "", err
	}
	if updated == text {
		return "", fmt.Errorf("no changes made to %s: the replacements produced identical content", args.Path)
	}
	if crlf {
		updated = strings.ReplaceAll(updated, "\n", "\r\n")
	}
	if bom {
		updated = "\uFEFF" + updated
	}
	if err := os.WriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s (%d replacement(s))", args.Path, len(edits)), nil
}

func createFile(path, name, body string) (string, error) {
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists; give old text to edit it", name)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	return "created " + name, nil
}

// lineEndingIsCRLF reports whether the first line ending in text is CRLF.
func lineEndingIsCRLF(text string) bool {
	lf := strings.Index(text, "\n")
	return lf > 0 && text[lf-1] == '\r'
}

// match is one occurrence of an old text, as rune positions in the file's
// lines. The end is on line endLine, at endCol.
type match struct {
	startLine, startCol int
	endLine, endCol     int
}

// applyReplacements matches every replacement, refuses ambiguity and overlap,
// and then applies them. Positions are resolved against text before any edit
// is made.
func applyReplacements(text string, edits []replacement, name string) (string, error) {
	lines := strings.Split(text, "\n")
	// starts[i] is the byte offset of line i in text.
	starts := make([]int, len(lines))
	for i, offset := 0, 0; i < len(lines); i++ {
		starts[i] = offset
		offset += len(lines[i]) + 1
	}
	type span struct {
		start, end int
		text       string
		edit       int
	}
	spans := make([]span, 0, len(edits))
	for i, edit := range edits {
		if edit.Old == "" {
			return "", fmt.Errorf("edits[%d].old must not be empty in %s", i, name)
		}
		found, err := locate(lines, edit.Old, i, len(edits), name)
		if err != nil {
			return "", err
		}
		spans = append(spans, span{
			start: starts[found.startLine] + len(string([]rune(lines[found.startLine])[:found.startCol])),
			end:   starts[found.endLine] + len(string([]rune(lines[found.endLine])[:found.endCol])),
			text:  edit.New,
			edit:  i,
		})
	}
	slices.SortFunc(spans, func(a, b span) int { return a.start - b.start })
	for i := 1; i < len(spans); i++ {
		if spans[i-1].end > spans[i].start {
			return "", fmt.Errorf("edits[%d] and edits[%d] overlap in %s; merge them into one edit", spans[i-1].edit, spans[i].edit, name)
		}
	}
	result := text
	for i := len(spans) - 1; i >= 0; i-- {
		result = result[:spans[i].start] + spans[i].text + result[spans[i].end:]
	}
	return result, nil
}

// locate finds the one place old occurs in lines. An exact match is used when
// there is one; only if there is none does a lenient match count. A lenient
// match ignores curly quotes, Unicode dashes and spaces, and trailing spaces,
// since a model sees those differently from the file on disk.
func locate(lines []string, old string, index, total int, name string) (match, error) {
	what := "the text"
	if total > 1 {
		what = fmt.Sprintf("the text of edits[%d]", index)
	}
	exact := findMatches(lines, old, false)
	if len(exact) == 0 {
		exact = findMatches(lines, old, true)
	}
	switch len(exact) {
	case 0:
		return match{}, fmt.Errorf("could not find %s in %s; it must match exactly, including whitespace and newlines. Re-read the file: text copied from an earlier summary is not exact", what, name)
	case 1:
		return exact[0], nil
	default:
		return match{}, fmt.Errorf("found %d matches of %s in %s; it must be unique. Include more surrounding lines", len(exact), what, name)
	}
}

// findMatches returns every place old occurs. Lines are compared as rune
// slices, so a column is the same position in the file and in its normalized
// form: normalization changes characters one for one and only trims trailing
// spaces.
func findMatches(lines []string, old string, lenient bool) []match {
	normalize := func(s string) []rune {
		if !lenient {
			return []rune(s)
		}
		runes := []rune(s)
		for i, r := range runes {
			runes[i] = foldRune(r)
		}
		return []rune(strings.TrimRight(string(runes), " \t\r"))
	}
	fileLines := make([][]rune, len(lines))
	for i, line := range lines {
		fileLines[i] = normalize(line)
	}
	pattern := strings.Split(old, "\n")
	patternLines := make([][]rune, len(pattern))
	for i, line := range pattern {
		patternLines[i] = normalize(line)
	}
	var matches []match
	if len(patternLines) == 1 {
		p := patternLines[0]
		for i, line := range fileLines {
			for c := 0; c+len(p) <= len(line); c++ {
				if slices.Equal(line[c:c+len(p)], p) {
					matches = append(matches, match{i, c, i, c + len(p)})
				}
			}
		}
		return matches
	}
	// A multi-line old text starts partway into its first line, runs whole
	// lines through its middle, and ends partway into its last line.
	first, last := patternLines[0], patternLines[len(patternLines)-1]
	for i, line := range fileLines {
		if !hasSuffixRunes(line, first) {
			continue
		}
		start := len(line) - len(first)
		if i+len(patternLines)-1 >= len(fileLines) {
			continue
		}
		ok := true
		for k := 1; k < len(patternLines)-1 && ok; k++ {
			ok = slices.Equal(fileLines[i+k], patternLines[k])
		}
		end := i + len(patternLines) - 1
		if ok && hasPrefixRunes(fileLines[end], last) {
			matches = append(matches, match{i, start, end, len(last)})
		}
	}
	return matches
}

func hasPrefixRunes(line, prefix []rune) bool {
	return len(prefix) <= len(line) && slices.Equal(line[:len(prefix)], prefix)
}

func hasSuffixRunes(line, suffix []rune) bool {
	return len(suffix) <= len(line) && slices.Equal(line[len(line)-len(suffix):], suffix)
}

// foldRune maps the characters a model commonly types in place of the file's
// own to the ASCII the file most likely uses. It maps one rune to one rune.
func foldRune(r rune) rune {
	switch {
	case r == '‘' || r == '’' || r == '‚' || r == '‛':
		return '\''
	case r == '“' || r == '”' || r == '„' || r == '‟':
		return '"'
	case r >= '‐' && r <= '―', r == '−':
		return '-'
	case r == ' ', r >= ' ' && r <= ' ', r == ' ', r == ' ', r == '　':
		return ' '
	}
	return r
}
