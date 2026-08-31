// Package tavily reaches the open web through Tavily's search and extract
// endpoints.
//
// The package deliberately holds both the HTTP client and the ports.Tool
// implementations, the same shape plugins/tools/google uses: bootstrap only
// decides whether the capability exists and registers what it gets back.
//
// Provider-specific types -- the request and response bodies Tavily speaks --
// stay inside search.go and extract.go and are never named in tools.go. That
// is the seam a second provider would be introduced at: the tools, the output
// bounding, the error surface and the wiring are already provider-neutral, so
// swapping means an interface over Search and Extract plus one new adapter,
// and nothing above it changes. The interface is not written now because one
// provider cannot tell you what the right one is -- the last attempt shipped
// a port for three providers and was deleted whole.
package tavily

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Config is Tavily's whole configuration surface.
type Config struct {
	APIKey string
	// APIKeyEnv names the variable the key came from, so a rejected key can
	// say which one to fix rather than leaving the owner to guess.
	APIKeyEnv string
	// BaseURL exists so tests can point at a local server. It is not reachable
	// from config: an operator-settable API host would let a config edit
	// redirect the owner's queries somewhere else.
	BaseURL        string
	SearchDepth    string
	ExtractDepth   string
	MaxResults     int
	MaxOutputBytes int
	Timeout        time.Duration
}

const (
	defaultBaseURL        = "https://api.tavily.com"
	defaultDepth          = "basic"
	defaultMaxResults     = 5
	defaultMaxOutputBytes = 65536
	defaultTimeout        = 30 * time.Second
	// maxErrorBodyBytes bounds what a provider error page can contribute to a
	// tool error. Without it a 500 that returns an HTML page becomes a context
	// problem on top of being a failure.
	maxErrorBodyBytes = 512
	// maxExtractURLs is below Tavily's own limit of 20 on purpose. Twenty
	// extracted pages in one turn is this tool's real failure mode, and five
	// is exactly one credit at basic depth. An owner who needs more calls the
	// tool twice.
	maxExtractURLs = 5
)

type Client struct {
	httpClient *http.Client
	config     Config
}

// New normalizes the config once so nothing downstream has to check for a
// zero value again.
func New(httpClient *http.Client, config Config) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if strings.TrimSpace(config.BaseURL) == "" {
		config.BaseURL = defaultBaseURL
	}
	config.BaseURL = strings.TrimSuffix(config.BaseURL, "/")
	if config.SearchDepth == "" {
		config.SearchDepth = defaultDepth
	}
	if config.ExtractDepth == "" {
		config.ExtractDepth = defaultDepth
	}
	if config.MaxResults <= 0 {
		config.MaxResults = defaultMaxResults
	}
	if config.MaxOutputBytes <= 0 {
		config.MaxOutputBytes = defaultMaxOutputBytes
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	return &Client{httpClient: httpClient, config: config}
}

// post performs one request and decodes the response into out.
//
// There is no retry loop. One call is one credit, so a retry the owner did not
// ask for is a charge they did not authorize -- and every status worth
// retrying is one the model can decide about itself.
func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("tavily %s: %w", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return c.statusError(path, response)
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// statusError turns Tavily's documented status codes into errors that say what
// the owner has to do about them. The distinction between 429, 432 and 433
// matters: one means wait, the others mean the plan or the balance ran out and
// waiting will not help.
func (c *Client) statusError(path string, response *http.Response) error {
	snippet, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))
	detail := strings.TrimSpace(string(snippet))
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("tavily rejected the API key in %s", c.config.APIKeyEnv)
	case http.StatusTooManyRequests:
		return fmt.Errorf("tavily rate limit exceeded; retry later")
	case 432:
		return fmt.Errorf("tavily plan limit reached; the key has no credits left on its plan")
	case 433:
		return fmt.Errorf("tavily pay-as-you-go limit reached")
	case http.StatusBadRequest:
		return fmt.Errorf("tavily rejected the request: %s", detail)
	default:
		return fmt.Errorf("tavily %s returned %d: %s", path, response.StatusCode, detail)
	}
}

// truncate cuts s to at most limit bytes, never splitting a rune, and reports
// whether it cut anything.
func truncate(s string, limit int) (string, bool) {
	if limit <= 0 {
		if s == "" {
			return "", false
		}
		return "", true
	}
	if len(s) <= limit {
		return s, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// shareOf divides the output budget evenly across results.
//
// Even shares rather than first-come packing is what keeps one enormous page
// from starving the others: a model that asked for five URLs wants five
// answers, not one article and four empty strings. Leftover budget from a
// short result is not redistributed -- the model can ask again for a specific
// URL, and that is cheaper than the bookkeeping.
func shareOf(budget, results int) int {
	if results <= 0 {
		return budget
	}
	return budget / results
}
