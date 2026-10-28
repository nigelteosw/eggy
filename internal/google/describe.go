package google

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Each Google tool says in its own words what a write does, so the owner who
// approves it reads "Send an email to sam@example.com" rather than a JSON blob.
// The sentence is written from the same arguments Execute decodes, and only
// write actions have one: a read is never shown as a decision, and an unknown
// action has no sentence so the gate falls back to the arguments.
//
// The values are the model's, so each is quoted and bounded. The card is what
// the owner trusts, and a value that reads as an instruction must not look like
// one.

// describeValue quotes a value for the card and bounds its length.
func describeValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "(none)"
	}
	if utf8.RuneCountInString(value) > 120 {
		runes := []rune(value)
		value = string(runes[:120]) + "…"
	}
	return strconv.Quote(value)
}

func describeList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, describeValue(value))
	}
	return strings.Join(quoted, ", ")
}

func (gmailTool) DescribeCall(raw json.RawMessage) string {
	var input struct {
		Action       string   `json:"action"`
		ID           string   `json:"id"`
		ThreadID     string   `json:"thread_id"`
		DraftID      string   `json:"draft_id"`
		To           string   `json:"to"`
		CC           string   `json:"cc"`
		Subject      string   `json:"subject"`
		AddLabels    []string `json:"add_labels"`
		RemoveLabels []string `json:"remove_labels"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	switch input.Action {
	case "send":
		return fmt.Sprintf("Send an email to %s%s, subject %s.", describeValue(input.To), describeCC(input.CC), describeValue(input.Subject))
	case "draft":
		return fmt.Sprintf("Save an email draft to %s%s, subject %s. Nothing is sent.", describeValue(input.To), describeCC(input.CC), describeValue(input.Subject))
	case "reply":
		return fmt.Sprintf("Reply to the email %s.", describeValue(input.ID))
	case "send_draft":
		return fmt.Sprintf("Send the email draft %s.", describeValue(cmpOr(input.DraftID, input.ID)))
	case "modify":
		return fmt.Sprintf("Change the labels on the email %s: add %s, remove %s.", describeValue(input.ID), describeList(input.AddLabels), describeList(input.RemoveLabels))
	case "trash":
		return fmt.Sprintf("Move the email %s to the trash.", describeValue(input.ID))
	}
	return ""
}

func describeCC(cc string) string {
	if strings.TrimSpace(cc) == "" {
		return ""
	}
	return ", copying " + describeValue(cc)
}

func cmpOr(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (calendarTool) DescribeCall(raw json.RawMessage) string {
	var input struct {
		Action      string    `json:"action"`
		CalendarID  string    `json:"calendar_id"`
		Start       string    `json:"start"`
		End         string    `json:"end"`
		Summary     string    `json:"summary"`
		Attendees   *[]string `json:"attendees"`
		EventID     string    `json:"event_id"`
		Response    string    `json:"response"`
		SendUpdates string    `json:"send_updates"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	when := ""
	if input.Start != "" {
		when = " from " + describeValue(input.Start)
		if input.End != "" {
			when += " to " + describeValue(input.End)
		}
	}
	guests := ""
	if input.Attendees != nil && len(*input.Attendees) > 0 {
		guests = fmt.Sprintf(", inviting %s", describeList(*input.Attendees))
	}
	switch input.Action {
	case "create":
		return fmt.Sprintf("Create the calendar event %s%s%s on %s.", describeValue(input.Summary), when, guests, describeValue(calendarOr(input.CalendarID)))
	case "update":
		return fmt.Sprintf("Change the calendar event %s%s%s.", describeValue(input.EventID), when, guests)
	case "delete":
		return fmt.Sprintf("Delete the calendar event %s.", describeValue(input.EventID))
	case "respond":
		return fmt.Sprintf("Reply %s to the calendar event %s.", describeValue(input.Response), describeValue(input.EventID))
	}
	return ""
}

func calendarOr(id string) string {
	if strings.TrimSpace(id) == "" {
		return "your primary calendar"
	}
	return id
}

func (driveTool) DescribeCall(raw json.RawMessage) string {
	var input struct {
		Action    string `json:"action"`
		FileID    string `json:"file_id"`
		Name      string `json:"name"`
		Folder    bool   `json:"folder"`
		Permanent bool   `json:"permanent"`
		Email     string `json:"email"`
		Domain    string `json:"domain"`
		Role      string `json:"role"`
		Type      string `json:"type"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	switch input.Action {
	case "create":
		kind := "file"
		if input.Folder {
			kind = "folder"
		}
		return fmt.Sprintf("Create a Drive %s named %s.", kind, describeValue(input.Name))
	case "update":
		return fmt.Sprintf("Change the Drive file %s, renaming it to %s.", describeValue(input.FileID), describeValue(input.Name))
	case "delete":
		if input.Permanent {
			return fmt.Sprintf("Permanently delete the Drive file %s. This cannot be undone.", describeValue(input.FileID))
		}
		return fmt.Sprintf("Move the Drive file %s to the trash.", describeValue(input.FileID))
	case "share":
		who := input.Email
		switch {
		case input.Type == "anyone":
			who = "anyone with the link"
		case input.Domain != "":
			who = "everyone at " + describeValue(input.Domain)
		default:
			who = describeValue(input.Email)
		}
		return fmt.Sprintf("Share the Drive file %s with %s as %s.", describeValue(input.FileID), who, describeValue(input.Role))
	}
	return ""
}

func (docsTool) DescribeCall(raw json.RawMessage) string {
	var input struct {
		Action     string `json:"action"`
		DocumentID string `json:"document_id"`
		Title      string `json:"title"`
		Find       string `json:"find"`
		Replace    string `json:"replace"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	switch input.Action {
	case "create":
		return fmt.Sprintf("Create a Google Doc titled %s.", describeValue(input.Title))
	case "append":
		return fmt.Sprintf("Add text to the end of the document %s.", describeValue(input.DocumentID))
	case "replace":
		return fmt.Sprintf("Replace %s with %s in the document %s.", describeValue(input.Find), describeValue(input.Replace), describeValue(input.DocumentID))
	}
	return ""
}

func (sheetsTool) DescribeCall(raw json.RawMessage) string {
	var input struct {
		Action        string `json:"action"`
		SpreadsheetID string `json:"spreadsheet_id"`
		Range         string `json:"range"`
		Title         string `json:"title"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	switch input.Action {
	case "create":
		return fmt.Sprintf("Create a spreadsheet titled %s.", describeValue(input.Title))
	case "add_sheet":
		return fmt.Sprintf("Add a tab named %s to the spreadsheet %s.", describeValue(input.Title), describeValue(input.SpreadsheetID))
	case "update":
		return fmt.Sprintf("Overwrite the cells %s in the spreadsheet %s.", describeValue(input.Range), describeValue(input.SpreadsheetID))
	case "append":
		return fmt.Sprintf("Add rows after %s in the spreadsheet %s.", describeValue(input.Range), describeValue(input.SpreadsheetID))
	case "clear":
		return fmt.Sprintf("Clear the cells %s in the spreadsheet %s.", describeValue(input.Range), describeValue(input.SpreadsheetID))
	}
	return ""
}

func (contactsTool) DescribeCall(raw json.RawMessage) string {
	var input struct {
		Action   string   `json:"action"`
		Resource string   `json:"resource"`
		Name     string   `json:"name"`
		Emails   []string `json:"emails"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &input) != nil {
		return ""
	}
	switch input.Action {
	case "create":
		return fmt.Sprintf("Add the contact %s with email %s.", describeValue(input.Name), describeList(input.Emails))
	case "update":
		return fmt.Sprintf("Change the contact %s to %s, with email %s.", describeValue(input.Resource), describeValue(input.Name), describeList(input.Emails))
	}
	return ""
}
