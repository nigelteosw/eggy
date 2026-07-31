package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// Tools returns one tool per product, and only for the products configured.
//
// A tool per product rather than a tool per operation is what keeps the
// per-turn schema cost close to the CLI shape Hermes gets for free: six
// schemas covering roughly twenty operations. An unconfigured product costs
// nothing at all, which is the core's standing rule.
func Tools(workspace *Workspace, products []string, now func() time.Time) []ports.Tool {
	if now == nil {
		now = time.Now
	}
	enabled := map[string]bool{}
	for _, product := range products {
		enabled[strings.ToLower(strings.TrimSpace(product))] = true
	}
	all := []ports.Tool{
		gmailTool{workspace: workspace},
		calendarTool{workspace: workspace, now: now},
		driveTool{workspace: workspace},
		docsTool{workspace: workspace},
		sheetsTool{workspace: workspace},
		contactsTool{workspace: workspace},
	}
	tools := make([]ports.Tool, 0, len(all))
	for _, tool := range all {
		if enabled[strings.TrimPrefix(tool.Definition().Name, "google_")] {
			tools = append(tools, tool)
		}
	}
	return tools
}

// result marshals a value for the model. Every tool returns JSON because a
// model reading a tool result wants fields it can quote identifiers out of,
// not prose it has to parse.
func result(value any) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// unauthorized turns the one recoverable failure into an instruction. Every
// product shares a grant, so every product fails the same way at once, and the
// repair is a single command.
func unauthorized(err error) error {
	if errors.Is(err, ErrNotAuthorized) {
		return fmt.Errorf("%w — run /google login", err)
	}
	return err
}

type gmailTool struct{ workspace *Workspace }

func (t gmailTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "google_gmail",
		Description: "Read and send mail. action=search takes Gmail query syntax (is:unread, from:, newer_than:7d, has:attachment) and returns headers only; action=get returns one message body by id; action=send needs to/subject/body; action=reply threads onto a message id; action=labels lists label ids; action=modify adds or removes labels.",
		Schema: json.RawMessage(`{"type":"object","properties":{
"action":{"type":"string","enum":["search","get","send","reply","labels","modify"]},
"query":{"type":"string"},"max":{"type":"integer"},"id":{"type":"string"},
"to":{"type":"string"},"cc":{"type":"string"},"subject":{"type":"string"},"body":{"type":"string"},
"html":{"type":"boolean"},"from":{"type":"string","description":"display name over the authenticated address, e.g. \"Eggy\" <owner@example.com>"},
"add_labels":{"type":"array","items":{"type":"string"}},"remove_labels":{"type":"array","items":{"type":"string"}}},
"required":["action"],"additionalProperties":false}`),
	}
}

func (t gmailTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Action       string   `json:"action"`
		Query        string   `json:"query"`
		Max          int      `json:"max"`
		ID           string   `json:"id"`
		To           string   `json:"to"`
		CC           string   `json:"cc"`
		Subject      string   `json:"subject"`
		Body         string   `json:"body"`
		HTML         bool     `json:"html"`
		From         string   `json:"from"`
		AddLabels    []string `json:"add_labels"`
		RemoveLabels []string `json:"remove_labels"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	switch input.Action {
	case "search":
		messages, err := t.workspace.GmailSearch(ctx, input.Query, input.Max)
		if err != nil {
			return nil, unauthorized(err)
		}
		return result(messages)
	case "get":
		message, err := t.workspace.GmailGet(ctx, input.ID)
		if err != nil {
			return nil, unauthorized(err)
		}
		return result(message)
	case "send":
		sent, err := t.workspace.GmailSend(ctx, Outgoing{To: input.To, CC: input.CC, Subject: input.Subject, Body: input.Body, HTML: input.HTML, From: input.From})
		if err != nil {
			return nil, unauthorized(err)
		}
		return result(sent)
	case "reply":
		sent, err := t.workspace.GmailReply(ctx, input.ID, input.Body, input.From)
		if err != nil {
			return nil, unauthorized(err)
		}
		return result(sent)
	case "labels":
		labels, err := t.workspace.GmailLabels(ctx)
		if err != nil {
			return nil, unauthorized(err)
		}
		return result(labels)
	case "modify":
		message, err := t.workspace.GmailModify(ctx, input.ID, input.AddLabels, input.RemoveLabels)
		if err != nil {
			return nil, unauthorized(err)
		}
		return result(message)
	default:
		return nil, fmt.Errorf("unknown gmail action %q", input.Action)
	}
}

type calendarTool struct {
	workspace *Workspace
	now       func() time.Time
}

func (t calendarTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "google_calendar",
		Description: "Read and change calendars. action=list reads every calendar the account can see over the next seven days by default, and each event names the calendar it came from; pass calendar_id to read one. action=calendars lists the available calendars and their ids. action=create needs summary, start and end; action=delete needs an event id. Every time must carry a timezone offset or Z — a bare datetime is read as UTC and lands hours away.",
		Schema: json.RawMessage(`{"type":"object","properties":{
"action":{"type":"string","enum":["list","calendars","create","delete"]},
"calendar_id":{"type":"string"},"start":{"type":"string"},"end":{"type":"string"},
"summary":{"type":"string"},"location":{"type":"string"},"description":{"type":"string"},
"attendees":{"type":"array","items":{"type":"string"}},"event_id":{"type":"string"}},
"required":["action"],"additionalProperties":false}`),
	}
}

func (t calendarTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Action      string   `json:"action"`
		CalendarID  string   `json:"calendar_id"`
		Start       string   `json:"start"`
		End         string   `json:"end"`
		Summary     string   `json:"summary"`
		Location    string   `json:"location"`
		Description string   `json:"description"`
		Attendees   []string `json:"attendees"`
		EventID     string   `json:"event_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	switch input.Action {
	case "list":
		events, err := t.workspace.CalendarList(ctx, input.CalendarID, input.Start, input.End, t.now())
		if err != nil {
			return nil, unauthorized(err)
		}
		return result(events)
	case "calendars":
		calendars, err := t.workspace.Calendars(ctx)
		if err != nil {
			return nil, unauthorized(err)
		}
		return result(calendars)
	case "create":
		event, err := t.workspace.CalendarCreate(ctx, NewEvent{
			CalendarID: input.CalendarID, Summary: input.Summary, Start: input.Start, End: input.End,
			Location: input.Location, Description: input.Description, Attendees: input.Attendees,
		})
		if err != nil {
			return nil, unauthorized(err)
		}
		return result(event)
	case "delete":
		if err := t.workspace.CalendarDelete(ctx, input.CalendarID, input.EventID); err != nil {
			return nil, unauthorized(err)
		}
		return result(map[string]string{"status": "deleted", "event_id": input.EventID})
	default:
		return nil, fmt.Errorf("unknown calendar action %q", input.Action)
	}
}

type driveTool struct{ workspace *Workspace }

func (t driveTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "google_drive",
		Description: "Search Drive by words in the file's full text. Set raw_query when passing a Drive query expression instead, such as mimeType='application/pdf'.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"max":{"type":"integer"},"raw_query":{"type":"boolean"}},"required":["query"],"additionalProperties":false}`),
	}
}

func (t driveTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Query    string `json:"query"`
		Max      int    `json:"max"`
		RawQuery bool   `json:"raw_query"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	files, err := t.workspace.DriveSearch(ctx, input.Query, input.Max, input.RawQuery)
	if err != nil {
		return nil, unauthorized(err)
	}
	return result(files)
}

type docsTool struct{ workspace *Workspace }

func (t docsTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "google_docs",
		Description: "Read a Google Doc's title and full text by document id. Find the id with google_drive first.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"document_id":{"type":"string"}},"required":["document_id"],"additionalProperties":false}`),
	}
}

func (t docsTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		DocumentID string `json:"document_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	document, err := t.workspace.DocsGet(ctx, input.DocumentID)
	if err != nil {
		return nil, unauthorized(err)
	}
	return result(document)
}

type sheetsTool struct{ workspace *Workspace }

func (t sheetsTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "google_sheets",
		Description: "Read and write spreadsheet cells. action=get reads an A1 range; action=update overwrites it; action=append adds rows after the last. Values are rows of cells, e.g. [[\"Name\",\"Score\"],[\"Alice\",95]].",
		Schema: json.RawMessage(`{"type":"object","properties":{
"action":{"type":"string","enum":["get","update","append"]},
"spreadsheet_id":{"type":"string"},"range":{"type":"string"},
"values":{"type":"array","items":{"type":"array","items":{}}}},
"required":["action","spreadsheet_id","range"],"additionalProperties":false}`),
	}
}

func (t sheetsTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Action        string  `json:"action"`
		SpreadsheetID string  `json:"spreadsheet_id"`
		Range         string  `json:"range"`
		Values        [][]any `json:"values"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	switch input.Action {
	case "get":
		values, err := t.workspace.SheetsGet(ctx, input.SpreadsheetID, input.Range)
		if err != nil {
			return nil, unauthorized(err)
		}
		return result(map[string]any{"values": values})
	case "update", "append":
		if len(input.Values) == 0 {
			return nil, errors.New("values are required")
		}
		write := t.workspace.SheetsUpdate
		if input.Action == "append" {
			write = t.workspace.SheetsAppend
		}
		cells, err := write(ctx, input.SpreadsheetID, input.Range, input.Values)
		if err != nil {
			return nil, unauthorized(err)
		}
		return result(map[string]any{"status": "ok", "updated_cells": cells})
	default:
		return nil, fmt.Errorf("unknown sheets action %q", input.Action)
	}
}

type contactsTool struct{ workspace *Workspace }

func (t contactsTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "google_contacts",
		Description: "List the owner's saved contacts with their email addresses and phone numbers.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"max":{"type":"integer"}},"additionalProperties":false}`),
	}
}

func (t contactsTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Max int `json:"max"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
	}
	contacts, err := t.workspace.ContactsList(ctx, input.Max)
	if err != nil {
		return nil, unauthorized(err)
	}
	return result(contacts)
}
