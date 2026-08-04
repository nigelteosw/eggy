package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Spreadsheet is what a caller needs before it can name a range. A tab's title
// is the first half of every A1 reference, and its dimensions are the other
// half: without them a read is a guess, and "Sheet1!A1:Z100" against a workbook
// whose first tab is called anything else is a 400 the model cannot diagnose.
type Spreadsheet struct {
	ID     string  `json:"id,omitempty"`
	Title  string  `json:"title"`
	Sheets []Sheet `json:"sheets,omitempty"`
	Link   string  `json:"link,omitempty"`
}

type Sheet struct {
	ID      int    `json:"id"`
	Title   string `json:"title"`
	Rows    int    `json:"rows,omitempty"`
	Columns int    `json:"columns,omitempty"`
}

type sheetProperties struct {
	SheetID        int    `json:"sheetId"`
	Title          string `json:"title"`
	GridProperties struct {
		RowCount    int `json:"rowCount"`
		ColumnCount int `json:"columnCount"`
	} `json:"gridProperties"`
}

func (p sheetProperties) sheet() Sheet {
	return Sheet{ID: p.SheetID, Title: p.Title, Rows: p.GridProperties.RowCount, Columns: p.GridProperties.ColumnCount}
}

// SheetsInfo names the tabs. The field mask matters as much as the call: a bare
// spreadsheets.get returns every cell in the workbook, which would blow the
// output bound on any real sheet and answer a question about structure with
// data.
func (w *Workspace) SheetsInfo(ctx context.Context, id string) (Spreadsheet, error) {
	if strings.TrimSpace(id) == "" {
		return Spreadsheet{}, errors.New("a spreadsheet id is required")
	}
	values := url.Values{"fields": {"spreadsheetId,spreadsheetUrl,properties.title,sheets.properties(sheetId,title,gridProperties)"}}
	var response struct {
		SpreadsheetID  string `json:"spreadsheetId"`
		SpreadsheetURL string `json:"spreadsheetUrl"`
		Properties     struct {
			Title string `json:"title"`
		} `json:"properties"`
		Sheets []struct {
			Properties sheetProperties `json:"properties"`
		} `json:"sheets"`
	}
	endpoint := fmt.Sprintf("%s/spreadsheets/%s", w.endpoints.Sheets, url.PathEscape(id))
	if err := w.call(ctx, http.MethodGet, endpoint, values, nil, &response); err != nil {
		return Spreadsheet{}, err
	}
	spreadsheet := Spreadsheet{
		ID: response.SpreadsheetID, Title: response.Properties.Title,
		Link: response.SpreadsheetURL, Sheets: make([]Sheet, 0, len(response.Sheets)),
	}
	for _, sheet := range response.Sheets {
		spreadsheet.Sheets = append(spreadsheet.Sheets, sheet.Properties.sheet())
	}
	return spreadsheet, nil
}

// SheetsCreate makes a new workbook, optionally naming its first tab so the
// caller knows the range to write to without asking.
func (w *Workspace) SheetsCreate(ctx context.Context, title, firstSheet string) (Spreadsheet, error) {
	if strings.TrimSpace(title) == "" {
		return Spreadsheet{}, errors.New("a title is required")
	}
	request := map[string]any{"properties": map[string]any{"title": title}}
	if name := strings.TrimSpace(firstSheet); name != "" {
		request["sheets"] = []map[string]any{{"properties": map[string]any{"title": name}}}
	}
	var response struct {
		SpreadsheetID  string `json:"spreadsheetId"`
		SpreadsheetURL string `json:"spreadsheetUrl"`
		Properties     struct {
			Title string `json:"title"`
		} `json:"properties"`
		Sheets []struct {
			Properties sheetProperties `json:"properties"`
		} `json:"sheets"`
	}
	if err := w.call(ctx, http.MethodPost, w.endpoints.Sheets+"/spreadsheets", nil, request, &response); err != nil {
		return Spreadsheet{}, err
	}
	spreadsheet := Spreadsheet{ID: response.SpreadsheetID, Title: response.Properties.Title, Link: response.SpreadsheetURL}
	for _, sheet := range response.Sheets {
		spreadsheet.Sheets = append(spreadsheet.Sheets, sheet.Properties.sheet())
	}
	return spreadsheet, nil
}

// SheetsAddSheet adds a tab to an existing workbook and reports its title back,
// because that title is what every later range has to name.
func (w *Workspace) SheetsAddSheet(ctx context.Context, id, title string) (Sheet, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(title) == "" {
		return Sheet{}, errors.New("a spreadsheet id and a tab title are required")
	}
	requests := []map[string]any{{"addSheet": map[string]any{"properties": map[string]any{"title": title}}}}
	var response struct {
		Replies []struct {
			AddSheet struct {
				Properties sheetProperties `json:"properties"`
			} `json:"addSheet"`
		} `json:"replies"`
	}
	endpoint := fmt.Sprintf("%s/spreadsheets/%s:batchUpdate", w.endpoints.Sheets, url.PathEscape(id))
	if err := w.call(ctx, http.MethodPost, endpoint, nil, map[string]any{"requests": requests}, &response); err != nil {
		return Sheet{}, err
	}
	if len(response.Replies) == 0 {
		return Sheet{Title: title}, nil
	}
	return response.Replies[0].AddSheet.Properties.sheet(), nil
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

// SheetsClear empties a range's values and leaves its formatting alone, which
// is what "clear" means to someone looking at the sheet. It reports the range
// Sheets actually cleared, since an open range like "Sheet1!A:C" resolves to
// something narrower.
func (w *Workspace) SheetsClear(ctx context.Context, id, cellRange string) (map[string]any, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(cellRange) == "" {
		return nil, errors.New("a spreadsheet id and a range are required")
	}
	var response struct {
		ClearedRange string `json:"clearedRange"`
	}
	endpoint := fmt.Sprintf("%s/spreadsheets/%s/values/%s:clear", w.endpoints.Sheets, url.PathEscape(id), url.PathEscape(cellRange))
	if err := w.call(ctx, http.MethodPost, endpoint, nil, map[string]any{}, &response); err != nil {
		return nil, err
	}
	return map[string]any{"status": "cleared", "spreadsheet_id": id, "cleared_range": response.ClearedRange}, nil
}
