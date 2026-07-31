package google

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Message is one mail in the shape a model reads. It mirrors the JSON contract
// Hermes' CLI prints, because that contract is well chosen: identifiers to act
// on, headers to decide with, and a snippet instead of a body until the body
// is asked for.
type Message struct {
	ID       string   `json:"id"`
	ThreadID string   `json:"thread_id"`
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	Subject  string   `json:"subject,omitempty"`
	Date     string   `json:"date,omitempty"`
	Snippet  string   `json:"snippet,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	Body     string   `json:"body,omitempty"`
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
	Headers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Body  struct{ Data string } `json:"body"`
	Parts []gmailPayload        `json:"parts"`
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
	}
	return message, nil
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

// GmailSend posts a MIME message Eggy assembles itself. There is no library
// here on purpose: the message is a handful of headers and a body, and adding
// a dependency to write six lines of RFC 5322 is not a trade worth making.
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

func (w *Workspace) submit(ctx context.Context, outgoing Outgoing, threadID string, extra map[string]string) (Message, error) {
	if strings.TrimSpace(outgoing.Body) == "" {
		return Message{}, errors.New("a body is required")
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
	for name, value := range extra {
		headers = append(headers, name+": "+value)
	}
	raw := strings.Join(headers, "\r\n") + "\r\n\r\n" + outgoing.Body
	request := map[string]string{"raw": base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(raw))}
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

// rfc3339 is the only time format these tools accept or emit. A bare datetime
// is ambiguous and Google resolves it as UTC, which silently moves an event by
// hours; Hermes documents the same rule as a warning rather than enforcing it.
func rfc3339(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if _, err := time.Parse(time.RFC3339, trimmed); err != nil {
		return "", fmt.Errorf("%q needs a timezone offset or Z (RFC 3339), otherwise it is read as UTC and lands hours away", value)
	}
	return trimmed, nil
}
