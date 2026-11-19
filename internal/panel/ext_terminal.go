package panel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

// TerminalService is the terminal extension as the panel sees it. The panel
// declares it and never imports the extension; bootstrap adapts the service.
// Every method acts as the account on the context, which the session guard put
// there, and each account's terminals live in its own core, so no route can
// name another account's terminal.
type TerminalService interface {
	Commands() []string
	Spaces(ctx context.Context) ([]TerminalSpace, error)
	CreateSpace(ctx context.Context, name string) error
	Start(ctx context.Context, space, command string, cols, rows uint16) (TerminalInfo, error)
	Kill(ctx context.Context, address string) error
	Attach(ctx context.Context, address string, cols, rows uint16) (TerminalStream, error)
	NeedsYou(ctx context.Context) ([]TerminalInfo, error)
}

// ErrTerminalNotFound is a terminal or space the calling account does not have.
var ErrTerminalNotFound = errors.New("terminal not found")

// TerminalInfo is one terminal as the browser sees it.
type TerminalInfo struct {
	Address  string    `json:"address"`
	Space    string    `json:"space"`
	Name     string    `json:"name"`
	Title    string    `json:"title"`
	Live     bool      `json:"live"`
	Status   string    `json:"status,omitempty"`
	Message  string    `json:"message,omitempty"`
	App      string    `json:"app,omitempty"`
	Changed  time.Time `json:"changed"`
	ExitCode *int      `json:"exit_code,omitempty"`
}

// TerminalSpace is a folder in the account's workspace.
type TerminalSpace struct {
	Name      string         `json:"name"`
	Terminals []TerminalInfo `json:"terminals"`
}

// TerminalStream is an attached terminal.
type TerminalStream interface {
	Output() <-chan []byte
	Reason() string
	Consumed(n int) error
	Write(p []byte) (int, error)
	Resize(cols, rows uint16) error
	Close() error
}

// terminalAddress is a terminal on the account's own core. A machine prefix
// ("box/t7") is refused: Eggy adds no machines, and no route may reach one.
var terminalAddress = regexp.MustCompile(`^t[0-9]{1,9}$`)

func mountTerminalRoutes(mux *http.ServeMux, guard func(http.HandlerFunc) http.Handler, webConfig WebUIConfig, svc TerminalService, revalidate func(*http.Request) bool) {
	upgrader := terminalUpgrader(webConfig)
	mux.Handle("GET /api/terminal/spaces", guard(terminalSpaces(svc)))
	mux.Handle("POST /api/terminal/spaces", guard(terminalCreateSpace(svc)))
	mux.Handle("GET /api/terminal/needs-you", guard(terminalNeedsYou(svc)))
	mux.Handle("POST /api/terminal/terminals", guard(terminalStart(svc)))
	mux.Handle("DELETE /api/terminal/terminals/{address}", guard(terminalKill(svc)))
	mux.Handle("GET /api/terminal/terminals/{address}/attach", guard(terminalAttach(svc, upgrader, revalidate)))
}

func writeTerminalError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrTerminalNotFound) {
		writeWebError(w, http.StatusNotFound, "No such terminal or space.")
		return
	}
	writeWebError(w, http.StatusBadGateway, err.Error())
}

func terminalSpaces(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spaces, err := svc.Spaces(r.Context())
		if err != nil {
			writeTerminalError(w, err)
			return
		}
		writeJSON(w, map[string]any{"spaces": spaces, "commands": svc.Commands()})
	}
}

func terminalCreateSpace(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if err := decodeStrictJSONBody(w, r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := svc.CreateSpace(r.Context(), body.Name); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"name": body.Name})
	}
}

func terminalNeedsYou(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ts, err := svc.NeedsYou(r.Context())
		if err != nil {
			writeTerminalError(w, err)
			return
		}
		writeJSON(w, map[string]any{"terminals": ts})
	}
}

func terminalStart(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// A command is a name from a fixed list, never a command line.
		var body struct {
			Space   string `json:"space"`
			Command string `json:"command"`
			Cols    uint16 `json:"cols"`
			Rows    uint16 `json:"rows"`
		}
		if err := decodeStrictJSONBody(w, r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		t, err := svc.Start(r.Context(), body.Space, body.Command, body.Cols, body.Rows)
		if err != nil {
			writeTerminalError(w, err)
			return
		}
		writeJSON(w, t)
	}
}

func terminalKill(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		address := r.PathValue("address")
		if !terminalAddress.MatchString(address) {
			writeTerminalError(w, ErrTerminalNotFound)
			return
		}
		if err := svc.Kill(r.Context(), address); err != nil {
			writeTerminalError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// terminalUpgrader opens a terminal socket only for a page on the panel's own
// origin. An upgrade is a GET, so the session guard's CSRF check does not run;
// typing into a terminal is still a write, so the origin must be present and
// the panel's, by the same rule the guard applies to every other write. With
// the SameSite=Lax session cookie, which a cross-site socket does not carry,
// another site cannot reach a terminal.
func terminalUpgrader(webConfig WebUIConfig) *websocket.Upgrader {
	return &websocket.Upgrader{
		ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10,
		CheckOrigin: func(r *http.Request) bool {
			return r.Header.Get("Origin") != "" && sameOrigin(webConfig, r)
		},
	}
}

const (
	// terminalMaxInput bounds one message from the browser: a paste, or a
	// control message.
	terminalMaxInput = 1 << 20
	// terminalRecheck is how often an open terminal re-checks the session, so
	// a sign-out or an expired session ends it, as the chat stream does.
	terminalRecheck = 30 * time.Second
)

// terminalControl is a text message from the browser. Keys travel as binary.
type terminalControl struct {
	Resize []uint16 `json:"resize"`
}

func terminalAttach(svc TerminalService, upgrader *websocket.Upgrader, revalidate func(*http.Request) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		address := r.PathValue("address")
		if !terminalAddress.MatchString(address) {
			writeTerminalError(w, ErrTerminalNotFound)
			return
		}
		cols, _ := strconv.ParseUint(r.URL.Query().Get("cols"), 10, 16)
		rows, _ := strconv.ParseUint(r.URL.Query().Get("rows"), 10, 16)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		// Attach before upgrading, so a terminal the account does not have is
		// an ordinary 404 rather than a socket that opens and closes.
		stream, err := svc.Attach(ctx, address, uint16(cols), uint16(rows))
		if err != nil {
			writeTerminalError(w, err)
			return
		}
		defer stream.Close()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return // the upgrader has answered
		}
		defer conn.Close()
		conn.SetReadLimit(terminalMaxInput)

		// One writer: output, the periodic session check, and the final reason.
		go func() {
			defer cancel()
			recheck := time.NewTicker(terminalRecheck)
			defer recheck.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-recheck.C:
					if revalidate != nil && !revalidate(r) {
						_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "signed out"), time.Now().Add(time.Second))
						return
					}
					_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				case chunk, ok := <-stream.Output():
					if !ok {
						reason, _ := json.Marshal(map[string]any{"closed": closeReason(stream.Reason())})
						_ = conn.WriteMessage(websocket.TextMessage, reason)
						_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
						return
					}
					_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
					if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
						return
					}
					_ = stream.Consumed(len(chunk))
				}
			}
		}()

		for {
			kind, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if ctx.Err() != nil {
				return
			}
			switch kind {
			case websocket.BinaryMessage:
				if _, err := stream.Write(msg); err != nil {
					return
				}
			case websocket.TextMessage:
				var control terminalControl
				if json.Unmarshal(msg, &control) == nil && len(control.Resize) == 2 && control.Resize[0] > 0 && control.Resize[1] > 0 {
					_ = stream.Resize(control.Resize[0], control.Resize[1])
				}
			}
		}
	}
}

// terminalClosed is what the browser learns about why a stream ended: the
// core's reason word and exit code, and nothing else it might have said.
type terminalClosed struct {
	Reason   string `json:"reason"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

func closeReason(raw string) terminalClosed {
	var out terminalClosed
	if json.Unmarshal([]byte(raw), &out) != nil || out.Reason == "" {
		out.Reason = "ended"
	}
	return out
}
