// Package toolsteps says, in the owner's words, what one tool call of a turn
// did: a label for the action and a one-line detail of what it was asked
// for. The live status line and the reloaded chat history both read it, so
// a step reads the same while the turn runs and after it has finished.
package toolsteps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Step is one tool call as the owner sees it.
type Step struct {
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// For names one call: its label and the detail of its arguments.
func For(name string, arguments json.RawMessage) Step {
	return Step{Label: Label(name), Detail: Detail(arguments)}
}

// labels says what each tool is doing. A tool with no entry reads as "Using"
// plus its name spaced out, never as a bare identifier.
var labels = map[string]string{
	"web_search":          "Searching the web",
	"web_extract":         "Reading a web page",
	"current_time":        "Checking the time",
	"memory":              "Updating what I remember",
	"recall_conversation": "Looking back at earlier conversations",
	"read_file":           "Reading a file",
	"schedule":            "Looking at your schedules",
	"skill_read":          "Reading a skill",
	"finance":             "Checking your finances",
	"agent_runner":        "Checking on a coding run",
	"google_gmail":        "Checking email",
	"google_calendar":     "Checking your calendar",
	"google_drive":        "Looking in Drive",
	"google_docs":         "Reading a document",
	"google_sheets":       "Reading a spreadsheet",
	"google_contacts":     "Looking up a contact",
	"repository_github":   "Checking GitHub",
	"repository_list":     "Looking at repositories",
	"workspace_open":      "Opening a workspace",
	"workspace_close":     "Closing a workspace",
	"status":              "Checking how I'm running",
	// The web chat draws its image placeholder on this exact label.
	"generate_image": "Drawing an image",
}

// Label is what the tool called name is doing.
func Label(name string) string {
	if label, ok := labels[name]; ok {
		return label
	}
	return "Using " + strings.ReplaceAll(name, "_", " ")
}

// maxDetail bounds a detail: it is one line beside a label, not the call.
// The whole call is in the trace.
const maxDetail = 120

// Detail is the scalar arguments of a call, in the order the model wrote
// them, on one line. A call whose one argument is a string shows just its
// value ("soft boiled eggs" rather than "query: soft boiled eggs"); nested
// objects and arrays are left to the trace. Arguments that are not a JSON
// object have no detail.
func Detail(arguments json.RawMessage) string {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return ""
	}
	type pair struct {
		key, value string
		text       bool
	}
	var pairs []pair
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return ""
		}
		key, _ := keyToken.(string)
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return ""
		}
		if value, ok := scalar(raw); ok {
			pairs = append(pairs, pair{key, value, len(raw) > 0 && raw[0] == '"'})
		}
	}
	var line string
	switch {
	case len(pairs) == 0:
		return ""
	case len(pairs) == 1 && pairs[0].text:
		line = pairs[0].value
	default:
		parts := make([]string, len(pairs))
		for i, p := range pairs {
			parts[i] = p.key + ": " + p.value
		}
		line = strings.Join(parts, ", ")
	}
	return truncate(line)
}

// scalar renders a JSON string, number or boolean on one line; null, objects
// and arrays and empty strings are not shown.
func scalar(raw json.RawMessage) (string, bool) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", false
	}
	switch v := value.(type) {
	case string:
		text := strings.Join(strings.Fields(v), " ")
		return text, text != ""
	case json.Number:
		return v.String(), true
	case bool:
		return fmt.Sprint(v), true
	}
	return "", false
}

func truncate(line string) string {
	runes := []rune(line)
	if len(runes) <= maxDetail {
		return line
	}
	return strings.TrimSpace(string(runes[:maxDetail-1])) + "…"
}
