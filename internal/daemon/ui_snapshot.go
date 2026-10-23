package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var uiSnapshotTimeout = 20 * time.Second

const (
	uiSnapshotMaxBytes = 32 << 20
	uiSnapshotMaxSide  = 8192
)

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// Requests live only in memory: one that outlives its caller is useless.
type snapshotRelay struct {
	mu      sync.Mutex
	pending map[string]chan []byte
}

func newSnapshotRelay() *snapshotRelay {
	return &snapshotRelay{pending: map[string]chan []byte{}}
}

func (s *snapshotRelay) open() (string, chan []byte) {
	id := uuid.NewString()
	reply := make(chan []byte, 1)
	s.mu.Lock()
	s.pending[id] = reply
	s.mu.Unlock()
	return id, reply
}

func (s *snapshotRelay) close(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

func (s *snapshotRelay) deliver(id string, image []byte) bool {
	s.mu.Lock()
	reply, ok := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if ok {
		reply <- image
	}
	return ok
}

type uiSnapshotRequest struct {
	Session string `json:"session,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	Device  string `json:"device,omitempty"`
}

type uiSnapshotEvent struct {
	ID string `json:"id"`
	uiSnapshotRequest
	ExpiresAt int64 `json:"expiresAt"`
}

func (d *Daemon) apiUISnapshotHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	var request uiSnapshotRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, apiRequestBodyLimit)).Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "The request body must be JSON.")
		return
	}
	if (request.Width == 0) != (request.Height == 0) || request.Width < 0 || request.Height < 0 || request.Width > uiSnapshotMaxSide || request.Height > uiSnapshotMaxSide {
		writeAPIError(w, http.StatusBadRequest, "invalid_size", "Give both width and height, each from 1 to 8192 points, or neither.")
		return
	}
	id, reply := d.snapshots.open()
	defer d.snapshots.close(id)
	d.publishEvent("ui.snapshot.requested", "", uiSnapshotEvent{ID: id, uiSnapshotRequest: request, ExpiresAt: time.Now().Add(uiSnapshotTimeout).UnixMilli()})
	timer := time.NewTimer(uiSnapshotTimeout)
	defer timer.Stop()
	select {
	case image := <-reply:
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(image)
	case <-timer.C:
		message := "No Agenthail app answered. Open the Mac app on a computer connected to this daemon."
		if request.Device != "" {
			message = "The Mac app named " + request.Device + " did not answer. Check that it is open and connected to this daemon."
		}
		writeAPIError(w, http.StatusGatewayTimeout, "snapshot_unanswered", message)
	case <-r.Context().Done():
	}
}

func (d *Daemon) apiUISnapshotUploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/ui/snapshot/")
	if id == "" || strings.Contains(id, "/") {
		writeAPIError(w, http.StatusNotFound, "snapshot_not_found", "No snapshot request has that id.")
		return
	}
	image, err := io.ReadAll(io.LimitReader(r.Body, uiSnapshotMaxBytes+1))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "snapshot_unreadable", err.Error())
		return
	}
	if len(image) > uiSnapshotMaxBytes {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "snapshot_too_large", "A snapshot must be under 32 MB.")
		return
	}
	if !bytes.HasPrefix(image, pngSignature) {
		writeAPIError(w, http.StatusUnsupportedMediaType, "snapshot_not_png", "A snapshot must be a PNG image.")
		return
	}
	if !d.snapshots.deliver(id, image) {
		writeAPIError(w, http.StatusConflict, "snapshot_not_pending", "That snapshot was already answered or has expired.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
