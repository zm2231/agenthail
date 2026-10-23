package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/zm2231/agenthail/internal/surface"
)

type remoteControlSource interface{ RemoteControlAvailable() bool }

// claudeRemoteControlHandler reads and sets remoteControlAtStartup in each
// Claude config root. An install without Remote Control refuses to set it,
// since Agenthail could not use the attachment it starts.
func (d *Daemon) claudeRemoteControlHandler(w http.ResponseWriter, r *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	source, ok := d.adapters().ForSurface(surface.KindClaude).(remoteControlSource)
	available := ok && source.RemoteControlAvailable()
	switch r.Method {
	case http.MethodGet:
		roots, err := surface.ReadClaudeRemoteControl(home)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"available": available, "roots": roots})
	case http.MethodPost:
		var request struct {
			Root      string `json:"root"`
			AtStartup *bool  `json:"atStartup"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || request.AtStartup == nil {
			http.Error(w, "invalid Remote Control request: send root and atStartup", http.StatusBadRequest)
			return
		}
		if !available {
			http.Error(w, "this install has no Claude Remote Control", http.StatusConflict)
			return
		}
		root, err := surface.ResolveClaudeConfigRoot(home, request.Root)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := surface.WriteClaudeRemoteControl(root, *request.AtStartup); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		d.publishEvent("settings.updated", "", map[string]string{"source": "claude-remote-control"})
		roots, err := surface.ReadClaudeRemoteControl(home)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"available": available, "roots": roots})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
