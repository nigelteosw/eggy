package agentrunner

import (
	"io"
	"sync"
	"unicode/utf8"
)

// outputTail is what a sandboxed command's output becomes. The model reads the
// end of it, where errors and summaries are, so only the last limit bytes are
// kept in memory. Every byte also goes to full, up to fullCap, so the model can
// search the rest with a narrower command.
type outputTail struct {
	mu    sync.Mutex
	limit int
	tail  []byte
	total int64

	full        io.Writer
	fullCap     int64
	fullWritten int64
	// fileIncomplete is set when the full copy stopped short: it hit fullCap,
	// or a write to it failed.
	fileIncomplete bool
}

// newOutputTail keeps the last limit bytes. A nil full writer keeps no copy.
func newOutputTail(limit int, full io.Writer, fullCap int64) *outputTail {
	return &outputTail{limit: limit, full: full, fullCap: fullCap}
}

func (o *outputTail) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.total += int64(len(p))
	o.tail = append(o.tail, p...)
	// Trim in batches rather than on every write, so a chatty command does not
	// copy its tail each time it prints.
	if len(o.tail) > 2*o.limit {
		o.tail = append([]byte(nil), o.tail[len(o.tail)-o.limit:]...)
	}
	if o.full != nil && !o.fileIncomplete {
		chunk := p
		if room := o.fullCap - o.fullWritten; int64(len(chunk)) > room {
			chunk = chunk[:room]
			o.fileIncomplete = true
		}
		n, err := o.full.Write(chunk)
		o.fullWritten += int64(n)
		if err != nil {
			o.fileIncomplete = true
		}
	}
	return len(p), nil
}

// result returns the kept end of the output and whether anything before it
// was dropped. A cut that splits a UTF-8 character is moved forward to the next
// whole character.
func (o *outputTail) result() (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	data := o.tail
	truncated := o.total > int64(o.limit)
	if len(data) > o.limit {
		data = data[len(data)-o.limit:]
	}
	if truncated {
		for i := 0; i < utf8.UTFMax && len(data) > 0 && !utf8.RuneStart(data[0]); i++ {
			data = data[1:]
		}
	}
	return string(data), truncated
}

// complete reports whether the full copy holds every byte written.
func (o *outputTail) complete() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return !o.fileIncomplete
}
