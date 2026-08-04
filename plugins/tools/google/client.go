package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

// Config is the whole of Google's configuration surface. One client, one set
// of scopes, one on/off switch -- the six products are not six settings,
// because they are not six grants.
type Config struct {
	ClientID       string
	ClientSecret   string
	Scopes         []string
	Timeout        time.Duration
	MaxOutputBytes int64
}

// Endpoints exists so tests can point every product at one local server. It is
// not reachable from config: an operator-settable API host would let a config
// edit redirect an owner's mail to somewhere else.
// DriveUpload is a separate host path from Drive, not a suffix of it: Google
// serves uploads from /upload/drive/v3 and metadata from /drive/v3, and posting
// a multipart body to the metadata endpoint silently creates an empty file.
type Endpoints struct{ Gmail, Calendar, Drive, DriveUpload, Docs, Sheets, People string }

func defaultEndpoints() Endpoints {
	return Endpoints{
		Gmail:       "https://gmail.googleapis.com/gmail/v1",
		Calendar:    "https://www.googleapis.com/calendar/v3",
		Drive:       "https://www.googleapis.com/drive/v3",
		DriveUpload: "https://www.googleapis.com/upload/drive/v3",
		Docs:        "https://docs.googleapis.com/v1",
		Sheets:      "https://sheets.googleapis.com/v4",
		People:      "https://people.googleapis.com/v1",
	}
}

// Workspace is the product surface: every call borrows the one grant Auth
// holds. Adding a product here adds requests, never a second authorization or
// a second consent screen -- the property the per-server MCP path could not
// offer.
type Workspace struct {
	auth      *Auth
	endpoints Endpoints
	timeout   time.Duration
	maxOutput int64
	// warmup is the one piece of state a product needs to keep between calls:
	// People's contact search reads a cache that has to be warmed first.
	warmup searchWarmup
}

const (
	defaultTimeout   = 30 * time.Second
	defaultMaxOutput = 131072
)

func NewWorkspace(auth *Auth, config Config) *Workspace {
	workspace := &Workspace{auth: auth, endpoints: defaultEndpoints(), timeout: config.Timeout, maxOutput: config.MaxOutputBytes}
	if workspace.timeout <= 0 {
		workspace.timeout = defaultTimeout
	}
	if workspace.maxOutput <= 0 {
		workspace.maxOutput = defaultMaxOutput
	}
	return workspace
}

// call performs one authorized request and decodes the response into out.
//
// Every product method funnels through here so the timeout, the output bound,
// and the error shape are decided once. The body is bounded because a model
// reads whatever comes back: an unbounded Drive listing or a long thread would
// otherwise spend the turn's context on one tool result.
func (w *Workspace) call(ctx context.Context, method, endpoint string, query url.Values, body, out any) error {
	client, err := w.auth.Client(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	if len(query) > 0 {
		separator := "?"
		if strings.Contains(endpoint, "?") {
			separator = "&"
		}
		endpoint += separator + query.Encode()
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, ErrNotAuthorized) {
			return ErrNotAuthorized
		}
		return fmt.Errorf("Google request failed: %w", err)
	}
	defer response.Body.Close()
	bounded := io.LimitReader(response.Body, w.maxOutput)
	raw, err := io.ReadAll(bounded)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return googleError(response.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		// A truncated body is the likely cause when the response filled the
		// bound exactly, and "unexpected end of JSON" would send the owner
		// looking at Google instead of at max_output_bytes.
		if int64(len(raw)) >= w.maxOutput {
			return fmt.Errorf("Google response exceeded google.max_output_bytes (%d); narrow the request", w.maxOutput)
		}
		return err
	}
	return nil
}

// callRaw is call for the two endpoints that do not answer in JSON: Drive's
// media download and its export. It returns the bytes and the content type,
// bounded the same way, and reports truncation rather than handing back a
// prefix that reads like a complete file.
func (w *Workspace) callRaw(ctx context.Context, endpoint string, query url.Values) ([]byte, string, error) {
	client, err := w.auth.Client(ctx)
	if err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, ErrNotAuthorized) {
			return nil, "", ErrNotAuthorized
		}
		return nil, "", fmt.Errorf("Google request failed: %w", err)
	}
	defer response.Body.Close()
	// One byte past the bound, so filling it exactly is distinguishable from
	// landing on it: a file whose size happens to equal the limit is complete.
	raw, err := io.ReadAll(io.LimitReader(response.Body, w.maxOutput+1))
	if err != nil {
		return nil, "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", googleError(response.StatusCode, raw)
	}
	if int64(len(raw)) > w.maxOutput {
		return nil, "", fmt.Errorf("that file is larger than google.max_output_bytes (%d); open it in Drive instead", w.maxOutput)
	}
	return raw, response.Header.Get("Content-Type"), nil
}

// upload posts a multipart/related body: JSON metadata first, content second.
// Drive's simple upload (uploadType=media) cannot carry a name or a parent, so
// every create here is multipart even when the metadata is one field.
//
// The 5 MB ceiling is Google's for this upload type. Nothing here approaches
// it -- content arrives as a string in a tool call -- so the resumable protocol
// would be several hundred lines serving a case that cannot occur.
func (w *Workspace) upload(ctx context.Context, metadata map[string]any, contentType string, content []byte, out any) error {
	client, err := w.auth.Client(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writePart(writer, "application/json; charset=UTF-8", encoded); err != nil {
		return err
	}
	if err := writePart(writer, contentType, content); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	endpoint := w.endpoints.DriveUpload + "/files?uploadType=multipart&fields=id,name,mimeType,webViewLink"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return err
	}
	// related, not form-data: Drive reads the parts positionally, and the
	// boundary has to be the one the writer actually used.
	request.Header.Set("Content-Type", "multipart/related; boundary="+writer.Boundary())
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, ErrNotAuthorized) {
			return ErrNotAuthorized
		}
		return fmt.Errorf("Google request failed: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, w.maxOutput))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return googleError(response.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func writePart(writer *multipart.Writer, contentType string, content []byte) error {
	part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {contentType}})
	if err != nil {
		return err
	}
	_, err = part.Write(content)
	return err
}

// googleError surfaces the API's own message. A 403 from a scope the consent
// screen dropped and a 403 from an API that was never enabled in the Cloud
// project read identically as a status code and not at all alike as a repair.
func googleError(status int, raw []byte) error {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Error.Message != "" {
		if status == http.StatusUnauthorized {
			return fmt.Errorf("%w: %s", ErrNotAuthorized, envelope.Error.Message)
		}
		return fmt.Errorf("Google API error %d: %s", status, envelope.Error.Message)
	}
	if status == http.StatusUnauthorized {
		return ErrNotAuthorized
	}
	return fmt.Errorf("Google API returned HTTP %d", status)
}
