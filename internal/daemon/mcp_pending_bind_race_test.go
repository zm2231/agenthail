package daemon

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// Codex connects during thread/start, racing its token's binding; a 401 makes it drop the server.
func TestACodexThreadsHandshakesWhileItsTokenBindsAreNeverRefused(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	server := mcpServer(t, d)
	handshake := func(token string) (int, error) {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/mcp", strings.NewReader(mcpInitialize))
		if err != nil {
			return 0, err
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return 0, err
		}
		response.Body.Close()
		return response.StatusCode, nil
	}
	const rounds, connectors = 40, 4
	for round := range rounds {
		thread := surface.Session{ID: fmt.Sprintf("thread-%d", round), Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Status: surface.StatusIdle}
		if err := r.RecordObservedSession(thread); err != nil {
			t.Fatal(err)
		}
		pending, err := r.IssuePendingCredential(nil, time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		var bound atomic.Bool
		var wg sync.WaitGroup
		failures := make(chan string, connectors)
		for range connectors {
			wg.Go(func() {
				for after := 0; after < 3; {
					if bound.Load() {
						after++
					}
					status, err := handshake(pending.Token)
					if err != nil || status != http.StatusOK {
						failures <- fmt.Sprintf("status=%d err=%v", status, err)
						return
					}
				}
			})
		}
		time.Sleep(time.Duration(round%5) * time.Millisecond)
		if claimed, err := r.ClaimPendingCredential(pending.Ref, thread.ID, time.Now()); err != nil || !claimed {
			t.Fatalf("claimed=%v err=%v", claimed, err)
		}
		bound.Store(true)
		wg.Wait()
		close(failures)
		for failure := range failures {
			t.Fatalf("round %d: a handshake while the token bound was refused: %s", round, failure)
		}
	}
}
