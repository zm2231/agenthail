package daemon

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
)

// stalledSocket is a client connection whose reader has stopped: a write
// waits until the test lets time pass, then fails only if the server set a
// write deadline, as a socket with full buffers does.
type stalledSocket struct {
	header   http.Header
	mu       sync.Mutex
	deadline time.Time
	blocked  chan struct{}
	once     sync.Once
	elapse   chan struct{}
	closed   chan struct{}
}

func (s *stalledSocket) Header() http.Header { return s.header }
func (s *stalledSocket) WriteHeader(int)     {}
func (s *stalledSocket) Flush()              {}

func (s *stalledSocket) SetWriteDeadline(deadline time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deadline = deadline
	return nil
}

func (s *stalledSocket) Write([]byte) (int, error) {
	s.once.Do(func() { close(s.blocked) })
	select {
	case <-s.elapse:
	case <-s.closed:
		return 0, os.ErrClosed
	}
	s.mu.Lock()
	bounded := !s.deadline.IsZero()
	s.mu.Unlock()
	if bounded {
		return 0, os.ErrDeadlineExceeded
	}
	<-s.closed
	return 0, os.ErrClosed
}

func TestAPairedHostStreamToAClientThatStoppedReadingEnds(t *testing.T) {
	local, _, record := pairedFederation(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session-stream?id="+url.QueryEscape(hosts.Qualify(record.ID, "remote-a"))+"&after=0", nil).WithContext(t.Context())
	request.Header.Set("Authorization", "Bearer secret")
	client := &stalledSocket{header: http.Header{}, blocked: make(chan struct{}), elapse: make(chan struct{}), closed: make(chan struct{})}
	t.Cleanup(func() { close(client.closed) })
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		local.handler.ServeHTTP(client, request)
	}()
	select {
	case <-client.blocked:
	case <-ended:
		t.Fatal("the relayed stream ended before it wrote")
	case <-time.After(10 * time.Second):
		t.Fatal("the relayed stream never wrote")
	}
	close(client.elapse)
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the relayed stream held a client that stopped reading")
	}
}
