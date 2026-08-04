package google

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

// Mutations names, per tool, the actions that change something outside Eggy.
//
// It lives beside the tools rather than in bootstrap because it is the same
// knowledge the switch statements below encode: whoever adds an action decides
// here whether it writes, in the file they are already editing. A read left out
// of this map costs nothing; a write left out is an ungated mutation, which is
// why the test asserts every action appears in exactly one of the two lists.
func Mutations() map[string][]string {
	return map[string][]string{
		"google_gmail":    {"send", "reply", "draft", "send_draft", "modify", "trash"},
		"google_calendar": {"create", "update", "delete", "respond"},
		"google_drive":    {"create", "update", "delete", "share"},
		"google_docs":     {"create", "append", "replace"},
		"google_sheets":   {"create", "add_sheet", "update", "append", "clear"},
		"google_contacts": {"create", "update"},
	}
}

// Reads is Mutations' complement: the actions that change nothing.
//
// It exists so that Actions can be derived from the two rather than written a
// third time. That is what makes the classification enforceable: a new action
// left out of both lists is missing from Actions, so the test comparing Actions
// against the schemas fails, and the author has to say which kind it is before
// it can ship. A hand-written Actions would have accepted a new write silently
// and left it ungated.
func Reads() map[string][]string {
	return map[string][]string{
		"google_gmail":    {"search", "get", "thread", "labels", "drafts", "attachment"},
		"google_calendar": {"list", "calendars", "get", "freebusy"},
		"google_drive":    {"search", "get"},
		"google_docs":     {"get"},
		"google_sheets":   {"info", "get"},
		"google_contacts": {"list", "search"},
	}
}

// Actions names every action each tool accepts, mutations included.
func Actions() map[string][]string {
	actions := map[string][]string{}
	for tool, reads := range Reads() {
		actions[tool] = slices.Clone(reads)
	}
	for tool, mutations := range Mutations() {
		actions[tool] = append(actions[tool], mutations...)
	}
	return actions
}

// GateAll is the action set for a tool that has no actions to choose between.
const GateAll = "*"

// GateFor builds the per-call test for one tool: does this call name one of
// the given actions?
//
// An unreadable or actionless payload is treated as gated. The failure modes
// are not symmetrical: gating a call that did not need it costs a prompt, while
// letting one through costs the mutation itself.
func GateFor(actions []string) func(json.RawMessage) bool {
	if len(actions) == 0 {
		return func(json.RawMessage) bool { return false }
	}
	gated := make(map[string]bool, len(actions))
	for _, action := range actions {
		if action == GateAll {
			return func(json.RawMessage) bool { return true }
		}
		gated[action] = true
	}
	return func(arguments json.RawMessage) bool {
		var call struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(arguments, &call); err != nil || call.Action == "" {
			return true
		}
		return gated[call.Action]
	}
}

// GateNotice tells the model which of a tool's actions will stop for approval,
// so it neither avoids the ungated ones nor retries a gated one while the owner
// is being asked. An empty result means the whole tool is gated and the
// standard notice already says everything there is to say.
func GateNotice(actions []string) string {
	if len(actions) == 0 || slices.Contains(actions, GateAll) {
		return ""
	}
	listed := slices.Clone(actions)
	slices.Sort(listed)
	return fmt.Sprintf(" These actions require the owner's approval: %s. Calling one asks them, and it runs only if they approve; the result then goes to the owner rather than back here, so do not call it again while waiting. Every other action on this tool runs normally.", strings.Join(listed, ", "))
}

// respond shapes both outcomes of one product call for the model, and takes
// them positionally so a case can forward a call directly:
// respond(w.GmailLabels(ctx)). Every tool returns JSON because a model reading
// a tool result wants fields it can quote identifiers out of, not prose it has
// to parse.
func respond[T any](value T, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, unauthorized(err)
	}
	return json.Marshal(value)
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
		Description: "Read and send mail. action=search takes Gmail query syntax (is:unread, from:, newer_than:7d, has:attachment) and returns headers only; action=get returns one message body by id, along with any attachments it carries; action=thread returns a whole conversation by thread_id; action=attachment fetches one by id and returns it only if it is text. action=send needs to/subject/body; action=draft writes the same mail without sending it, which is the safer way to offer one for review; action=drafts lists what is waiting and action=send_draft sends one by draft id; action=reply threads onto a message id; action=labels lists label ids; action=modify adds or removes them; action=trash moves a message to the trash, where Gmail keeps it for 30 days.",
		Schema: json.RawMessage(`{"type":"object","properties":{
"action":{"type":"string","enum":["search","get","thread","labels","drafts","attachment","send","reply","draft","send_draft","modify","trash"]},
"query":{"type":"string"},"max":{"type":"integer"},"id":{"type":"string"},
"thread_id":{"type":"string"},"draft_id":{"type":"string"},"attachment_id":{"type":"string"},
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
		ThreadID     string   `json:"thread_id"`
		DraftID      string   `json:"draft_id"`
		AttachmentID string   `json:"attachment_id"`
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
	outgoing := Outgoing{To: input.To, CC: input.CC, Subject: input.Subject, Body: input.Body, HTML: input.HTML, From: input.From}
	switch input.Action {
	case "search":
		return respond(t.workspace.GmailSearch(ctx, input.Query, input.Max))
	case "get":
		return respond(t.workspace.GmailGet(ctx, input.ID))
	case "thread":
		// Falling back to id lets "read that thread" work when the model has
		// only the message it came from, which is the usual case.
		return respond(t.workspace.GmailThread(ctx, cmp.Or(input.ThreadID, input.ID)))
	case "attachment":
		return respond(t.workspace.GmailAttachment(ctx, input.ID, input.AttachmentID))
	case "send":
		return respond(t.workspace.GmailSend(ctx, outgoing))
	case "reply":
		return respond(t.workspace.GmailReply(ctx, input.ID, input.Body, input.From))
	case "draft":
		return respond(t.workspace.GmailDraft(ctx, outgoing))
	case "drafts":
		return respond(t.workspace.GmailDrafts(ctx, input.Max))
	case "send_draft":
		return respond(t.workspace.GmailSendDraft(ctx, cmp.Or(input.DraftID, input.ID)))
	case "labels":
		return respond(t.workspace.GmailLabels(ctx))
	case "modify":
		return respond(t.workspace.GmailModify(ctx, input.ID, input.AddLabels, input.RemoveLabels))
	case "trash":
		return respond(t.workspace.GmailTrash(ctx, input.ID))
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
		Description: "Read and change calendars. action=list reads every calendar the account can see over the next seven days by default, and each event names the calendar it came from; pass calendar_id to read one. action=calendars lists the available calendars and their ids; action=get reads one event by id; action=freebusy returns the busy blocks across every calendar in one call, which is the way to find a free slot. action=create needs summary, start and end; action=update changes an existing event in place and is always right for moving one, because delete-and-recreate discards every guest's reply; action=respond answers an invitation with yes, no or maybe; action=delete needs an event id. Every time must carry a timezone offset or Z — a bare datetime is read as UTC and lands hours away. Guests are emailed about events that have attendees; pass send_updates=none to change nothing but the owner's own calendar.",
		Schema: json.RawMessage(`{"type":"object","properties":{
"action":{"type":"string","enum":["list","calendars","get","freebusy","create","update","delete","respond"]},
"calendar_id":{"type":"string"},"calendar_ids":{"type":"array","items":{"type":"string"},"description":"freebusy only; omit for every calendar"},
"start":{"type":"string"},"end":{"type":"string"},
"summary":{"type":"string"},"location":{"type":"string"},"description":{"type":"string"},
"attendees":{"type":"array","items":{"type":"string"},"description":"on update this replaces the whole guest list; omit it to leave the guests alone"},
"event_id":{"type":"string"},"response":{"type":"string","enum":["yes","no","maybe"]},
"send_updates":{"type":"string","enum":["all","externalOnly","none"],"description":"who to notify; defaults to all when the event has attendees"}},
"required":["action"],"additionalProperties":false}`),
	}
}

func (t calendarTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Action      string    `json:"action"`
		CalendarID  string    `json:"calendar_id"`
		CalendarIDs []string  `json:"calendar_ids"`
		Start       string    `json:"start"`
		End         string    `json:"end"`
		Summary     string    `json:"summary"`
		Location    string    `json:"location"`
		Description string    `json:"description"`
		Attendees   *[]string `json:"attendees"`
		EventID     string    `json:"event_id"`
		Response    string    `json:"response"`
		SendUpdates string    `json:"send_updates"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	// A guest list that was never mentioned and one that was set to empty are
	// different instructions to update, so the pointer survives to it and is
	// flattened only for the actions that cannot tell them apart anyway.
	var attendees []string
	if input.Attendees != nil {
		attendees = *input.Attendees
	}
	switch input.Action {
	case "list":
		return respond(t.workspace.CalendarList(ctx, input.CalendarID, input.Start, input.End, t.now()))
	case "calendars":
		return respond(t.workspace.Calendars(ctx))
	case "get":
		return respond(t.workspace.CalendarGet(ctx, input.CalendarID, input.EventID))
	case "freebusy":
		return respond(t.workspace.CalendarFreeBusy(ctx, input.CalendarIDs, input.Start, input.End, t.now()))
	case "update":
		return respond(t.workspace.CalendarUpdate(ctx, EventChange{
			CalendarID: input.CalendarID, EventID: input.EventID, Summary: input.Summary,
			Start: input.Start, End: input.End, Location: input.Location, Description: input.Description,
			Attendees: input.Attendees, SendUpdates: input.SendUpdates,
		}))
	case "respond":
		return respond(t.workspace.CalendarRespond(ctx, input.CalendarID, input.EventID, input.Response))
	case "create":
		return respond(t.workspace.CalendarCreate(ctx, NewEvent{
			CalendarID: input.CalendarID, Summary: input.Summary, Start: input.Start, End: input.End,
			Location: input.Location, Description: input.Description, Attendees: attendees,
			SendUpdates: input.SendUpdates,
		}))
	case "delete":
		// The only action with nothing to report back, so it states the
		// outcome rather than returning an empty body the model has to guess at.
		err := t.workspace.CalendarDelete(ctx, input.CalendarID, input.EventID, input.SendUpdates)
		return respond(map[string]string{"status": "deleted", "event_id": input.EventID}, err)
	default:
		return nil, fmt.Errorf("unknown calendar action %q", input.Action)
	}
}

type driveTool struct{ workspace *Workspace }

func (t driveTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "google_drive",
		Description: "Find and manage files. action=search matches words in a file's full text; set raw_query to pass a Drive query expression instead, such as mimeType='application/pdf' or '<folder id>' in parents. action=get reads a file's content — Docs and Sheets are exported to text and CSV, plain text files are returned as-is, and anything else reports what it is rather than returning bytes. action=create writes a text file, or a folder with folder=true; action=update renames a file, moves it by naming a new parent, or both; action=delete puts it in the trash unless permanent=true; action=share grants access to an email address, a domain, or to anyone with the link.",
		Schema: json.RawMessage(`{"type":"object","properties":{
"action":{"type":"string","enum":["search","get","create","update","delete","share"]},
"query":{"type":"string"},"max":{"type":"integer"},"raw_query":{"type":"boolean"},
"file_id":{"type":"string"},"name":{"type":"string"},"content":{"type":"string"},
"mime_type":{"type":"string"},"parent":{"type":"string","description":"folder id; on update this moves the file there"},
"folder":{"type":"boolean"},"permanent":{"type":"boolean","description":"delete outright instead of trashing; cannot be undone"},
"email":{"type":"string"},"domain":{"type":"string"},
"role":{"type":"string","enum":["reader","commenter","writer"]},
"type":{"type":"string","enum":["user","group","domain","anyone"],"description":"anyone means a public link"},
"notify":{"type":"boolean"},"message":{"type":"string"}},
"required":["action"],"additionalProperties":false}`),
	}
}

func (t driveTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Action    string `json:"action"`
		Query     string `json:"query"`
		Max       int    `json:"max"`
		RawQuery  bool   `json:"raw_query"`
		FileID    string `json:"file_id"`
		Name      string `json:"name"`
		Content   string `json:"content"`
		MimeType  string `json:"mime_type"`
		Parent    string `json:"parent"`
		Folder    bool   `json:"folder"`
		Permanent bool   `json:"permanent"`
		Email     string `json:"email"`
		Domain    string `json:"domain"`
		Role      string `json:"role"`
		Type      string `json:"type"`
		Notify    bool   `json:"notify"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	switch input.Action {
	case "search":
		return respond(t.workspace.DriveSearch(ctx, input.Query, input.Max, input.RawQuery))
	case "get":
		return respond(t.workspace.DriveGet(ctx, input.FileID))
	case "create":
		return respond(t.workspace.DriveCreate(ctx, NewFile{
			Name: input.Name, MimeType: input.MimeType, Content: input.Content,
			Parent: input.Parent, Folder: input.Folder,
		}))
	case "update":
		return respond(t.workspace.DriveUpdate(ctx, FileChange{ID: input.FileID, Name: input.Name, Parent: input.Parent}))
	case "delete":
		return respond(t.workspace.DriveDelete(ctx, input.FileID, input.Permanent))
	case "share":
		return respond(t.workspace.DriveShare(ctx, Share{
			FileID: input.FileID, Email: input.Email, Domain: input.Domain,
			Role: input.Role, Type: input.Type, Notify: input.Notify, Message: input.Message,
		}))
	default:
		return nil, fmt.Errorf("unknown drive action %q", input.Action)
	}
}

type docsTool struct{ workspace *Workspace }

func (t docsTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "google_docs",
		Description: "Read and write Google Docs. action=get returns a document's title and full text by id — find the id with google_drive first. action=create makes a new one from a title and optional body text; action=append adds text at the end; action=replace substitutes every occurrence of one string and reports how many it changed, so zero means nothing matched. This handles text, not formatting.",
		Schema: json.RawMessage(`{"type":"object","properties":{
"action":{"type":"string","enum":["get","create","append","replace"]},
"document_id":{"type":"string"},"title":{"type":"string"},"text":{"type":"string"},
"find":{"type":"string"},"replace":{"type":"string"},"match_case":{"type":"boolean"}},
"required":["action"],"additionalProperties":false}`),
	}
}

func (t docsTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Action     string `json:"action"`
		DocumentID string `json:"document_id"`
		Title      string `json:"title"`
		Text       string `json:"text"`
		Find       string `json:"find"`
		Replace    string `json:"replace"`
		MatchCase  bool   `json:"match_case"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	switch input.Action {
	case "get":
		return respond(t.workspace.DocsGet(ctx, input.DocumentID))
	case "create":
		return respond(t.workspace.DocsCreate(ctx, input.Title, input.Text))
	case "append":
		return respond(t.workspace.DocsAppend(ctx, input.DocumentID, input.Text))
	case "replace":
		return respond(t.workspace.DocsReplace(ctx, input.DocumentID, input.Find, input.Replace, input.MatchCase))
	default:
		return nil, fmt.Errorf("unknown docs action %q", input.Action)
	}
}

type sheetsTool struct{ workspace *Workspace }

func (t sheetsTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "google_sheets",
		Description: "Read and write spreadsheet cells. action=info lists the workbook's tab names and sizes and takes no range — call it first, because a tab is not always called Sheet1 and an A1 range naming the wrong one fails. action=get reads an A1 range; action=update overwrites it; action=append adds rows after the last; action=clear empties a range but leaves its formatting. action=create makes a new workbook and action=add_sheet adds a tab to one. Values are rows of cells, e.g. [[\"Name\",\"Score\"],[\"Alice\",95]].",
		Schema: json.RawMessage(`{"type":"object","properties":{
"action":{"type":"string","enum":["info","get","create","add_sheet","update","append","clear"]},
"spreadsheet_id":{"type":"string"},"range":{"type":"string"},
"title":{"type":"string","description":"workbook title on create, tab title on add_sheet"},
"sheet_name":{"type":"string","description":"names the first tab of a new workbook"},
"values":{"type":"array","items":{"type":"array","items":{}}}},
"required":["action"],"additionalProperties":false}`),
	}
}

func (t sheetsTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Action        string  `json:"action"`
		SpreadsheetID string  `json:"spreadsheet_id"`
		Range         string  `json:"range"`
		Title         string  `json:"title"`
		SheetName     string  `json:"sheet_name"`
		Values        [][]any `json:"values"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	// Only the value actions address a range, so they check for one rather
	// than sending Google a URL with an empty path segment.
	needsRange := []string{"get", "update", "append", "clear"}
	if slices.Contains(needsRange, input.Action) && strings.TrimSpace(input.Range) == "" {
		return nil, fmt.Errorf("a range is required for %s; call action=info for the tab names", input.Action)
	}
	switch input.Action {
	case "info":
		return respond(t.workspace.SheetsInfo(ctx, input.SpreadsheetID))
	case "create":
		return respond(t.workspace.SheetsCreate(ctx, input.Title, input.SheetName))
	case "add_sheet":
		return respond(t.workspace.SheetsAddSheet(ctx, input.SpreadsheetID, input.Title))
	case "clear":
		return respond(t.workspace.SheetsClear(ctx, input.SpreadsheetID, input.Range))
	case "get":
		values, err := t.workspace.SheetsGet(ctx, input.SpreadsheetID, input.Range)
		return respond(map[string]any{"values": values}, err)
	case "update", "append":
		if len(input.Values) == 0 {
			return nil, errors.New("values are required")
		}
		write := t.workspace.SheetsUpdate
		if input.Action == "append" {
			write = t.workspace.SheetsAppend
		}
		cells, err := write(ctx, input.SpreadsheetID, input.Range, input.Values)
		return respond(map[string]any{"status": "ok", "updated_cells": cells}, err)
	default:
		return nil, fmt.Errorf("unknown sheets action %q", input.Action)
	}
}

type contactsTool struct{ workspace *Workspace }

func (t contactsTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "google_contacts",
		Description: "The owner's saved contacts, with email addresses and phone numbers. action=search finds one by name, email or phone and is the right call when looking someone up; action=list returns the first few and is only useful for browsing. action=create saves a new contact; action=update changes one, addressed by the resource name a search returns. An update replaces the fields it is given and leaves the others alone.",
		Schema: json.RawMessage(`{"type":"object","properties":{
"action":{"type":"string","enum":["list","search","create","update"]},
"query":{"type":"string"},"max":{"type":"integer"},
"resource":{"type":"string","description":"as returned by search, e.g. people/c123"},
"name":{"type":"string"},
"emails":{"type":"array","items":{"type":"string"}},
"phones":{"type":"array","items":{"type":"string"}}},
"required":["action"],"additionalProperties":false}`),
	}
}

func (t contactsTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Action   string   `json:"action"`
		Query    string   `json:"query"`
		Max      int      `json:"max"`
		Resource string   `json:"resource"`
		Name     string   `json:"name"`
		Emails   []string `json:"emails"`
		Phones   []string `json:"phones"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
	}
	contact := Contact{Name: input.Name, Emails: input.Emails, Phones: input.Phones}
	switch input.Action {
	// A bare call with no action stays a list, which is what it did before
	// contacts had actions at all.
	case "list", "":
		return respond(t.workspace.ContactsList(ctx, input.Max))
	case "search":
		return respond(t.workspace.ContactsSearch(ctx, input.Query, input.Max))
	case "create":
		return respond(t.workspace.ContactsCreate(ctx, contact))
	case "update":
		return respond(t.workspace.ContactsUpdate(ctx, input.Resource, contact))
	default:
		return nil, fmt.Errorf("unknown contacts action %q", input.Action)
	}
}
