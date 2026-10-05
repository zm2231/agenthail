package daemon

import (
	"database/sql"
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
	if err := surface.ValidateRuntimeTransport(session); err != nil {
		writeAPIError(w, http.StatusConflict, "transport_unavailable", err.Error())
		return
	}
	adapter := d.surfaceForKind(session.Surface)
	if adapter == nil {
		writeAPIError(w, http.StatusConflict, "stream_unsupported", "This session does not expose a live stream.")
		return
	}
	if !surface.EffectiveCapabilities(session, adapter.Capabilities()).Stream {
		writeAPIError(w, http.StatusConflict, "stream_unsupported", "This session does not expose a live stream.")
		return
	}
	after, err := parseSessionStreamCursor(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_cursor", "The session stream cursor is invalid.")
		return
	}
	subscription, err := d.sources.subscribeContext(r.Context(), session, adapter)
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
	d.writeSessionStream(w, r, flusher, sessionID, after, window, subscription.Entries)
}

func (d *Daemon) writeSessionStream(w http.ResponseWriter, r *http.Request, flusher http.Flusher, sessionID string, after uint64, window registry.SessionJournalWindow, entries <-chan registry.SessionJournalEntry) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	watermark := window.LatestSeq
	if watermark < after {
		watermark = after
	}
	for _, entry := range window.Entries {
		if !d.apiEventStreamAuthorized(r) {
			return
		}
		if err := writeSessionStreamEntry(w, sessionID, entry); err != nil {
			return
		}
		if entry.Seq > watermark {
			watermark = entry.Seq
		}
	}
	flusher.Flush()
	keepalive := time.NewTicker(eventKeepalivePeriod)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case entry, open := <-entries:
			if !open {
				return
			}
			if entry.Seq <= watermark {
				continue
			}
			if !d.apiEventStreamAuthorized(r) {
				return
			}
			if err := writeSessionStreamEntry(w, sessionID, entry); err != nil {
				return
			}
			watermark = entry.Seq
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

func (d *Daemon) sessionStreamBodyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	id, ref := strings.TrimSpace(r.URL.Query().Get("id")), strings.TrimSpace(r.URL.Query().Get("ref"))
	start, err := strconv.Atoi(r.URL.Query().Get("start"))
	if err != nil || start < 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_range", "The body range is invalid.")
		return
	}
	end := start + sessionStreamBodyBytes
	if value := strings.TrimSpace(r.URL.Query().Get("end")); value != "" {
		end, err = strconv.Atoi(value)
		if err != nil || end < start || end-start > sessionStreamBodyBytes {
			writeAPIError(w, http.StatusBadRequest, "invalid_range", "The body range is invalid.")
			return
		}
	}
	body, total, err := d.Registry.SessionJournalBody(id, ref, start, end)
	if err == sql.ErrNoRows {
		writeAPIError(w, http.StatusNotFound, "body_unavailable", "The retained body is unavailable.")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_range", "The body range is invalid.")
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"sessionId": id, "bodyRef": ref, "start": start, "end": start + len(body), "total": total, "body": string(body), "truncated": start+len(body) < total})
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
