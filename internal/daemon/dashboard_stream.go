package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) dashboardStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is unavailable", http.StatusInternalServerError)
		return
	}
	session, err := d.Registry.Session(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	adapter := d.surfaceForKind(session.Surface)
	if adapter == nil || !adapter.Capabilities().Stream {
		http.Error(w, "this session does not support live streaming", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	fmt.Fprint(w, "event: ready\ndata: {}\n\n")
	flusher.Flush()
	events := make(chan surface.StreamEvent, 32)
	errors := make(chan error, 1)
	go func() {
		streamErr := adapter.Stream(r.Context(), session, "", func(event surface.StreamEvent) {
			select {
			case events <- event:
			case <-r.Context().Done():
			}
		}, 30*time.Minute)
		close(events)
		errors <- streamErr
	}()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-events:
			if !open {
				streamErr := <-errors
				if streamErr != nil && r.Context().Err() == nil {
					payload, _ := json.Marshal(map[string]string{"error": streamErr.Error()})
					fmt.Fprintf(w, "event: stream-error\ndata: %s\n\n", payload)
					flusher.Flush()
				}
				return
			}
			payload, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: delta\ndata: %s\n\n", payload)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
