package terminal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// protocolGeneration is the herdr-core protocol this client was written
// against. A core reporting another is refused rather than half-understood.
const protocolGeneration = 1

// maxEarlyFrames bounds what is held for an unregistered channel.
const maxEarlyFrames = 64

// errClosed is returned once the connection to the core has ended.
var errClosed = errors.New("terminal: connection to herdr-core closed")

// client speaks herdr-core's framed JSON-RPC over one connection, the stdin
// and stdout of `hc connect`. Everything the core sends is data from a program
// the owner runs, so nothing here acts on it beyond routing.
type client struct {
	w   io.Writer
	wmu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcReply
	streams map[uint32]*stream
	// early holds frames for a channel whose attach reply has been routed but
	// whose stream is not registered yet: the reply and the first paint can
	// both be read before the caller gets the channel number.
	early  map[uint32][][]byte
	events func(eventNotice)
	err    error
	done   chan struct{}
}

type rpcReply struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Kind string `json:"kind"`
	} `json:"data"`
}

func (e *rpcError) Error() string { return "herdr-core: " + e.Message }

// eventNotice is one notification from an event subscription.
type eventNotice struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// stream is one attached terminal: output arrives on out, and closing it
// tells the core the viewer has gone.
type stream struct {
	c       *client
	channel uint32
	out     chan []byte
	reason  chan string
	once    sync.Once
	// blocking streams are read by Eggy itself (events), never dropped.
	blocking bool
}

// newClient starts reading frames from r. events receives every event-channel
// notification; it must not block.
func newClient(r io.Reader, w io.Writer, events func(eventNotice)) *client {
	c := &client{w: w, pending: map[int64]chan rpcReply{}, streams: map[uint32]*stream{}, early: map[uint32][][]byte{}, events: events, done: make(chan struct{})}
	go c.readLoop(r)
	return c
}

// hello starts plain mode, asks for framed mode, and checks the generation.
// It is the one plain-mode exchange: one JSON line out, one back.
func hello(r *bufio.Reader, w io.Writer) error {
	req := map[string]any{"jsonrpc": "2.0", "id": 0, "method": "hello", "params": map[string]any{"framed": true, "generation": protocolGeneration}}
	line, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(line, '\n')); err != nil {
		return err
	}
	reply, err := r.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("herdr-core hello: %w", err)
	}
	var answer struct {
		Result struct {
			Generation int  `json:"generation"`
			Framed     bool `json:"framed"`
		} `json:"result"`
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(reply, &answer); err != nil {
		return fmt.Errorf("herdr-core hello: %w", err)
	}
	if answer.Error != nil {
		return answer.Error
	}
	if answer.Result.Generation != protocolGeneration {
		return fmt.Errorf("herdr-core speaks protocol generation %d; this Eggy needs %d", answer.Result.Generation, protocolGeneration)
	}
	if !answer.Result.Framed {
		return errors.New("herdr-core refused framed mode")
	}
	return nil
}

func (c *client) send(f frame) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return writeFrame(c.w, f)
}

// call makes one JSON-RPC call on channel 0 and decodes its result into out.
func (c *client) call(ctx context.Context, method string, params, out any) error {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.nextID++
	id := c.nextID
	reply := make(chan rpcReply, 1)
	c.pending[id] = reply
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := c.send(frame{channel: 0, kind: kindData, payload: body}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.closedErr()
	case r := <-reply:
		if r.Error != nil {
			return r.Error
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(r.Result, out)
	}
}

func (c *client) closedErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// openStream registers channel before any of its frames can be routed. The
// core sends the attach reply before stream frames, and frames are read in
// order, so registering on the reply is in time.
func (c *client) openStream(channel uint32, blocking bool) *stream {
	s := &stream{c: c, channel: channel, blocking: blocking, out: make(chan []byte, 64), reason: make(chan string, 1)}
	c.mu.Lock()
	c.streams[channel] = s
	backlog := c.early[channel]
	delete(c.early, channel)
	for _, p := range backlog {
		s.deliver(p)
	}
	c.mu.Unlock()
	return s
}

func (c *client) readLoop(r io.Reader) {
	err := func() error {
		for {
			f, err := readFrame(r)
			if err != nil {
				return err
			}
			switch {
			case f.channel == 0 && f.kind == kindData:
				c.routeReply(f.payload)
			case f.kind == kindData:
				c.mu.Lock()
				s := c.streams[f.channel]
				if s == nil && len(c.early[f.channel]) < maxEarlyFrames {
					c.early[f.channel] = append(c.early[f.channel], f.payload)
				}
				c.mu.Unlock()
				if s != nil {
					s.deliver(f.payload)
				}
			case f.kind == kindClose:
				c.mu.Lock()
				s := c.streams[f.channel]
				delete(c.streams, f.channel)
				delete(c.early, f.channel)
				c.mu.Unlock()
				if s != nil {
					s.finish(string(f.payload))
				}
			}
			// Another kind from the core (a RESIZE we did not ask for) is ignored.
		}
	}()
	c.mu.Lock()
	if errors.Is(err, io.EOF) || err == nil {
		err = errClosed
	}
	c.err = err
	streams := c.streams
	c.streams = map[uint32]*stream{}
	c.mu.Unlock()
	close(c.done)
	for _, s := range streams {
		s.finish(`{"reason":"disconnected"}`)
	}
}

// subscribe starts the event subscription and returns its snapshot. Its
// channel is read as JSON lines and handed to the events callback; the event
// stream is never dropped for being slow, since the callback does not block.
func (c *client) subscribe(ctx context.Context) (json.RawMessage, error) {
	var result struct {
		Channel *uint32 `json:"channel"`
	}
	var raw json.RawMessage
	if err := c.call(ctx, "events.subscribe", map[string]any{}, &raw); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.Channel == nil {
		return nil, errors.New("herdr-core gave no event channel")
	}
	// The event channel is read like a stream whose bytes are JSON lines.
	s := c.openStream(*result.Channel, true)
	go func() {
		var buf bytes.Buffer
		for chunk := range s.out {
			buf.Write(chunk)
			c.drainEvents(&buf)
			_ = c.send(frame{channel: s.channel, kind: kindCredit, payload: creditPayload(len(chunk))})
		}
	}()
	return raw, nil
}

func (c *client) drainEvents(buf *bytes.Buffer) {
	for {
		line, err := buf.ReadBytes('\n')
		if err != nil {
			// An incomplete line waits for the rest.
			buf.Reset()
			buf.Write(line)
			return
		}
		var n eventNotice
		if json.Unmarshal(bytes.TrimSpace(line), &n) == nil && n.Method != "" && c.events != nil {
			c.events(n)
		}
	}
}

func (c *client) routeReply(payload []byte) {
	var msg struct {
		ID *int64 `json:"id"`
		rpcReply
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(payload, &msg) != nil {
		return
	}
	if msg.ID == nil {
		if msg.Method != "" && c.events != nil {
			c.events(eventNotice{Method: msg.Method, Params: msg.Params})
		}
		return
	}
	c.mu.Lock()
	reply := c.pending[*msg.ID]
	c.mu.Unlock()
	if reply != nil {
		reply <- msg.rpcReply
	}
}

// deliver hands output to the viewer. A viewer that stops reading is dropped
// rather than allowed to stall every other stream: the core repaints a viewer
// that falls behind, so nothing it needs is lost by reattaching.
func (s *stream) deliver(payload []byte) {
	if s.blocking {
		s.out <- payload
		return
	}
	select {
	case s.out <- payload:
	default:
		s.Close()
	}
}

func (s *stream) finish(reason string) {
	s.once.Do(func() {
		s.reason <- reason
		close(s.out)
	})
}

// Output is the terminal's bytes: first paint, then live output. It closes
// when the stream ends; Reason then says why.
func (s *stream) Output() <-chan []byte { return s.out }

// Reason is the core's close reason, available once Output has closed.
func (s *stream) Reason() string {
	select {
	case r := <-s.reason:
		return r
	default:
		return ""
	}
}

// Consumed tells the core n bytes of output were written to the viewer, which
// is what lets it send more.
func (s *stream) Consumed(n int) error {
	return s.c.send(frame{channel: s.channel, kind: kindCredit, payload: creditPayload(n)})
}

// Write sends keys to the program.
func (s *stream) Write(p []byte) (int, error) {
	if err := s.c.send(frame{channel: s.channel, kind: kindData, payload: append([]byte(nil), p...)}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize sets the viewer's size.
func (s *stream) Resize(cols, rows uint16) error {
	return s.c.send(frame{channel: s.channel, kind: kindResize, payload: resizePayload(cols, rows)})
}

// Close detaches. The program keeps running.
func (s *stream) Close() error {
	s.c.mu.Lock()
	_, open := s.c.streams[s.channel]
	delete(s.c.streams, s.channel)
	s.c.mu.Unlock()
	s.finish(`{"reason":"detached"}`)
	if !open {
		return nil
	}
	return s.c.send(frame{channel: s.channel, kind: kindClose})
}
