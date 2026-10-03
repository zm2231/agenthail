package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type sessionStreamEnvelope struct {
	Stream    string          `json:"stream"`
	SessionID string          `json:"sessionId"`
	Seq       uint64          `json:"seq"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
}

func (d *Daemon) apiSessionStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "stream_unavailable", "Streaming is unavailable.")
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("id"))
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "session_not_found", "The requested session was not found.")
		return
	}
	adapter := d.surfaceForKind(session.Surface)
	if adapter == nil || !surface.EffectiveCapabilities(session, adapter.Capabilities()).Stream {
		writeAPIError(w, http.StatusConflict, "stream_unsupported", "This session does not expose a live stream.")
		return
	}
	after, err := parseSessionStreamCursor(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_cursor", "The session stream cursor is invalid.")
		return
	}
	subscription, err := d.sources.subscribe(session, adapter)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "stream_unavailable", "The session source could not start.")
		return
	}
	defer subscription.Cancel()
	window, err := d.Registry.SessionJournalAfter(sessionID, after, sessionJournalRetentionCount)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "stream_unavailable", "The session journal could not be read.")
		return
	}
	if window.Gap {
		writeDashboardJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "stream_gap", "message": "The session stream cursor is no longer retained."}, "earliestSeq": window.EarliestSeq, "latestSeq": window.LatestSeq})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	seen := map[uint64]uint64{}
	for _, entry := range window.Entries {
		if !d.apiEventStreamAuthorized(r) {
			return
		}
		if err := writeSessionStreamEntry(w, sessionID, entry); err != nil {
			return
		}
		seen[entry.Seq] = sessionJournalVersion(entry)
	}
	flusher.Flush()
	keepalive := time.NewTicker(eventKeepalivePeriod)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case entry, open := <-subscription.Entries:
			if !open {
				return
			}
			version := sessionJournalVersion(entry)
			if prior, found := seen[entry.Seq]; found && version <= prior {
				continue
			}
			if !d.apiEventStreamAuthorized(r) {
				return
			}
			if err := writeSessionStreamEntry(w, sessionID, entry); err != nil {
				return
			}
			seen[entry.Seq] = version
			flusher.Flush()
		case <-keepalive.C:
			if !d.apiEventStreamAuthorized(r) {
				return
			}
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func parseSessionStreamCursor(r *http.Request) (uint64, error) {
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("after"))
	}
	if value == "" {
		return 0, nil
	}
	return strconv.ParseUint(value, 10, 64)
}

func writeSessionStreamEntry(w http.ResponseWriter, sessionID string, entry registry.SessionJournalEntry) error {
	payload := entry.Payload
	if !json.Valid(payload) {
		return fmt.Errorf("invalid session journal payload")
	}
	envelope, err := json.Marshal(sessionStreamEnvelope{Stream: "session", SessionID: sessionID, Seq: entry.Seq, Type: "item", Data: payload})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: item\ndata: %s\n\n", entry.Seq, envelope)
	return err
}

func sessionJournalVersion(entry registry.SessionJournalEntry) uint64 {
	var payload struct {
		Version uint64 `json:"version"`
	}
	if json.Unmarshal(entry.Payload, &payload) != nil {
		return 0
	}
	return payload.Version
}
