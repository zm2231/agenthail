package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/uploads"
)

const maxUploadsPerSend = 8

// uploadsDir sits beside the registry, so it is ~/.agenthail/uploads for the
// installed daemon and private to each test registry.
func uploadsDir(reg *registry.Registry) string {
	path := reg.Path()
	if index := strings.IndexByte(path, '?'); index >= 0 {
		path = path[:index]
	}
	return filepath.Join(filepath.Dir(path), "uploads")
}

func (d *Daemon) sweepUploads(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := d.uploads.Sweep(); err != nil {
			d.log.Printf("sweep uploads: %s", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// apiUploadHandler stores a file a person attaches to a message for the
// session it names. hostRouted has already forwarded a paired host's session,
// so the file always lands on the daemon whose agent will read it.
func (d *Daemon) apiUploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
	if sessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "upload_query_invalid", "sessionId is required.")
		return
	}
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "session_not_found", "The requested session was not found.")
		return
	}
	adapter, err := d.adapters().For(session)
	if err != nil || !adapter.Capabilities().Attach {
		writeAPIError(w, http.StatusConflict, "attach_unsupported", "This session cannot take attached files.")
		return
	}
	upload, err := d.uploads.Put(r.URL.Query().Get("name"), http.MaxBytesReader(w, r.Body, uploads.MaxFileBytes+1))
	if err != nil {
		writeUploadError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"upload": uploadView(upload)})
}

func writeUploadError(w http.ResponseWriter, err error) {
	var maxBytes *http.MaxBytesError
	switch {
	case errors.Is(err, uploads.ErrTooLarge), errors.As(err, &maxBytes):
		writeAPIError(w, http.StatusRequestEntityTooLarge, "upload_too_large", fmt.Sprintf("Images can be up to %d MiB and other files up to %d MiB.", uploads.MaxImageBytes>>20, uploads.MaxFileBytes>>20))
	case errors.Is(err, uploads.ErrEmpty):
		writeAPIError(w, http.StatusBadRequest, "upload_empty", "The file is empty.")
	default:
		writeAPIError(w, http.StatusInternalServerError, "upload_failed", "The file could not be stored.")
	}
}

// uploadView is what a client sees: never the stored path, which belongs to
// the daemon's machine.
func uploadView(upload surface.Upload) map[string]any {
	view := map[string]any{"id": upload.ID, "name": upload.Name, "mediaType": upload.MediaType, "bytes": upload.Bytes}
	if upload.Width > 0 {
		view["width"], view["height"] = upload.Width, upload.Height
	}
	return view
}

// resolveUploads turns the ids a send names into stored files.
func (d *Daemon) resolveUploads(ids []string) (surface.Uploads, error) {
	if len(ids) > maxUploadsPerSend {
		return nil, fmt.Errorf("a message can carry at most %d attachments", maxUploadsPerSend)
	}
	var resolved surface.Uploads
	for _, id := range ids {
		upload, err := d.uploads.Resolve(strings.TrimSpace(id))
		if err != nil {
			return nil, fmt.Errorf("attachment %q is not stored on this host; upload it again", id)
		}
		resolved = append(resolved, upload)
	}
	return resolved, nil
}
