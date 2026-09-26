package google

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// Message is one mail in the shape a model reads. It mirrors the JSON contract
// Hermes' CLI prints, because that contract is well chosen: identifiers to act
// on, headers to decide with, and a snippet instead of a body until the body
// is asked for.
type Message struct {
	ID          string       `json:"id"`
	ThreadID    string       `json:"thread_id"`
	From        string       `json:"from,omitempty"`
	To          string       `json:"to,omitempty"`
	Subject     string       `json:"subject,omitempty"`
	Date        string       `json:"date,omitempty"`
	Snippet     string       `json:"snippet,omitempty"`
	Labels      []string     `json:"labels,omitempty"`
	Body        string       `json:"body,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment is what a message advertises, never the bytes. Listing them on
// the message is what makes them reachable at all: the id is per-message and
// appears nowhere else, so an attachment nobody listed cannot be fetched.
type Attachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MimeType string `json:"mime_type,omitempty"`
	Size     int    `json:"size,omitempty"`
}

// maxMessages bounds a search. Each result costs a metadata fetch, and a
// hundred of them would spend the turn on one tool call.
const maxMessages = 25

// GmailSearch runs Gmail's own query syntax -- is:unread, from:, newer_than:,
// has:attachment -- and hydrates each hit with the headers needed to triage it.
//
// Gmail's list endpoint returns identifiers only, so the metadata costs one
// request per message. That is why the count is bounded here rather than
// passed through.
func (w *Workspace) GmailSearch(ctx context.Context, query string, max int) ([]Message, error) {
	if max <= 0 || max > maxMessages {
		max = 10
	}
	var listing struct {
		Messages []struct{ ID, ThreadID string } `json:"messages"`
	}
	values := url.Values{"q": {query}, "maxResults": {fmt.Sprint(max)}}
	if err := w.call(ctx, http.MethodGet, w.endpoints.Gmail+"/users/me/messages", values, nil, &listing); err != nil {
		return nil, err
	}
	messages := make([]Message, 0, len(listing.Messages))
	for _, found := range listing.Messages {
		message, err := w.gmailMessage(ctx, found.ID, "metadata")
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

// GmailGet returns the readable body, preferring text/plain and falling back
// to HTML, which is the only ordering that yields something a model can quote
// from a modern multipart mail.
func (w *Workspace) GmailGet(ctx context.Context, id string) (Message, error) {
	return w.gmailMessage(ctx, id, "full")
}

type gmailPayload struct {
	MimeType string `json:"mimeType"`
	Filename string `json:"filename"`
	Headers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Body struct {
		Data         string `json:"data"`
		AttachmentID string `json:"attachmentId"`
		Size         int    `json:"size"`
	} `json:"body"`
	Parts []gmailPayload `json:"parts"`
}

type gmailMessageResponse struct {
	ID       string       `json:"id"`
	ThreadID string       `json:"threadId"`
	Snippet  string       `json:"snippet"`
	LabelIDs []string     `json:"labelIds"`
	Payload  gmailPayload `json:"payload"`
}

func (w *Workspace) gmailMessage(ctx context.Context, id, format string) (Message, error) {
	if strings.TrimSpace(id) == "" {
		return Message{}, errors.New("a message id is required")
	}
	var response gmailMessageResponse
	values := url.Values{"format": {format}}
	if format == "metadata" {
		values["metadataHeaders"] = []string{"From", "To", "Subject", "Date"}
	}
	if err := w.call(ctx, http.MethodGet, w.endpoints.Gmail+"/users/me/messages/"+url.PathEscape(id), values, nil, &response); err != nil {
		return Message{}, err
	}
	message := Message{ID: response.ID, ThreadID: response.ThreadID, Snippet: response.Snippet, Labels: response.LabelIDs}
	message.From = header(response.Payload, "From")
	message.To = header(response.Payload, "To")
	message.Subject = header(response.Payload, "Subject")
	message.Date = header(response.Payload, "Date")
	if format == "full" {
		message.Body = messageBody(response.Payload)
		message.Attachments = attachmentsIn(response.Payload)
	}
	return message, nil
}

// attachmentsIn collects the parts Gmail stored separately. A part carries an
// attachmentId exactly when its bytes were not inlined, which is the same test
// as "this is an attachment" and a more reliable one than the filename: inline
// images have names too.
func attachmentsIn(payload gmailPayload) []Attachment {
	var found []Attachment
	if payload.Body.AttachmentID != "" {
		name := payload.Filename
		if name == "" {
			name = "(unnamed)"
		}
		found = append(found, Attachment{ID: payload.Body.AttachmentID, Filename: name, MimeType: payload.MimeType, Size: payload.Body.Size})
	}
	for _, part := range payload.Parts {
		found = append(found, attachmentsIn(part)...)
	}
	return found
}

func header(payload gmailPayload, name string) string {
	for _, candidate := range payload.Headers {
		if strings.EqualFold(candidate.Name, name) {
			return candidate.Value
		}
	}
	return ""
}

// messageBody walks the MIME tree for the first text/plain part, then settles
// for text/html. Gmail nests alternatives arbitrarily deep once a mail has
// attachments, so this recurses rather than reading the top level only.
func messageBody(payload gmailPayload) string {
	if body := findPart(payload, "text/plain"); body != "" {
		return body
	}
	return findPart(payload, "text/html")
}

func findPart(payload gmailPayload, mimeType string) string {
	if strings.HasPrefix(payload.MimeType, mimeType) && payload.Body.Data != "" {
		if decoded, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(payload.Body.Data); err == nil {
			return string(decoded)
		}
	}
	for _, part := range payload.Parts {
		if body := findPart(part, mimeType); body != "" {
			return body
		}
	}
	return ""
}

// Outgoing is one mail to send or one reply to write. From carries a display
// name over the authenticated address -- Gmail allows that without any Send As
// configuration, which is what lets several agents share one mailbox and still
// be told apart by a recipient.
type Outgoing struct {
	To      string
	CC      string
	Subject string
	Body    string
	HTML    bool
	From    string
	ReplyTo string
}

// GmailSend posts a MIME message Eggy assembles itself.
func (w *Workspace) GmailSend(ctx context.Context, outgoing Outgoing) (Message, error) {
	if strings.TrimSpace(outgoing.To) == "" {
		return Message{}, errors.New("a recipient is required")
	}
	return w.submit(ctx, outgoing, "", nil)
}

// GmailReply threads correctly, which is the whole reason it is not a send
// with a quoted subject: a reply that omits In-Reply-To and References opens a
// new conversation in every client the recipient might use.
func (w *Workspace) GmailReply(ctx context.Context, id, body, from string) (Message, error) {
	original, err := w.gmailMessage(ctx, id, "metadata")
	if err != nil {
		return Message{}, err
	}
	var full gmailMessageResponse
	values := url.Values{"format": {"metadata"}, "metadataHeaders": {"Message-ID", "References", "Subject", "From", "Reply-To"}}
	if err := w.call(ctx, http.MethodGet, w.endpoints.Gmail+"/users/me/messages/"+url.PathEscape(id), values, nil, &full); err != nil {
		return Message{}, err
	}
	messageID := header(full.Payload, "Message-ID")
	references := strings.TrimSpace(header(full.Payload, "References") + " " + messageID)
	recipient := header(full.Payload, "Reply-To")
	if recipient == "" {
		recipient = original.From
	}
	subject := original.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	extra := map[string]string{}
	if messageID != "" {
		extra["In-Reply-To"] = messageID
		extra["References"] = references
	}
	return w.submit(ctx, Outgoing{To: recipient, Subject: subject, Body: body, From: from}, original.ThreadID, extra)
}

// mimeMessage assembles RFC 5322 and encodes it the way Gmail wants it. There
// is no library here on purpose: the message is a handful of headers and a
// body, and adding a dependency to write six lines of it is not a trade worth
// making.
func mimeMessage(outgoing Outgoing, extra map[string]string) (string, error) {
	if strings.TrimSpace(outgoing.Body) == "" {
		return "", errors.New("a body is required")
	}
	contentType := "text/plain; charset=\"UTF-8\""
	if outgoing.HTML {
		contentType = "text/html; charset=\"UTF-8\""
	}
	headers := []string{"MIME-Version: 1.0", "Content-Type: " + contentType, "To: " + outgoing.To}
	if outgoing.From != "" {
		headers = append(headers, "From: "+outgoing.From)
	}
	if outgoing.CC != "" {
		headers = append(headers, "Cc: "+outgoing.CC)
	}
	if outgoing.Subject != "" {
		headers = append(headers, "Subject: "+mime.QEncoding.Encode("UTF-8", outgoing.Subject))
	}
	// Sorted, because a map iterates in a different order every time and the
	// threading headers would otherwise make every test flaky.
	for _, name := range slices.Sorted(maps.Keys(extra)) {
		headers = append(headers, name+": "+extra[name])
	}
	raw := strings.Join(headers, "\r\n") + "\r\n\r\n" + outgoing.Body
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(raw)), nil
}

func (w *Workspace) submit(ctx context.Context, outgoing Outgoing, threadID string, extra map[string]string) (Message, error) {
	raw, err := mimeMessage(outgoing, extra)
	if err != nil {
		return Message{}, err
	}
	request := map[string]string{"raw": raw}
	if threadID != "" {
		request["threadId"] = threadID
	}
	var response struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	}
	if err := w.call(ctx, http.MethodPost, w.endpoints.Gmail+"/users/me/messages/send", nil, request, &response); err != nil {
		return Message{}, err
	}
	return Message{ID: response.ID, ThreadID: response.ThreadID}, nil
}

// GmailThread returns a whole conversation, oldest first. Reading a thread one
// message at a time costs a call per message and loses the ordering that makes
// it a conversation; this is the call for "what is the latest on this".
//
// Bodies are included, because a thread whose messages are all snippets answers
// nothing that search did not already answer.
func (w *Workspace) GmailThread(ctx context.Context, id string) ([]Message, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("a thread id is required")
	}
	var response struct {
		Messages []gmailMessageResponse `json:"messages"`
	}
	values := url.Values{"format": {"full"}}
	if err := w.call(ctx, http.MethodGet, w.endpoints.Gmail+"/users/me/threads/"+url.PathEscape(id), values, nil, &response); err != nil {
		return nil, err
	}
	messages := make([]Message, 0, len(response.Messages))
	for _, found := range response.Messages {
		message := Message{ID: found.ID, ThreadID: found.ThreadID, Snippet: found.Snippet, Labels: found.LabelIDs}
		message.From = header(found.Payload, "From")
		message.To = header(found.Payload, "To")
		message.Subject = header(found.Payload, "Subject")
		message.Date = header(found.Payload, "Date")
		message.Body = messageBody(found.Payload)
		message.Attachments = attachmentsIn(found.Payload)
		messages = append(messages, message)
	}
	return messages, nil
}

// AttachmentContent is a fetched attachment. Text arrives decoded; anything
// else arrives as a description of itself.
type AttachmentContent struct {
	Filename string `json:"filename,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Size     int    `json:"size"`
	Text     string `json:"text,omitempty"`
	Note     string `json:"note,omitempty"`
}

// GmailAttachment fetches one attachment's bytes and returns them only when
// they are text.
//
// A model cannot do anything with base64 of a PDF or an image except spend the
// turn's context on it, so binary content is reported rather than returned:
// what it is, how big, and that it was not decoded. That is a more useful
// answer than 200 KB of base64 and a truncation error.
func (w *Workspace) GmailAttachment(ctx context.Context, messageID, attachmentID string) (AttachmentContent, error) {
	if strings.TrimSpace(messageID) == "" || strings.TrimSpace(attachmentID) == "" {
		return AttachmentContent{}, errors.New("a message id and an attachment id are required; action=get lists them")
	}
	// The metadata lives on the message, not on the attachment: the attachment
	// endpoint returns bytes and a size and nothing else.
	message, err := w.gmailMessage(ctx, messageID, "full")
	if err != nil {
		return AttachmentContent{}, err
	}
	content := AttachmentContent{}
	for _, attachment := range message.Attachments {
		if attachment.ID == attachmentID {
			content.Filename, content.MimeType, content.Size = attachment.Filename, attachment.MimeType, attachment.Size
			break
		}
	}
	if !isTextual(content.MimeType) {
		content.Note = fmt.Sprintf("%s is not text, so its content was not fetched; ask the owner if they need it", displayType(content.MimeType))
		return content, nil
	}
	var response struct {
		Size int    `json:"size"`
		Data string `json:"data"`
	}
	endpoint := fmt.Sprintf("%s/users/me/messages/%s/attachments/%s", w.endpoints.Gmail, url.PathEscape(messageID), url.PathEscape(attachmentID))
	if err := w.call(ctx, http.MethodGet, endpoint, nil, nil, &response); err != nil {
		return AttachmentContent{}, err
	}
	decoded, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(response.Data)
	if err != nil {
		return AttachmentContent{}, fmt.Errorf("decode attachment: %w", err)
	}
	content.Size = response.Size
	content.Text = string(decoded)
	return content, nil
}

// isTextual decides what is worth decoding into a model's context. JSON, CSV
// and XML are text that Gmail does not label text/*, and they are exactly the
// attachments worth reading.
func isTextual(mimeType string) bool {
	base, _, _ := strings.Cut(mimeType, ";")
	base = strings.TrimSpace(strings.ToLower(base))
	if strings.HasPrefix(base, "text/") {
		return true
	}
	switch base {
	case "application/json", "application/xml", "application/csv", "application/x-ndjson", "application/yaml", "application/x-yaml":
		return true
	}
	return false
}

func displayType(mimeType string) string {
	if strings.TrimSpace(mimeType) == "" {
		return "That attachment"
	}
	return mimeType
}

// Draft is a mail written but not sent. The draft id and the message id are
// different identifiers and only the draft id can send it, which is why both
// are reported.
type Draft struct {
	ID      string  `json:"id"`
	Message Message `json:"message,omitzero"`
}

// GmailDraft writes a mail without sending it. It is the safe half of every
// "email X about Y": the owner reads it in Gmail and sends it themselves, and
// nothing left the account to get there.
func (w *Workspace) GmailDraft(ctx context.Context, outgoing Outgoing) (Draft, error) {
	if strings.TrimSpace(outgoing.To) == "" {
		return Draft{}, errors.New("a recipient is required")
	}
	raw, err := mimeMessage(outgoing, nil)
	if err != nil {
		return Draft{}, err
	}
	var response struct {
		ID      string `json:"id"`
		Message struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"message"`
	}
	request := map[string]any{"message": map[string]string{"raw": raw}}
	if err := w.call(ctx, http.MethodPost, w.endpoints.Gmail+"/users/me/drafts", nil, request, &response); err != nil {
		return Draft{}, err
	}
	return Draft{ID: response.ID, Message: Message{ID: response.Message.ID, ThreadID: response.Message.ThreadID, To: outgoing.To, Subject: outgoing.Subject}}, nil
}

// GmailDrafts lists what is waiting, with enough of each to tell them apart.
func (w *Workspace) GmailDrafts(ctx context.Context, max int) ([]Draft, error) {
	if max <= 0 || max > maxMessages {
		max = 10
	}
	var listing struct {
		Drafts []struct {
			ID      string `json:"id"`
			Message struct {
				ID string `json:"id"`
			} `json:"message"`
		} `json:"drafts"`
	}
	values := url.Values{"maxResults": {fmt.Sprint(max)}}
	if err := w.call(ctx, http.MethodGet, w.endpoints.Gmail+"/users/me/drafts", values, nil, &listing); err != nil {
		return nil, err
	}
	drafts := make([]Draft, 0, len(listing.Drafts))
	for _, found := range listing.Drafts {
		draft := Draft{ID: found.ID}
		// The listing carries ids only. A draft nobody can tell apart from
		// another is not a list the owner can choose from.
		if message, err := w.gmailMessage(ctx, found.Message.ID, "metadata"); err == nil {
			draft.Message = message
		}
		drafts = append(drafts, draft)
	}
	return drafts, nil
}

// GmailSendDraft sends one that already exists, unchanged.
func (w *Workspace) GmailSendDraft(ctx context.Context, draftID string) (Message, error) {
	if strings.TrimSpace(draftID) == "" {
		return Message{}, errors.New("a draft id is required")
	}
	var response struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	}
	request := map[string]string{"id": draftID}
	if err := w.call(ctx, http.MethodPost, w.endpoints.Gmail+"/users/me/drafts/send", nil, request, &response); err != nil {
		return Message{}, err
	}
	return Message{ID: response.ID, ThreadID: response.ThreadID}, nil
}

// GmailTrash moves a message to the trash, where Gmail keeps it for 30 days.
// There is no permanent delete here on purpose: nothing an agent does to a
// mailbox should be unrecoverable, and the owner can empty the trash.
func (w *Workspace) GmailTrash(ctx context.Context, id string) (Message, error) {
	if strings.TrimSpace(id) == "" {
		return Message{}, errors.New("a message id is required")
	}
	var response gmailMessageResponse
	endpoint := w.endpoints.Gmail + "/users/me/messages/" + url.PathEscape(id) + "/trash"
	if err := w.call(ctx, http.MethodPost, endpoint, nil, nil, &response); err != nil {
		return Message{}, err
	}
	return Message{ID: response.ID, ThreadID: response.ThreadID, Labels: response.LabelIDs}, nil
}

type Label struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (w *Workspace) GmailLabels(ctx context.Context) ([]Label, error) {
	var response struct {
		Labels []Label `json:"labels"`
	}
	if err := w.call(ctx, http.MethodGet, w.endpoints.Gmail+"/users/me/labels", nil, nil, &response); err != nil {
		return nil, err
	}
	return response.Labels, nil
}

func (w *Workspace) GmailModify(ctx context.Context, id string, add, remove []string) (Message, error) {
	if len(add) == 0 && len(remove) == 0 {
		return Message{}, errors.New("no labels to add or remove")
	}
	request := map[string][]string{}
	if len(add) > 0 {
		request["addLabelIds"] = add
	}
	if len(remove) > 0 {
		request["removeLabelIds"] = remove
	}
	var response gmailMessageResponse
	if err := w.call(ctx, http.MethodPost, w.endpoints.Gmail+"/users/me/messages/"+url.PathEscape(id)+"/modify", nil, request, &response); err != nil {
		return Message{}, err
	}
	return Message{ID: response.ID, ThreadID: response.ThreadID, Labels: response.LabelIDs}, nil
}
