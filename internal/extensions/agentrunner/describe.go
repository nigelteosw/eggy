package agentrunner

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// DescribeCall says, for the approval card, what a cancel or an answer does to
// a coding run. Status and list read, and have no sentence, so the gate shows
// their arguments as before.
func (agentRunnerTool) DescribeCall(raw json.RawMessage) string {
	var args struct {
		Action string `json:"action"`
		ID     string `json:"id"`
		Text   string `json:"text"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return ""
	}
	switch args.Action {
	case "cancel":
		return fmt.Sprintf("Cancel the coding run %s. Its work so far stays where it is.", quoteRun(args.ID))
	case "answer":
		return fmt.Sprintf("Answer the coding run %s's question with %s.", quoteRun(args.ID), quoteRun(args.Text))
	}
	return ""
}

// quoteRun quotes a model-written value for the card and bounds it, so the
// text reads as the value it is.
func quoteRun(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `"(none)"`
	}
	if len(value) > 200 {
		value = value[:200] + "…"
	}
	return strconv.Quote(value)
}
