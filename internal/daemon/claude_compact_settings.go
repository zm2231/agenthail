package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) claudeCompactSettingsHandler(w http.ResponseWriter, r *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodGet:
		roots, err := surface.ClaudeConfigRoots(home)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		results := make([]surface.ClaudeCompactSettings, 0, len(roots))
		for _, root := range roots {
			settings, err := surface.ReadClaudeCompactSettings(root)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			results = append(results, settings)
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"roots": results})
	case http.MethodPost:
		var request struct {
			Root    string `json:"root"`
			Model   string `json:"model"`
			Window  string `json:"window"`
			Enabled *bool  `json:"enabled"`
			Idle    *bool  `json:"idleCompaction"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid compaction settings request", http.StatusBadRequest)
			return
		}
		root, err := surface.ResolveClaudeConfigRoot(home, request.Root)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		settings, err := surface.WriteClaudeCompactSettings(root, surface.ClaudeCompactChange{Model: request.Model, Window: request.Window, Enabled: request.Enabled, Idle: request.Idle})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		d.publishEvent("settings.updated", "", map[string]string{"source": "claude-compact-settings"})
		writeDashboardJSON(w, http.StatusOK, settings)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
