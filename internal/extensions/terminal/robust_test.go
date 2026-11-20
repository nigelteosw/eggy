package terminal

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// A viewer closing its stream while the core is still sending must not crash
// the process: finishing closes Output, and the reader must never send on it
// afterwards. Run with -race; it panicked with "send on closed channel".
func TestStreamCloseDuringOutputDoesNotPanic(t *testing.T) {
	for range 200 {
		var input bytes.Buffer
		for range 50 {
			if err := writeFrame(&input, frame{channel: 7, kind: kindData, payload: []byte("x")}); err != nil {
				t.Fatal(err)
			}
		}
		pr, pw := io.Pipe()
		c := newClient(pr, io.Discard, nil)
		s := c.openStream(7, false)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = pw.Write(input.Bytes())
			_ = pw.Close()
		}()
		_ = s.Close()
		wg.Wait()
		<-c.done
	}
}

// hungLauncher's core for "slow" never answers hello; others are fake cores.
type hungLauncher struct {
	fake *fakeLauncher
	slow string
}

type hungConn struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (h hungConn) Read(p []byte) (int, error)  { return h.r.Read(p) }
func (h hungConn) Write(p []byte) (int, error) { return len(p), nil }
func (h hungConn) Close() error {
	_ = h.r.CloseWithError(io.ErrClosedPipe)
	return h.w.Close()
}

func (l hungLauncher) Connect(dir string) (Conn, error) {
	if strings.HasSuffix(dir, l.slow) {
		r, w := io.Pipe()
		return hungConn{r: r, w: w}, nil
	}
	return l.fake.Connect(dir)
}

// One account's core that never answers holds up only that account, and is
// given up on after connectTimeout rather than waited on for ever.
func TestHungCoreBlocksOnlyItsAccount(t *testing.T) {
	fake := &fakeLauncher{t: t}
	svc, err := New(Options{
		Root:     t.TempDir(),
		DirName:  func(a string) string { return "acct-" + a },
		Prepare:  func(string) error { return nil },
		Launcher: hungLauncher{fake: fake, slow: "acct-slow"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx, cancel := context.WithTimeout(as("slow"), 300*time.Millisecond)
	defer cancel()
	slowDone := make(chan error, 1)
	go func() { _, err := svc.Terminals(ctx); slowDone <- err }()

	time.Sleep(50 * time.Millisecond) // the slow account is now inside connect
	fastDone := make(chan error, 1)
	go func() { _, err := svc.Terminals(as("fast")); fastDone <- err }()
	select {
	case err := <-fastDone:
		if err != nil {
			t.Fatalf("other account: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("one account's hung core blocked another account")
	}
	select {
	case err := <-slowDone:
		if err == nil {
			t.Fatal("a core that never answered was accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waited on a hung core past its deadline")
	}
}
