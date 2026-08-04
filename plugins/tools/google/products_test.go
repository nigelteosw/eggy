package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// request is one call the fake API saw, kept because most of these tests are
// about what Eggy sent rather than what it did with the answer.
type request struct {
	Method string
	Path   string
	Query  url.Values
	Body   string
}

// recordingAPI answers from a table of path suffixes and records everything.
type recordingAPI struct {
	responses map[string]string
	seen      []request
}

func (a *recordingAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	a.seen = append(a.seen, request{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Body: string(body)})
	for suffix, response := range a.responses {
		if strings.HasSuffix(r.URL.Path, suffix) {
			_, _ = w.Write([]byte(response))
			return
		}
	}
	_, _ = w.Write([]byte(`{}`))
}

func (a *recordingAPI) lastTo(t *testing.T, suffix string) request {
	t.Helper()
	for index := len(a.seen) - 1; index >= 0; index-- {
		if strings.HasSuffix(a.seen[index].Path, suffix) {
			return a.seen[index]
		}
	}
	t.Fatalf("no request to %q; saw %+v", suffix, a.seen)
	return request{}
}

func recordingWorkspace(t *testing.T, responses map[string]string) (*Workspace, *recordingAPI) {
	t.Helper()
	api := &recordingAPI{responses: responses}
	return authorizedWorkspace(t, api), api
}

// Patching attendees replaces the whole array, so an RSVP that sends only the
// owner's entry uninvites everyone else -- a mistake nobody notices until the
// meeting is empty.
func TestRespondingToAnInviteKeepsTheOtherGuests(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{
		"/events/e1": `{"id":"e1","summary":"Review","attendees":[
{"email":"boss@example.com","responseStatus":"accepted"},
{"email":"owner@example.com","self":true,"responseStatus":"needsAction"},
{"email":"peer@example.com","optional":true,"responseStatus":"declined"}]}`,
	})
	if _, err := workspace.CalendarRespond(context.Background(), "", "e1", "yes"); err != nil {
		t.Fatal(err)
	}
	patch := api.lastTo(t, "/events/e1")
	if patch.Method != http.MethodPatch {
		t.Fatalf("method=%s, want a partial update", patch.Method)
	}
	var sent struct {
		Attendees []struct {
			Email          string `json:"email"`
			Optional       bool   `json:"optional"`
			ResponseStatus string `json:"responseStatus"`
		} `json:"attendees"`
	}
	if err := json.Unmarshal([]byte(patch.Body), &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent.Attendees) != 3 {
		t.Fatalf("sent %d attendees, want every one of them back: %s", len(sent.Attendees), patch.Body)
	}
	for _, attendee := range sent.Attendees {
		switch attendee.Email {
		case "owner@example.com":
			if attendee.ResponseStatus != "accepted" {
				t.Fatalf("the owner's own reply was not set: %s", patch.Body)
			}
		case "boss@example.com":
			if attendee.ResponseStatus != "accepted" {
				t.Fatalf("another guest's reply was reset: %s", patch.Body)
			}
		case "peer@example.com":
			if attendee.ResponseStatus != "declined" || !attendee.Optional {
				t.Fatalf("an optional guest lost their reply or their status: %s", patch.Body)
			}
		}
	}
}

func TestRespondingWhenNotAGuestChangesNothing(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{
		"/events/e1": `{"id":"e1","attendees":[{"email":"someone@example.com"}]}`,
	})
	if _, err := workspace.CalendarRespond(context.Background(), "", "e1", "yes"); err == nil {
		t.Fatal("responded to an event this account is not invited to")
	}
	for _, seen := range api.seen {
		if seen.Method == http.MethodPatch {
			t.Fatalf("an event was modified anyway: %+v", seen)
		}
	}
}

// Update is a patch, and the fields nobody mentioned must not appear in it --
// sending an empty attendees array would clear the guest list.
func TestUpdatingAnEventSendsOnlyWhatChanged(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{
		"/events/e1": `{"id":"e1","summary":"Review","attendees":[{"email":"guest@example.com"}]}`,
	})
	_, err := workspace.CalendarUpdate(context.Background(), EventChange{
		EventID: "e1", Start: "2026-08-01T15:00:00Z", End: "2026-08-01T16:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	patch := api.lastTo(t, "/events/e1")
	if patch.Method != http.MethodPatch {
		t.Fatalf("method=%s", patch.Method)
	}
	if strings.Contains(patch.Body, "attendees") || strings.Contains(patch.Body, "summary") {
		t.Fatalf("a partial update carried fields nobody set: %s", patch.Body)
	}
	// The event already had a guest, so moving it has to tell them even though
	// the caller said nothing about attendees.
	if patch.Query.Get("sendUpdates") != "all" {
		t.Fatalf("sendUpdates=%q, want the existing guests told the meeting moved", patch.Query.Get("sendUpdates"))
	}
}

// An explicitly empty guest list is a request to remove everyone, and it is
// the one case that must survive the round trip as an empty array.
func TestUpdatingCanClearTheGuestList(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{"/events/e1": `{"id":"e1"}`})
	empty := []string{}
	if _, err := workspace.CalendarUpdate(context.Background(), EventChange{EventID: "e1", Attendees: &empty}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(api.lastTo(t, "/events/e1").Body, `"attendees":[]`) {
		t.Fatalf("body=%s", api.lastTo(t, "/events/e1").Body)
	}
}

func TestFreeBusyAsksOneQuestionAcrossEveryCalendar(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{
		"/users/me/calendarList": `{"items":[{"id":"work@example.com"},{"id":"home@example.com"}]}`,
		"/freeBusy": `{"calendars":{
"work@example.com":{"busy":[{"start":"2026-08-01T14:00:00Z","end":"2026-08-01T15:00:00Z"}]},
"home@example.com":{"busy":[{"start":"2026-08-01T09:00:00Z","end":"2026-08-01T10:00:00Z"}]}}}`,
	})
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	busy, err := workspace.CalendarFreeBusy(context.Background(), nil, "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	// Merged across calendars, the blocks arrive in whatever order the map
	// iterated; a schedule reported out of order is read out of order.
	if len(busy) != 2 || busy[0].Calendar != "home@example.com" {
		t.Fatalf("busy=%#v", busy)
	}
	freeBusy := api.lastTo(t, "/freeBusy")
	if !strings.Contains(freeBusy.Body, "work@example.com") || !strings.Contains(freeBusy.Body, "home@example.com") {
		t.Fatalf("freebusy asked about %s", freeBusy.Body)
	}
}

// A Doc has no bytes to download and a text file has nothing to export, so the
// metadata lookup that decides between them is not optional.
func TestDriveGetExportsNativeFilesAndDownloadsTheRest(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{
		"/files/doc1":        `{"id":"doc1","name":"Notes","mimeType":"application/vnd.google-apps.document"}`,
		"/files/doc1/export": "the document text",
	})
	content, err := workspace.DriveGet(context.Background(), "doc1")
	if err != nil {
		t.Fatal(err)
	}
	if content.Text != "the document text" || content.Exported != "text/plain" {
		t.Fatalf("content=%#v", content)
	}
	if got := api.lastTo(t, "/export").Query.Get("mimeType"); got != "text/plain" {
		t.Fatalf("exported as %q, want the one format a model can read", got)
	}

	workspace, api = recordingWorkspace(t, map[string]string{
		"/files/f2": `{"id":"f2","name":"notes.txt","mimeType":"text/plain"}`,
	})
	if _, err := workspace.DriveGet(context.Background(), "f2"); err != nil {
		t.Fatal(err)
	}
	if got := api.lastTo(t, "/files/f2").Query.Get("alt"); got != "media" {
		t.Fatalf("a plain text file was not downloaded as-is: alt=%q", got)
	}
}

// Returning base64 of a PDF spends the whole turn's context on something the
// model cannot read, so it is described instead.
func TestDriveGetDescribesBinaryRatherThanReturningIt(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{
		"/files/f3": `{"id":"f3","name":"report.pdf","mimeType":"application/pdf","webViewLink":"https://drive/f3"}`,
	})
	content, err := workspace.DriveGet(context.Background(), "f3")
	if err != nil {
		t.Fatal(err)
	}
	if content.Text != "" || !strings.Contains(content.Note, "application/pdf") {
		t.Fatalf("content=%#v", content)
	}
	if content.File.Link == "" {
		t.Fatal("a file that cannot be read must still be reachable")
	}
	for _, seen := range api.seen {
		if seen.Query.Get("alt") == "media" {
			t.Fatal("the bytes were fetched anyway")
		}
	}
}

// Drive has no move: a file's location is its parent list, and adding a parent
// without removing the old one leaves the file in two places.
func TestMovingAFileRemovesTheOldParent(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{
		"/files/f1": `{"id":"f1","name":"notes","parents":["old-folder"]}`,
	})
	if _, err := workspace.DriveUpdate(context.Background(), FileChange{ID: "f1", Parent: "new-folder"}); err != nil {
		t.Fatal(err)
	}
	patch := api.lastTo(t, "/files/f1")
	if patch.Query.Get("addParents") != "new-folder" || patch.Query.Get("removeParents") != "old-folder" {
		t.Fatalf("query=%v", patch.Query)
	}
}

func TestDeletingAFileTrashesItUnlessToldOtherwise(t *testing.T) {
	workspace, api := recordingWorkspace(t, nil)
	result, err := workspace.DriveDelete(context.Background(), "f1", false)
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "trashed" || api.lastTo(t, "/files/f1").Method != http.MethodPatch {
		t.Fatalf("result=%v method=%s", result, api.lastTo(t, "/files/f1").Method)
	}

	result, err = workspace.DriveDelete(context.Background(), "f1", true)
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "deleted" || api.lastTo(t, "/files/f1").Method != http.MethodDelete {
		t.Fatalf("result=%v method=%s", result, api.lastTo(t, "/files/f1").Method)
	}
}

// A public link cannot be walked back by asking the recipient nicely, so the
// answer has to say plainly what just happened.
func TestSharingWithAnyoneSaysSo(t *testing.T) {
	workspace, _ := recordingWorkspace(t, map[string]string{"/permissions": `{"id":"p1"}`})
	result, err := workspace.DriveShare(context.Background(), Share{FileID: "f1", Type: "anyone", Role: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	if result["warning"] == nil {
		t.Fatalf("a public link was created without saying so: %v", result)
	}
	if _, err := workspace.DriveShare(context.Background(), Share{FileID: "f1", Type: "user"}); err == nil {
		t.Fatal("shared with a user without an address")
	}
}

func TestCreatingAFileUploadsItAsMultipart(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{"/files": `{"id":"f9","name":"notes.txt"}`})
	if _, err := workspace.DriveCreate(context.Background(), NewFile{Name: "notes.txt", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	upload := api.lastTo(t, "/files")
	if upload.Query.Get("uploadType") != "multipart" {
		t.Fatalf("uploadType=%q", upload.Query.Get("uploadType"))
	}
	// Metadata first, content second: Drive reads the parts positionally.
	if !strings.Contains(upload.Body, `"name":"notes.txt"`) || !strings.Contains(upload.Body, "hello") {
		t.Fatalf("body=%s", upload.Body)
	}
	if strings.Index(upload.Body, `"name":"notes.txt"`) > strings.Index(upload.Body, "hello") {
		t.Fatalf("the parts are the wrong way round: %s", upload.Body)
	}

	// A folder has no content, so it is a metadata create rather than an
	// upload with an empty second part.
	workspace, api = recordingWorkspace(t, map[string]string{"/files": `{"id":"f10","name":"Reports"}`})
	if _, err := workspace.DriveCreate(context.Background(), NewFile{Name: "Reports", Folder: true}); err != nil {
		t.Fatal(err)
	}
	if api.lastTo(t, "/files").Query.Has("uploadType") {
		t.Fatal("a folder was created through the upload endpoint")
	}
}

// Hermes reads the whole document to compute the final index, minus one for
// the newline Docs keeps last. endOfSegmentLocation does it in one call.
func TestAppendingToADocTakesOneRequest(t *testing.T) {
	workspace, api := recordingWorkspace(t, nil)
	if _, err := workspace.DocsAppend(context.Background(), "d1", "more text"); err != nil {
		t.Fatal(err)
	}
	if len(api.seen) != 1 {
		t.Fatalf("appending took %d requests: %+v", len(api.seen), api.seen)
	}
	body := api.seen[0].Body
	if !strings.Contains(body, "endOfSegmentLocation") || strings.Contains(body, `"index"`) {
		t.Fatalf("body=%s", body)
	}
	// Without the break, appending twice runs the two together on one line.
	if !strings.Contains(body, `more text\n`) {
		t.Fatalf("appended text carries no paragraph break: %s", body)
	}
}

// A replace that matched nothing and one that changed the document both
// return success, so the count is the only thing that tells them apart.
func TestReplacingNothingSaysSo(t *testing.T) {
	workspace, _ := recordingWorkspace(t, map[string]string{
		":batchUpdate": `{"replies":[{"replaceAllText":{"occurrencesChanged":0}}]}`,
	})
	result, err := workspace.DocsReplace(context.Background(), "d1", "missing", "new", false)
	if err != nil {
		t.Fatal(err)
	}
	if result["occurrences_changed"] != 0 || result["note"] == nil {
		t.Fatalf("result=%v", result)
	}
}

// searchContacts reads a server-side cache that Google requires be warmed
// first, and the warmup is worth paying for once rather than once per search.
func TestContactSearchWarmsTheCacheOnceThenSearches(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{
		":searchContacts": `{"results":[{"person":{"resourceName":"people/c1","names":[{"displayName":"Sam"}],"emailAddresses":[{"value":"sam@example.com"}]}}]}`,
	})
	contacts, err := workspace.ContactsSearch(context.Background(), "sam", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 1 || contacts[0].Name != "Sam" || contacts[0].Resource != "people/c1" {
		t.Fatalf("contacts=%#v", contacts)
	}
	if len(api.seen) != 2 {
		t.Fatalf("expected a warmup then a search, saw %d requests", len(api.seen))
	}
	if api.seen[0].Query.Get("query") != "" || api.seen[1].Query.Get("query") != "sam" {
		t.Fatalf("requests=%+v", api.seen)
	}

	// The second search inside the window reuses the warm cache.
	if _, err := workspace.ContactsSearch(context.Background(), "alex", 5); err != nil {
		t.Fatal(err)
	}
	if len(api.seen) != 3 {
		t.Fatalf("the warmup was paid twice: %d requests", len(api.seen))
	}
}

// People clears any field named in updatePersonFields and not supplied, so
// naming a field the caller never set deletes it.
func TestUpdatingAContactNamesOnlyTheFieldsGiven(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{
		"/people/c1": `{"resourceName":"people/c1","etag":"etag-1","names":[{"displayName":"Sam"}],"phoneNumbers":[{"value":"555"}]}`,
	})
	_, err := workspace.ContactsUpdate(context.Background(), "people/c1", Contact{Emails: []string{"sam@work.example"}})
	if err != nil {
		t.Fatal(err)
	}
	update := api.lastTo(t, ":updateContact")
	fields := update.Query.Get("updatePersonFields")
	if fields != "emailAddresses" {
		t.Fatalf("updatePersonFields=%q, want only what the caller set", fields)
	}
	// The etag is how People refuses an edit made against a stale read.
	if !strings.Contains(update.Body, "etag-1") {
		t.Fatalf("no etag was carried: %s", update.Body)
	}
}

// The attachment ids are per-message and appear nowhere else, so a message
// that does not list them makes its own attachments unreachable.
func TestReadingAMessageListsItsAttachments(t *testing.T) {
	body := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte("see attached"))
	workspace, _ := recordingWorkspace(t, map[string]string{
		"/messages/m1": `{"id":"m1","payload":{"mimeType":"multipart/mixed","parts":[
{"mimeType":"text/plain","body":{"data":"` + body + `"}},
{"mimeType":"application/pdf","filename":"report.pdf","body":{"attachmentId":"a1","size":9000}}]}}`,
	})
	message, err := workspace.GmailGet(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if message.Body != "see attached" {
		t.Fatalf("body=%q", message.Body)
	}
	if len(message.Attachments) != 1 || message.Attachments[0].ID != "a1" || message.Attachments[0].Filename != "report.pdf" {
		t.Fatalf("attachments=%#v", message.Attachments)
	}
}

func TestFetchingABinaryAttachmentDescribesItInstead(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{
		"/messages/m1": `{"id":"m1","payload":{"parts":[{"mimeType":"application/pdf","filename":"report.pdf","body":{"attachmentId":"a1","size":9000}}]}}`,
	})
	content, err := workspace.GmailAttachment(context.Background(), "m1", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if content.Text != "" || content.Filename != "report.pdf" || !strings.Contains(content.Note, "application/pdf") {
		t.Fatalf("content=%#v", content)
	}
	for _, seen := range api.seen {
		if strings.Contains(seen.Path, "/attachments/") {
			t.Fatal("the bytes were fetched anyway")
		}
	}
}

func TestFetchingATextAttachmentDecodesIt(t *testing.T) {
	encoded := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte("id,total\n1,99\n"))
	workspace, _ := recordingWorkspace(t, map[string]string{
		"/messages/m1":        `{"id":"m1","payload":{"parts":[{"mimeType":"text/csv","filename":"sales.csv","body":{"attachmentId":"a1","size":14}}]}}`,
		"/attachments/a1":     `{"size":14,"data":"` + encoded + `"}`,
		"/messages/m1/attach": `{}`,
	})
	content, err := workspace.GmailAttachment(context.Background(), "m1", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content.Text, "id,total") {
		t.Fatalf("content=%#v", content)
	}
}

// A draft is the safe half of "email X about Y": it exists in the mailbox for
// the owner to read, and nothing left the account.
func TestDraftingDoesNotSend(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{
		"/drafts": `{"id":"r1","message":{"id":"m5","threadId":"t5"}}`,
	})
	draft, err := workspace.GmailDraft(context.Background(), Outgoing{To: "a@b.c", Subject: "Hi", Body: "text"})
	if err != nil {
		t.Fatal(err)
	}
	if draft.ID != "r1" {
		t.Fatalf("draft=%#v", draft)
	}
	for _, seen := range api.seen {
		if strings.HasSuffix(seen.Path, "/messages/send") {
			t.Fatal("drafting sent the mail")
		}
	}
	if !strings.Contains(api.lastTo(t, "/drafts").Body, `"raw"`) {
		t.Fatalf("body=%s", api.lastTo(t, "/drafts").Body)
	}
}

func TestTrashingIsRecoverable(t *testing.T) {
	workspace, api := recordingWorkspace(t, map[string]string{"/trash": `{"id":"m1","labelIds":["TRASH"]}`})
	if _, err := workspace.GmailTrash(context.Background(), "m1"); err != nil {
		t.Fatal(err)
	}
	seen := api.lastTo(t, "/trash")
	if seen.Method != http.MethodPost {
		t.Fatalf("method=%s", seen.Method)
	}
	for _, call := range api.seen {
		if call.Method == http.MethodDelete {
			t.Fatal("a message was deleted outright rather than trashed")
		}
	}
}

// A tool result that is not text still has to be a JSON document the model can
// read fields out of, whatever shape the product method returned.
func TestEveryNewActionAnswersInJSON(t *testing.T) {
	workspace, _ := recordingWorkspace(t, map[string]string{
		"/events/e1":      `{"id":"e1","attendees":[{"email":"owner@example.com","self":true}]}`,
		"/files/f1":       `{"id":"f1","name":"notes.txt","mimeType":"text/plain"}`,
		"/permissions":    `{"id":"p1"}`,
		":batchUpdate":    `{"replies":[{"replaceAllText":{"occurrencesChanged":2}}]}`,
		":searchContacts": `{"results":[]}`,
		"/drafts":         `{"id":"r1"}`,
	})
	calls := map[string][]string{
		"google_calendar": {`{"action":"get","event_id":"e1"}`, `{"action":"respond","event_id":"e1","response":"yes"}`},
		"google_drive":    {`{"action":"get","file_id":"f1"}`, `{"action":"delete","file_id":"f1"}`, `{"action":"share","file_id":"f1","email":"a@b.c"}`},
		"google_docs":     {`{"action":"replace","document_id":"d1","find":"a","replace":"b"}`},
		"google_sheets":   {`{"action":"clear","spreadsheet_id":"s1","range":"A1:B2"}`},
		"google_contacts": {`{"action":"search","query":"sam"}`},
		"google_gmail":    {`{"action":"draft","to":"a@b.c","body":"hi"}`},
	}
	for _, tool := range Tools(workspace, []string{"gmail", "calendar", "drive", "docs", "sheets", "contacts"}, time.Now) {
		for _, arguments := range calls[tool.Definition().Name] {
			result, err := tool.Execute(context.Background(), json.RawMessage(arguments))
			if err != nil {
				t.Fatalf("%s %s: %v", tool.Definition().Name, arguments, err)
			}
			if !json.Valid(result) {
				t.Fatalf("%s %s returned %q", tool.Definition().Name, arguments, result)
			}
		}
	}
}
