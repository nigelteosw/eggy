package telegram

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func newCountingClient() (*Client, *int32counter) {
	counter := &int32counter{}
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "sendChatAction") {
			counter.inc()
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`))}, nil
	})}
	return NewClient("https://api.telegram.test", "token", httpClient), counter
}

type int32counter struct {
	mu    sync.Mutex
	value int
}

func (c *int32counter) inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value++
}

func (c *int32counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

func TestStartTypingSendsImmediately(t *testing.T) {
	client, counter := newCountingClient()
	stop := StartTyping(context.Background(), client, "42", time.Hour)
	stop()
	if counter.get() != 1 {
		t.Fatalf("typing sends=%d", counter.get())
	}
}

func TestStartTypingRepeatsAtInterval(t *testing.T) {
	client, counter := newCountingClient()
	stop := StartTyping(context.Background(), client, "42", 5*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	stop()
	if counter.get() < 3 {
		t.Fatalf("expected multiple typing indicator sends within 30ms at a 5ms interval, got %d", counter.get())
	}
}

func TestStartTypingStopsSendingAfterStopReturns(t *testing.T) {
	client, counter := newCountingClient()
	stop := StartTyping(context.Background(), client, "42", 5*time.Millisecond)
	time.Sleep(12 * time.Millisecond)
	stop()
	countAtStop := counter.get()
	time.Sleep(20 * time.Millisecond)
	countAfter := counter.get()
	if countAfter != countAtStop {
		t.Fatalf("typing indicator kept firing after stop: before=%d after=%d", countAtStop, countAfter)
	}
}
