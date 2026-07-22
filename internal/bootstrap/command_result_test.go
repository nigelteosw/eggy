package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/adapters/channels/telegram"
)

func TestRenderJSONProducesStableLowercaseFieldNames(t *testing.T) {
	result := CommandResult{
		State:  ResultSuccess,
		Title:  "Set provider deepseek.",
		Detail: "Restart Eggy for this to take effect.",
		Fields: []ResultField{{Label: "Provider", Value: "deepseek"}},
		Next:   []string{"/restart"},
	}
	body, err := result.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["state"] != "success" || decoded["title"] != "Set provider deepseek." {
		t.Fatalf("decoded=%#v", decoded)
	}
	fields, ok := decoded["fields"].([]any)
	if !ok || len(fields) != 1 {
		t.Fatalf("fields=%#v", decoded["fields"])
	}
	field, ok := fields[0].(map[string]any)
	if !ok || field["label"] != "Provider" || field["value"] != "deepseek" {
		t.Fatalf("field=%#v", field)
	}
	next, ok := decoded["next"].([]any)
	if !ok || len(next) != 1 || next[0] != "/restart" {
		t.Fatalf("next=%#v", decoded["next"])
	}
}

func TestRenderJSONOmitsEmptyFields(t *testing.T) {
	body, err := CommandResult{State: ResultInfo, Title: "No providers configured."}.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"detail", "fields", "table_headers", "table_rows", "lines", "next"} {
		if _, present := decoded[absent]; present {
			t.Fatalf("expected %q to be omitted, decoded=%#v", absent, decoded)
		}
	}
}

func TestRenderPlainTextIsCleanAndColourFree(t *testing.T) {
	result := CommandResult{
		State:  ResultError,
		Title:  "Repository not found",
		Detail: "eggy is not configured. Add it first.",
		Fields: []ResultField{{Label: "Repository", Value: "eggy"}},
		Next:   []string{"/repositories add eggy <clone_url>"},
	}
	text := result.RenderPlainText()
	if !strings.HasPrefix(text, "Error: Repository not found") {
		t.Fatalf("text=%q missing error marker", text)
	}
	if strings.ContainsRune(text, '\x1b') {
		t.Fatalf("text=%q contains an ANSI escape byte", text)
	}
	if !strings.Contains(text, "Repository: eggy") || !strings.Contains(text, "Next: /repositories add eggy <clone_url>") {
		t.Fatalf("text=%q missing fields/next", text)
	}
}

func TestRenderPlainTextFallbackHasNoMarkupForBareResult(t *testing.T) {
	result := CommandResult{Title: "Removed eggy."}
	text := result.RenderPlainText()
	if text != "Removed eggy." {
		t.Fatalf("text=%q, want plain title with no state marker or markup", text)
	}
}

func TestRenderPlainTextTableAligns(t *testing.T) {
	result := CommandResult{
		TableHeaders: []string{"ID", "Status"},
		TableRows:    [][]string{{"run-1", "running"}, {"run-2", "completed"}},
	}
	text := result.RenderPlainText()
	lines := strings.Split(text, "\n")
	if len(lines) != 3 {
		t.Fatalf("text=%q, want header + 2 rows", text)
	}
	if !strings.HasPrefix(lines[0], "ID") || !strings.Contains(lines[0], "Status") {
		t.Fatalf("header=%q", lines[0])
	}
}

func TestRenderPlainTextListsUseBulletPrefix(t *testing.T) {
	result := CommandResult{Lines: []string{"reviewer", "release-notes"}}
	text := result.RenderPlainText()
	if text != "- reviewer\n- release-notes" {
		t.Fatalf("text=%q", text)
	}
}

func TestRenderPlainTextHandlesLongOutputWithoutTruncating(t *testing.T) {
	long := strings.Repeat("implementation activity ", 500)
	result := CommandResult{Title: "Run log", Detail: long}
	text := result.RenderPlainText()
	if !strings.Contains(text, long) {
		t.Fatal("long detail was truncated or altered")
	}
}

func TestRenderMarkdownEscapesHTMLSensitiveCharactersEndToEnd(t *testing.T) {
	result := CommandResult{
		Title:  "Add request staged",
		Fields: []ResultField{{Label: "Clone URL", Value: "https://example.com/a&b<script>alert(1)</script>"}},
	}
	html := telegram.FormatHTML(result.RenderMarkdown())
	if strings.Contains(html, "<script>") {
		t.Fatalf("html=%q leaked unescaped script tag", html)
	}
	if !strings.Contains(html, "&amp;b&lt;script&gt;") {
		t.Fatalf("html=%q did not escape & and < from field value", html)
	}
}

func TestRenderMarkdownTableBecomesBulletsWithLinksPreserved(t *testing.T) {
	result := CommandResult{
		TableHeaders: []string{"Repository", "Link"},
		TableRows:    [][]string{{"eggy", "[docs](https://example.com/eggy)"}},
	}
	html := telegram.FormatHTML(result.RenderMarkdown())
	if !strings.Contains(html, "<b>eggy</b>") {
		t.Fatalf("html=%q missing bulleted first cell", html)
	}
	if !strings.Contains(html, `<a href="https://example.com/eggy">docs</a>`) {
		t.Fatalf("html=%q missing rendered link", html)
	}
}

func TestRenderMarkdownErrorTitleIsBoldWithMarker(t *testing.T) {
	result := CommandResult{State: ResultError, Title: "Repository not found"}
	html := telegram.FormatHTML(result.RenderMarkdown())
	if !strings.Contains(html, "<b>Error: Repository not found</b>") {
		t.Fatalf("html=%q missing bold error title", html)
	}
}

func TestRenderMarkdownNextCommandsRenderAsInlineCode(t *testing.T) {
	result := CommandResult{Title: "Stopped run-1.", Next: []string{"/status"}}
	html := telegram.FormatHTML(result.RenderMarkdown())
	if !strings.Contains(html, "<code>/status</code>") {
		t.Fatalf("html=%q missing inline-code next command", html)
	}
}
