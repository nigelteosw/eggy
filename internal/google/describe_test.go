package google

import (
	"encoding/json"
	"strings"
	"testing"
)

// describe runs a tool's sentence for one call, the way the approval gate does.
func describe(t *testing.T, tool interface {
	DescribeCall(json.RawMessage) string
}, arguments string) string {
	t.Helper()
	return tool.DescribeCall(json.RawMessage(arguments))
}

func TestSendingAnEmailNamesItsRecipientAndSubject(t *testing.T) {
	got := describe(t, gmailTool{}, `{"action":"send","to":"sam@example.com","cc":"pat@example.com","subject":"Friday"}`)
	want := `Send an email to "sam@example.com", copying "pat@example.com", subject "Friday".`
	if got != want {
		t.Fatalf("sentence=%q, want %q", got, want)
	}
}

func TestADraftSaysNothingIsSent(t *testing.T) {
	got := describe(t, gmailTool{}, `{"action":"draft","to":"sam@example.com","subject":"Hi"}`)
	if !strings.Contains(got, "Nothing is sent.") {
		t.Fatalf("draft sentence does not say nothing is sent: %q", got)
	}
}

func TestAPermanentDriveDeleteSaysItCannotBeUndone(t *testing.T) {
	got := describe(t, driveTool{}, `{"action":"delete","file_id":"abc","permanent":true}`)
	if !strings.Contains(got, "cannot be undone") {
		t.Fatalf("permanent delete does not warn: %q", got)
	}
	trashed := describe(t, driveTool{}, `{"action":"delete","file_id":"abc"}`)
	if strings.Contains(trashed, "cannot be undone") || !strings.Contains(trashed, "trash") {
		t.Fatalf("ordinary delete should move to trash: %q", trashed)
	}
}

func TestSharingWithAnyoneNamesTheAudience(t *testing.T) {
	got := describe(t, driveTool{}, `{"action":"share","file_id":"abc","type":"anyone","role":"reader"}`)
	if !strings.Contains(got, "anyone with the link") {
		t.Fatalf("share sentence hides who gets access: %q", got)
	}
}

func TestACalendarInviteNamesTheGuests(t *testing.T) {
	got := describe(t, calendarTool{}, `{"action":"create","summary":"Standup","start":"2026-11-02T09:00","end":"2026-11-02T09:15","attendees":["sam@example.com"]}`)
	for _, want := range []string{`"Standup"`, `"sam@example.com"`, `"2026-11-02T09:00"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("sentence %q lacks %s", got, want)
		}
	}
}

// Values from the model are quoted and bounded, so a subject that reads like
// an instruction is shown as the quoted text it is, and a huge one is cut.
func TestValuesAreQuotedAndBounded(t *testing.T) {
	long := strings.Repeat("a", 500)
	got := describe(t, gmailTool{}, `{"action":"send","to":"x@example.com","subject":"`+long+`"}`)
	if len(got) > 300 || !strings.Contains(got, `"`) {
		t.Fatalf("long subject was not bounded and quoted: %d bytes %q", len(got), got)
	}
}

// Reads and unknown actions have no sentence, so the gate shows their arguments
// as it always has rather than a made-up description.
func TestReadsHaveNoSentence(t *testing.T) {
	if got := describe(t, gmailTool{}, `{"action":"search","query":"invoices"}`); got != "" {
		t.Fatalf("read has a sentence: %q", got)
	}
	if got := describe(t, gmailTool{}, `{"action":"nonsense"}`); got != "" {
		t.Fatalf("unknown action has a sentence: %q", got)
	}
}

// Every write the classification names has a sentence, so an action added to
// Mutations cannot reach the owner as raw arguments by accident.
func TestEveryWriteActionHasASentence(t *testing.T) {
	tools := map[string]interface {
		DescribeCall(json.RawMessage) string
	}{
		"google_gmail":    gmailTool{},
		"google_calendar": calendarTool{},
		"google_drive":    driveTool{},
		"google_docs":     docsTool{},
		"google_sheets":   sheetsTool{},
		"google_contacts": contactsTool{},
	}
	for name, actions := range Mutations() {
		for _, action := range actions {
			call, _ := json.Marshal(map[string]string{"action": action})
			if got := tools[name].DescribeCall(call); got == "" {
				t.Errorf("%s %s has no sentence", name, action)
			}
		}
	}
}

// Reclassification changes what the gate does, not what the call says, so the
// sentence reaches the card through the wrapper.
func TestReclassifiedToolKeepsItsSentence(t *testing.T) {
	tool := Reclassified(gmailTool{}, []string{"send"})
	describer, ok := tool.(interface {
		DescribeCall(json.RawMessage) string
	})
	if !ok {
		t.Fatal("reclassified tool does not describe its calls")
	}
	if got := describer.DescribeCall(json.RawMessage(`{"action":"send","to":"sam@example.com"}`)); !strings.Contains(got, "sam@example.com") {
		t.Fatalf("sentence lost in reclassification: %q", got)
	}
}
