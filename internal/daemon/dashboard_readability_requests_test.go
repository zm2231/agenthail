package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

const guardRequestHeader = "X-Agenthail-Guard-Request"

type guardRequest struct {
	path     string
	received time.Time
	headers  time.Time
	status   int
	done     time.Time
}

// guardRequests records, per request the guard tagged, how far the dashboard
// server got with it, so a stalled guard can say which side a request died on.
type guardRequests struct {
	mu   sync.Mutex
	seen map[string]*guardRequest
}

func (g *guardRequests) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(guardRequestHeader)
		if id == "" {
			next.ServeHTTP(w, r)
			return
		}
		record := &guardRequest{path: r.URL.RequestURI(), received: time.Now()}
		g.mu.Lock()
		if g.seen == nil {
			g.seen = map[string]*guardRequest{}
		}
		g.seen[id] = record
		g.mu.Unlock()
		next.ServeHTTP(&guardResponse{ResponseWriter: w, mark: func(status int) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if record.headers.IsZero() {
				record.headers, record.status = time.Now(), status
			}
		}}, r)
		g.mu.Lock()
		record.done = time.Now()
		g.mu.Unlock()
	})
}

func (g *guardRequests) describe(id string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	record := g.seen[id]
	switch {
	case record == nil:
		return "the daemon never received it"
	case record.headers.IsZero():
		return fmt.Sprintf("the daemon received %s %s ago and has not answered", record.path, time.Since(record.received).Round(time.Second))
	case record.done.IsZero():
		return fmt.Sprintf("the daemon answered %s with %d after %s and is still writing it", record.path, record.status, record.headers.Sub(record.received).Round(time.Millisecond))
	default:
		return fmt.Sprintf("the daemon answered %s with %d after %s and finished it %s ago", record.path, record.status, record.headers.Sub(record.received).Round(time.Millisecond), time.Since(record.done).Round(time.Second))
	}
}

type guardResponse struct {
	http.ResponseWriter
	mark func(status int)
}

func (w *guardResponse) WriteHeader(status int) {
	w.mark(status)
	w.ResponseWriter.WriteHeader(status)
}

func (w *guardResponse) Write(b []byte) (int, error) {
	w.mark(http.StatusOK)
	return w.ResponseWriter.Write(b)
}

func (w *guardResponse) Flush() {
	w.mark(http.StatusOK)
	http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *guardResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func reportStalledRequests(path string, servers map[string]*guardRequests) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var stuck []struct {
		Dashboard string `json:"dashboard"`
		ID        string `json:"id"`
		Path      string `json:"path"`
	}
	if err := json.Unmarshal(data, &stuck); err != nil {
		fmt.Printf("readability guard: unreadable stall file: %v\n", err)
		return
	}
	for _, request := range stuck {
		described := "no dashboard server has that address"
		if server := servers[request.Dashboard]; server != nil {
			described = server.describe(request.ID)
		}
		fmt.Printf("readability guard: request %s %s: %s\n", request.ID, request.Path, described)
	}
}
