package services

import (
	"encoding/json"
	"strings"
	"testing"
)

// describingTool is a tool that says what its own calls do, in the words the
// owner reads on the approval card.
type describingTool struct {
	recordingTool
	sentence string
}

func (t *describingTool) DescribeCall(json.RawMessage) string { return t.sentence }

// The sentence a tool writes for its call is what the owner approves, so it
// replaces the raw arguments on the card rather than sitting beside them.
func TestApprovalShowsTheToolsOwnSentence(t *testing.T) {
	service, _, _ := newGateFixture(t)
	inner := &describingTool{recordingTool: recordingTool{name: "google_gmail"}, sentence: "Send an email to sam@example.com, subject \"Friday\"."}
	gated := NewApprovalGatedTool(inner, service, service)

	if _, err := gated.Execute(asAccount("42"), json.RawMessage(`{"action":"send","to":"sam@example.com"}`)); err != nil {
		t.Fatal(err)
	}
	pending, err := service.Pending(asAccount("42"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Summary != inner.sentence {
		t.Fatalf("summary=%q, want the tool's sentence", pending[0].Summary)
	}
}

// A tool that does not describe itself keeps the old card, so MCP tools and
// anything unmigrated still show the owner exactly what would run.
func TestApprovalFallsBackToArgumentsForToolsWithoutASentence(t *testing.T) {
	service, _, gated := newGateFixture(t)
	if _, err := gated.Execute(asAccount("42"), json.RawMessage(`{"to":"someone@example.com"}`)); err != nil {
		t.Fatal(err)
	}
	pending, err := service.Pending(asAccount("42"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pending[0].Summary, "someone@example.com") {
		t.Fatalf("fallback summary dropped the arguments: %q", pending[0].Summary)
	}
}

// An empty sentence is no sentence: a describer that has nothing to say about
// a call must not put a blank approval in front of the owner.
func TestEmptySentenceFallsBackToArguments(t *testing.T) {
	service, _, _ := newGateFixture(t)
	inner := &describingTool{recordingTool: recordingTool{name: "mail__send_message"}, sentence: "   "}
	gated := NewApprovalGatedTool(inner, service, service)
	if _, err := gated.Execute(asAccount("42"), json.RawMessage(`{"to":"someone@example.com"}`)); err != nil {
		t.Fatal(err)
	}
	pending, err := service.Pending(asAccount("42"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pending[0].Summary, "someone@example.com") {
		t.Fatalf("blank sentence replaced the arguments: %q", pending[0].Summary)
	}
}

// A long sentence is still bounded, so a describer cannot flood the card.
func TestLongSentenceIsBounded(t *testing.T) {
	service, _, _ := newGateFixture(t)
	inner := &describingTool{recordingTool: recordingTool{name: "google_gmail"}, sentence: strings.Repeat("x", 2000)}
	gated := NewApprovalGatedTool(inner, service, service)
	if _, err := gated.Execute(asAccount("42"), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	pending, err := service.Pending(asAccount("42"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending[0].Summary) > 600 {
		t.Fatalf("summary is %d bytes", len(pending[0].Summary))
	}
}

// A schedule fires with nobody watching, so the card says what it will do and
// when, and a reminder says it is delivered word for word.
func TestScheduleSentenceSaysWhatAndWhen(t *testing.T) {
	tool := scheduleTool{}
	recurring := tool.DescribeCall(json.RawMessage(`{"action":"create","instruction":"Check the build","cron":"0 9 * * 1-5"}`))
	if !strings.Contains(recurring, `"Check the build"`) || !strings.Contains(recurring, "0 9 * * 1-5") {
		t.Fatalf("recurring sentence=%q", recurring)
	}
	once := tool.DescribeCall(json.RawMessage(`{"action":"create","kind":"reminder","instruction":"Call the dentist","at":"2026-11-02T09:00:00+08:00"}`))
	if !strings.Contains(once, "word for word") || !strings.Contains(once, "2026-11-02T09:00:00+08:00") {
		t.Fatalf("reminder sentence=%q", once)
	}
	cancel := tool.DescribeCall(json.RawMessage(`{"action":"cancel","id":"sch_1"}`))
	if !strings.Contains(cancel, `"sch_1"`) || !strings.Contains(cancel, "never runs again") {
		t.Fatalf("cancel sentence=%q", cancel)
	}
	if got := tool.DescribeCall(json.RawMessage(`{"action":"list"}`)); got != "" {
		t.Fatalf("list has a sentence: %q", got)
	}
}
