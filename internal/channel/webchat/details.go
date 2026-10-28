package webchat

import (
	"bytes"
	"encoding/json"
)

// maxApprovalDetails bounds the arguments shown on a card. The sentence is the
// decision; the details are for checking it, so a long body is cut, not sent in
// full to a phone screen.
const maxApprovalDetails = 2000

// approvalDetails lays out what an approval would run: the tool, then its
// arguments indented, so each value is on a line of its own. It reads the
// gated-call payload the approval already records; a payload it cannot read
// gives nothing, and the card shows the sentence alone.
func approvalDetails(payload json.RawMessage) string {
	var call struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(payload, &call) != nil || call.Tool == "" {
		return ""
	}
	var indented bytes.Buffer
	if len(call.Arguments) == 0 || json.Indent(&indented, call.Arguments, "", "  ") != nil {
		return call.Tool
	}
	text := indented.String()
	if len(text) > maxApprovalDetails {
		text = text[:maxApprovalDetails] + "\n…"
	}
	return call.Tool + "\n" + text
}
