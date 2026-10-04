package daemon

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	providers "github.com/zm2231/agenthail/internal/surface/surfaces"
)

func (d *Daemon) apiSessionAttachmentHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	sessionID, id := strings.TrimSpace(r.URL.Query().Get("sessionId")), strings.TrimSpace(r.URL.Query().Get("id"))
	if sessionID == "" || id == "" {
		writeAPIError(w, http.StatusBadRequest, "attachment_query_invalid", "sessionId and id are required.")
		return
	}
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "session_not_found", "The requested session was not found.")
		return
	}
	adapter := d.surfaceForKind(session.Surface)
	reader, ok := adapter.(surface.AttachmentReader)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "attachment_not_found", "The referenced attachment was not found.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	attachment, data, err := reader.ReadAttachment(ctx, session, id)
	if err != nil {
		status, code, message := providers.AttachmentHTTPStatus(err)
		writeAPIError(w, status, code, message)
		return
	}
	w.Header().Set("Content-Type", attachment.MediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, immutable, max-age=31536000")
	w.Header().Set("ETag", `"`+attachment.ID+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
