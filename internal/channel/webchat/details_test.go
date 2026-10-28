package webchat

import (
	"encoding/json"
	"strings"
	"testing"
)

// The details are what the owner can open to check a sentence against: the
// tool and its arguments, laid out so each value can be read on its own line.
func TestApprovalDetailsShowToolAndReadableArguments(t *testing.T) {
	payload := json.RawMessage(`{"tool":"google_gmail","arguments":{"action":"send","to":"sam@example.com"}}`)
	got := approvalDetails(payload)
	if !strings.HasPrefix(got, "google_gmail\n") {
		t.Fatalf("details do not name the tool first: %q", got)
	}
	if !strings.Contains(got, "\"to\": \"sam@example.com\"") {
		t.Fatalf("arguments are not laid out one per line: %q", got)
	}
}

// Arguments are bounded, so a long body cannot push the card off the screen.
func TestApprovalDetailsAreBounded(t *testing.T) {
	long := strings.Repeat("a", 5000)
	got := approvalDetails(json.RawMessage(`{"tool":"x","arguments":{"body":"` + long + `"}}`))
	if len(got) > 2100 {
		t.Fatalf("details are %d bytes", len(got))
	}
}

// A payload that does not decode gives no details rather than a broken card.
func TestUnreadableApprovalPayloadHasNoDetails(t *testing.T) {
	if got := approvalDetails(json.RawMessage(`not json`)); got != "" {
		t.Fatalf("details=%q", got)
	}
}
