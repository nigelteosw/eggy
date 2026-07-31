package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Drive, Docs, Sheets and Contacts share a file because each is a handful of
// calls against the same grant. Splitting them per product would suggest they
// are independent adapters; they are one integration with several endpoints.

type File struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type,omitempty"`
	Modified string `json:"modified,omitempty"`
	Link     string `json:"link,omitempty"`
}

// DriveSearch takes plain words by default and escapes them into a fullText
// query. raw passes the caller's string through as a Drive query expression --
// mimeType filters and the like -- which is a sharper tool and a much easier
// one to get a syntax error from.
func (w *Workspace) DriveSearch(ctx context.Context, query string, max int, raw bool) ([]File, error) {
	if max <= 0 || max > 50 {
		max = 10
	}
	expression := query
	if !raw {
		expression = fmt.Sprintf("fullText contains '%s'", strings.ReplaceAll(query, "'", `\'`))
	}
	values := url.Values{
		"q": {expression}, "pageSize": {fmt.Sprint(max)},
		"fields": {"files(id,name,mimeType,modifiedTime,webViewLink)"},
	}
	var response struct {
		Files []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			MimeType     string `json:"mimeType"`
			ModifiedTime string `json:"modifiedTime"`
			WebViewLink  string `json:"webViewLink"`
		} `json:"files"`
	}
	if err := w.call(ctx, http.MethodGet, w.endpoints.Drive+"/files", values, nil, &response); err != nil {
		return nil, err
	}
	files := make([]File, 0, len(response.Files))
	for _, file := range response.Files {
		files = append(files, File{ID: file.ID, Name: file.Name, MimeType: file.MimeType, Modified: file.ModifiedTime, Link: file.WebViewLink})
	}
	return files, nil
}

type Document struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// DocsGet flattens the document to text. A Doc is a tree of structural
// elements; a model asked to read one wants prose, not the tree.
func (w *Workspace) DocsGet(ctx context.Context, id string) (Document, error) {
	if strings.TrimSpace(id) == "" {
		return Document{}, errors.New("a document id is required")
	}
	var response struct {
		Title string `json:"title"`
		Body  struct {
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
	return Document{Title: response.Title, Text: text.String()}, nil
}

// SheetsGet returns the raw cell grid. Rows are ragged -- Sheets omits
// trailing empty cells -- and squaring them off here would invent data.
func (w *Workspace) SheetsGet(ctx context.Context, id, cellRange string) ([][]any, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(cellRange) == "" {
		return nil, errors.New("a spreadsheet id and a range are required")
	}
	var response struct {
		Values [][]any `json:"values"`
	}
	endpoint := fmt.Sprintf("%s/spreadsheets/%s/values/%s", w.endpoints.Sheets, url.PathEscape(id), url.PathEscape(cellRange))
	if err := w.call(ctx, http.MethodGet, endpoint, nil, nil, &response); err != nil {
		return nil, err
	}
	return response.Values, nil
}

// SheetsUpdate overwrites a range; SheetsAppend adds after the last row.
// USER_ENTERED means the sheet parses what is written the way it would parse
// typing: a date stays a date and a formula stays a formula.
func (w *Workspace) SheetsUpdate(ctx context.Context, id, cellRange string, values [][]any) (int, error) {
	endpoint := fmt.Sprintf("%s/spreadsheets/%s/values/%s", w.endpoints.Sheets, url.PathEscape(id), url.PathEscape(cellRange))
	var response struct {
		UpdatedCells int `json:"updatedCells"`
	}
	query := url.Values{"valueInputOption": {"USER_ENTERED"}}
	if err := w.call(ctx, http.MethodPut, endpoint, query, map[string]any{"values": values}, &response); err != nil {
		return 0, err
	}
	return response.UpdatedCells, nil
}

func (w *Workspace) SheetsAppend(ctx context.Context, id, cellRange string, values [][]any) (int, error) {
	endpoint := fmt.Sprintf("%s/spreadsheets/%s/values/%s:append", w.endpoints.Sheets, url.PathEscape(id), url.PathEscape(cellRange))
	var response struct {
		Updates struct {
			UpdatedCells int `json:"updatedCells"`
		} `json:"updates"`
	}
	query := url.Values{"valueInputOption": {"USER_ENTERED"}, "insertDataOption": {"INSERT_ROWS"}}
	if err := w.call(ctx, http.MethodPost, endpoint, query, map[string]any{"values": values}, &response); err != nil {
		return 0, err
	}
	return response.Updates.UpdatedCells, nil
}

type Contact struct {
	Name   string   `json:"name,omitempty"`
	Emails []string `json:"emails,omitempty"`
	Phones []string `json:"phones,omitempty"`
}

// ContactsList is the capability the MCP path cannot reach at all: Google
// hosts no People MCP server, so this is the one product where the choice of
// integration decides whether it exists.
func (w *Workspace) ContactsList(ctx context.Context, max int) ([]Contact, error) {
	if max <= 0 || max > 100 {
		max = 20
	}
	values := url.Values{"pageSize": {fmt.Sprint(max)}, "personFields": {"names,emailAddresses,phoneNumbers"}}
	var response struct {
		Connections []struct {
			Names []struct {
				DisplayName string `json:"displayName"`
			} `json:"names"`
			EmailAddresses []struct {
				Value string `json:"value"`
			} `json:"emailAddresses"`
			PhoneNumbers []struct {
				Value string `json:"value"`
			} `json:"phoneNumbers"`
		} `json:"connections"`
	}
	if err := w.call(ctx, http.MethodGet, w.endpoints.People+"/people/me/connections", values, nil, &response); err != nil {
		return nil, err
	}
	contacts := make([]Contact, 0, len(response.Connections))
	for _, person := range response.Connections {
		contact := Contact{}
		if len(person.Names) > 0 {
			contact.Name = person.Names[0].DisplayName
		}
		for _, email := range person.EmailAddresses {
			contact.Emails = append(contact.Emails, email.Value)
		}
		for _, phone := range person.PhoneNumbers {
			contact.Phones = append(contact.Phones, phone.Value)
		}
		contacts = append(contacts, contact)
	}
	return contacts, nil
}
