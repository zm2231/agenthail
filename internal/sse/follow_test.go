package sse

import (
	"context"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
)

// wedgedClient is a stream whose reader has stopped: a write blocks until the
// connection's write deadline passes, as on a socket whose buffers are full.
type wedgedClient struct {
	header   http.Header
	mu       sync.Mutex
	deadline time.Time
	set      chan struct{}
	stop     chan struct{}
}

func (c *wedgedClient) Header() http.Header { return c.header }
func (c *wedgedClient) WriteHeader(int)     {}
func (c *wedgedClient) Flush()              {}

func (c *wedgedClient) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = deadline
	select {
	case c.set <- struct{}{}:
	default:
	}
	return nil
}

func (c *wedgedClient) Write([]byte) (int, error) {
	for {
		c.mu.Lock()
		deadline := c.deadline
		c.mu.Unlock()
		var expired <-chan time.Time
		if !deadline.IsZero() {
			expired = time.After(time.Until(deadline))
		}
		select {
		case <-expired:
			return 0, os.ErrDeadlineExceeded
		case <-c.set:
		case <-c.stop:
			return 0, os.ErrClosed
		}
	}
}

func TestAStreamWhoseClientStopsReadingEnds(t *testing.T) {
	keepalive, wait := keepalivePeriod, writeWait
	keepalivePeriod, writeWait = 10*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { keepalivePeriod, writeWait = keepalive, wait })
	client := &wedgedClient{header: http.Header{}, set: make(chan struct{}, 1), stop: make(chan struct{})}
	t.Cleanup(func() { close(client.stop) })
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		Follow(context.Background(), client, make(chan struct{}), func(struct{}, bool) bool { return true }, func() bool { return true })
	}()
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("a stream whose client stopped reading was never ended")
	}
}
