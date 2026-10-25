package daemon

import (
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/sse"
)

func (d *Daemon) relayHostStream(w http.ResponseWriter, r *http.Request, host *pairedHost, query url.Values) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "stream_unavailable", "Streaming is unavailable.")
		return
	}
	header := http.Header{"Accept": []string{"text/event-stream"}}
	if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
		header.Set("Last-Event-ID", lastID)
	}
	response, err := host.client.Open(r.Context(), apiPath(r.URL.Path), query, header)
	if err != nil {
		writeHostReadError(w, host, err)
		return
	}
	if response.StatusCode != http.StatusOK {
		writeHostResponse(w, host, response)
		return
	}
	defer response.Body.Close()
	sse.SetHeaders(w.Header())
	w.WriteHeader(http.StatusOK)
	reader := sse.NewReader(response.Body)
	for {
		event, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			return
		}
		if event.HasData {
			if qualified, err := hosts.QualifyJSON(host.record.ID, []byte(event.Data)); err == nil {
				event.Data = string(qualified)
			}
		}
		if err := sse.WriteEvent(w, event); err != nil {
			return
		}
		flusher.Flush()
	}
}
