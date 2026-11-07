package daemon

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// stalledClient is an event stream reader that takes nothing until it is
// released, like a phone whose connection stalls during a burst.
type stalledClient struct {
	header  http.Header
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	body    bytes.Buffer
}

func newStalledClient() *stalledClient {
	return &stalledClient{header: http.Header{}, started: make(chan struct{}), release: make(chan struct{})}
}

func (c *stalledClient) Header() http.Header { return c.header }
func (c *stalledClient) WriteHeader(int)     {}
func (c *stalledClient) Flush()              {}

func (c *stalledClient) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.started) })
	<-c.release
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body.Write(p)
}

func (c *stalledClient) received() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body.String()
}

// openStalledStream opens /api/v1/events for a client that stalls at its
// first byte. ended closes when the daemon ends the stream.
func openStalledStream(t *testing.T, d *Daemon) (client *stalledClient, ended <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer secret")
	client = newStalledClient()
	done := make(chan struct{})
	go func() {
		defer close(done)
		dashboardRouter(d).ServeHTTP(client, request)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-client.release:
		default:
			close(client.release)
		}
		<-done
	})
	select {
	case <-client.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the event stream never opened")
	}
	return client, done
}

func publishBurst(t *testing.T, d *Daemon, n int) (last apiEvent) {
	t.Helper()
	for index := range n {
		event, err := d.events.publish("test.burst", strconv.Itoa(index), nil)
		if err != nil {
			t.Fatal(err)
		}
		last = event
	}
	return last
}

func awaitReceived(t *testing.T, client *stalledClient, ended <-chan struct{}, want string) string {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		if body := client.received(); strings.Contains(body, want) {
			return body
		}
		select {
		case <-ended:
			t.Fatalf("the daemon ended the stream before sending %q; it sent %d bytes", want, len(client.received()))
		case <-deadline:
			t.Fatalf("the stream never sent %q", want)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestAStalledEventStreamGetsEveryEventOfABurst(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	client, ended := openStalledStream(t, d)
	first := d.events.cursor() + 1
	last := publishBurst(t, d, 200)
	close(client.release)
	body := awaitReceived(t, client, ended, "id: "+strconv.FormatUint(last.ID, 10)+"\n")
	position := 0
	for id := first; id <= last.ID; id++ {
		next := strings.Index(body[position:], "id: "+strconv.FormatUint(id, 10)+"\n")
		if next < 0 {
			t.Fatalf("event %d is missing or out of order", id)
		}
		position += next
	}
	after := publishBurst(t, d, 1)
	awaitReceived(t, client, ended, "id: "+strconv.FormatUint(after.ID, 10)+"\n")
}

func TestAStreamThatFallsBehindTheHistoryIsToldToRereadAndKeepsFollowing(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	client, ended := openStalledStream(t, d)
	publishBurst(t, d, eventHistoryLimit+10)
	close(client.release)
	awaitReceived(t, client, ended, "event: stream.reset\n")
	after := publishBurst(t, d, 1)
	awaitReceived(t, client, ended, "id: "+strconv.FormatUint(after.ID, 10)+"\n")
}
