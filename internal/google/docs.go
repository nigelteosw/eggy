package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type Document struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title"`
	Text  string `json:"text,omitempty"`
	Link  string `json:"link,omitempty"`
}

// documentLink is built rather than read back: the Docs API returns no URL,
// and the owner needs somewhere to click.
func documentLink(id string) string {
	if id == "" {
		return ""
	}
	return "https://docs.google.com/document/d/" + id + "/edit"
}

// DocsGet flattens the document to text. A Doc is a tree of structural
// elements; a model asked to read one wants prose, not the tree.
func (w *Workspace) DocsGet(ctx context.Context, id string) (Document, error) {
	if strings.TrimSpace(id) == "" {
		return Document{}, errors.New("a document id is required")
	}
	var response struct {
		DocumentID string `json:"documentId"`
		Title      string `json:"title"`
		Body       struct {
			Content []struct {
				Paragraph struct {
					Elements []struct {
						TextRun struct {
							Content string `json:"content"`
						} `json:"textRun"`
					} `json:"elements"`
				} `json:"paragraph"`
			} `json:"content"`
		} `json:"body"`
	}
	if err := w.call(ctx, http.MethodGet, w.endpoints.Docs+"/documents/"+url.PathEscape(id), nil, nil, &response); err != nil {
		return Document{}, err
	}
	var text strings.Builder
	for _, element := range response.Body.Content {
		for _, run := range element.Paragraph.Elements {
			text.WriteString(run.TextRun.Content)
		}
	}
	return Document{ID: response.DocumentID, Title: response.Title, Text: text.String(), Link: documentLink(response.DocumentID)}, nil
}

// DocsCreate makes a document and optionally fills it. Creating is one call
// and writing is another -- documents.create takes a title and nothing else --
// so a create with body text is two requests and cannot be one.
func (w *Workspace) DocsCreate(ctx context.Context, title, body string) (Document, error) {
	if strings.TrimSpace(title) == "" {
		return Document{}, errors.New("a title is required")
	}
	var created struct {
		DocumentID string `json:"documentId"`
		Title      string `json:"title"`
	}
	if err := w.call(ctx, http.MethodPost, w.endpoints.Docs+"/documents", nil, map[string]any{"title": title}, &created); err != nil {
		return Document{}, err
	}
	document := Document{ID: created.DocumentID, Title: created.Title, Link: documentLink(created.DocumentID)}
	if strings.TrimSpace(body) == "" {
		return document, nil
	}
	if _, err := w.DocsAppend(ctx, created.DocumentID, body); err != nil {
		// The document exists either way, and reporting only the failure would
		// leave an untitled orphan the owner never hears about.
		return document, fmt.Errorf("created %q but could not write its body: %w", created.Title, err)
	}
	document.Text = body
	return document, nil
}

// DocsAppend adds text at the end.
//
// endOfSegmentLocation is what makes this one request. Hermes reads the whole
// document first to compute the final index, minus one for the newline Docs
// always keeps last -- an index arithmetic that is easy to get wrong and
// unnecessary, because the API will find the end itself.
func (w *Workspace) DocsAppend(ctx context.Context, id, text string) (map[string]any, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("a document id is required")
	}
	if text == "" {
		return nil, errors.New("there is no text to append")
	}
	// Docs does not add the paragraph break: without it, appending twice runs
	// the two together on one line.
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	requests := []map[string]any{{
		"insertText": map[string]any{
			// An empty segmentId means the body, as opposed to a header or a
			// footnote.
			"endOfSegmentLocation": map[string]any{"segmentId": ""},
			"text":                 text,
		},
	}}
	endpoint := w.endpoints.Docs + "/documents/" + url.PathEscape(id) + ":batchUpdate"
	if err := w.call(ctx, http.MethodPost, endpoint, nil, map[string]any{"requests": requests}, nil); err != nil {
		return nil, err
	}
	return map[string]any{"status": "appended", "document_id": id, "characters": len(text), "link": documentLink(id)}, nil
}

// DocsReplace substitutes text throughout the document. It reports how many
// replacements happened, which is the only way to tell a successful edit from
// a search string that matched nothing -- both otherwise return success.
func (w *Workspace) DocsReplace(ctx context.Context, id, find, replace string, matchCase bool) (map[string]any, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("a document id is required")
	}
	if find == "" {
		return nil, errors.New("there is nothing to find")
	}
	requests := []map[string]any{{
		"replaceAllText": map[string]any{
			"containsText": map[string]any{"text": find, "matchCase": matchCase},
			"replaceText":  replace,
		},
	}}
	var response struct {
		Replies []struct {
			ReplaceAllText struct {
				OccurrencesChanged int `json:"occurrencesChanged"`
			} `json:"replaceAllText"`
		} `json:"replies"`
	}
	endpoint := w.endpoints.Docs + "/documents/" + url.PathEscape(id) + ":batchUpdate"
	if err := w.call(ctx, http.MethodPost, endpoint, nil, map[string]any{"requests": requests}, &response); err != nil {
		return nil, err
	}
	changed := 0
	for _, reply := range response.Replies {
		changed += reply.ReplaceAllText.OccurrencesChanged
	}
	result := map[string]any{"status": "replaced", "document_id": id, "occurrences_changed": changed, "link": documentLink(id)}
	if changed == 0 {
		result["note"] = fmt.Sprintf("nothing matched %q, so the document is unchanged", find)
	}
	return result, nil
}
