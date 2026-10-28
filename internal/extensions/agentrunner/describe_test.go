package agentrunner

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCancellingARunSaysWhatItLeavesBehind(t *testing.T) {
	got := agentRunnerTool{}.DescribeCall(json.RawMessage(`{"action":"cancel","id":"run-7"}`))
	if !strings.Contains(got, `"run-7"`) || !strings.Contains(got, "stays where it is") {
		t.Fatalf("cancel sentence=%q", got)
	}
}

func TestAnsweringARunQuotesTheAnswer(t *testing.T) {
	got := agentRunnerTool{}.DescribeCall(json.RawMessage(`{"action":"answer","id":"run-7","text":"use the staging branch"}`))
	if !strings.Contains(got, `"use the staging branch"`) {
		t.Fatalf("answer sentence=%q", got)
	}
}

func TestReadsHaveNoSentence(t *testing.T) {
	tool := agentRunnerTool{}
	for _, action := range []string{"status", "list", "steps"} {
		got := tool.DescribeCall(json.RawMessage(`{"action":"` + action + `"}`))
		if got != "" {
			t.Fatalf("%s has a sentence: %q", action, got)
		}
	}
}
