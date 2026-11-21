package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/surface/claudesurface"
)

// claudePromptHolder is the Claude adapter's side of the PermissionRequest
// hook: it holds a terminal session's prompt until a person answers it
// through Agenthail or the session settles it.
type claudePromptHolder interface {
	HoldPrompt(ctx context.Context, prompt claudesurface.ClaudePermissionPrompt) (*claudesurface.ClaudePromptDecision, error)
	OnPromptChange(changed func(claudesurface.ClaudePromptChange))
}

func (d *Daemon) claudePrompts() claudePromptHolder {
	for _, adapter := range d.Surfaces {
		if holder, ok := adapter.(claudePromptHolder); ok {
			return holder
		}
	}
	return nil
}

// claudePermissionRequestHandler holds the request open until the prompt is
// answered or settled. Only this machine's CLI holds the dashboard token it
// requires, so no paired device or browser can post a prompt.
func (d *Daemon) claudePermissionRequestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	if !dashboardBearer(r.Context()) {
		writeAPIError(w, http.StatusForbidden, "local_only", "Only Agenthail's own hook on this machine can post a prompt.")
		return
	}
	holder := d.claudePrompts()
	if holder == nil {
		writeAPIError(w, http.StatusNotFound, "not_held", claudesurface.ErrPromptNotHeld.Error())
		return
	}
	var request daemonapi.ClaudePermissionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, apiRequestBodyLimit)).Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "The prompt is not valid JSON.")
		return
	}
	request.Prompt.PID = request.PID
	if request.AskedAtMS > 0 {
		request.Prompt.AskedAt = time.UnixMilli(request.AskedAtMS)
	}
	decision, err := holder.HoldPrompt(r.Context(), request.Prompt)
	switch {
	case errors.Is(err, claudesurface.ErrPromptNotHeld):
		writeAPIError(w, http.StatusConflict, "not_held", err.Error())
		return
	case r.Context().Err() != nil:
		// The daemon is stopping or the hook left. A handler that returns
		// without writing still sends 200, which the hook would take as
		// settled; dropping the connection tells it to post again.
		panic(http.ErrAbortHandler)
	case err != nil:
		writeAPIError(w, http.StatusBadRequest, "invalid_prompt", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, daemonapi.ClaudePermissionResponse{Decision: decision})
}

// claudePromptChanged records what the session waits on once a held prompt
// opens or closes, which announces the question with what it asks and
// withdraws the notifications of the wait it replaced.
func (d *Daemon) claudePromptChanged(change claudesurface.ClaudePromptChange) {
	d.background.start(func(lifetime context.Context) {
		session, err := d.Registry.Session(change.SessionID)
		if err != nil || session == nil {
			return
		}
		adapter, err := d.adapters().For(session)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(lifetime, surfaceOperationTimeout)
		defer cancel()
		if _, err := d.observeTurn(ctx, adapter, session); err != nil {
			d.log.Printf("observe %s after its prompt changed: %s", d.resolveDisplay(session.ID), err)
		}
	})
}
