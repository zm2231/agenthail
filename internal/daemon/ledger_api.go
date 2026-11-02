package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type ledgerCaptureRequest struct {
	SessionID     string `json:"sessionId"`
	CaptureRef    string `json:"captureRef"`
	Prompt        string `json:"prompt"`
	SubmittedAtMs int64  `json:"submittedAtMs"`
}

type ledgerCaptureResponse struct {
	Captured   bool   `json:"captured"`
	ProjectKey string `json:"projectKey,omitempty"`
}

func (d *Daemon) apiLedgerCaptureHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ledgerCaptureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.SessionID == "" || req.Prompt == "" || req.CaptureRef == "" || req.SubmittedAtMs <= 0 {
		http.Error(w, "sessionId, captureRef, prompt and submittedAtMs are required", http.StatusBadRequest)
		return
	}
	if !surfaces.IsTypedHumanPrompt(req.Prompt) {
		writeJSON(w, ledgerCaptureResponse{Captured: false})
		return
	}

	session, err := d.Registry.Session(req.SessionID)
	if err != nil || session.Cwd == "" {
		writeJSON(w, ledgerCaptureResponse{Captured: false})
		return
	}

	project, err := ResolvePathProject(context.Background(), d.Registry, session.Cwd)
	if err != nil || project == nil || project.Key == "" {
		writeJSON(w, ledgerCaptureResponse{Captured: false})
		return
	}

	store := ledger.NewStore(d.Registry.WriteDB(), d.Registry.ReadDB())
	if !d.sessionCapturesForProject(store, project, session) {
		writeJSON(w, ledgerCaptureResponse{Captured: false})
		return
	}

	_, captureErr := store.RecordUtterance(ledger.Utterance{
		ProjectKey: project.Key,
		CaptureRef: req.CaptureRef,
		SpeakerID:  surface.PersonSessionID,
		TargetID:   req.SessionID,
		Surface:    "terminal",
		Text:       req.Prompt,
		At:         time.UnixMilli(req.SubmittedAtMs),
	})
	if captureErr != nil {
		d.log.Printf("ledger capture: record terminal utterance for project %s: %s", project.Key, captureErr)
		http.Error(w, "capture failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, ledgerCaptureResponse{Captured: true, ProjectKey: project.Key})
}

func (d *Daemon) sessionCapturesForProject(store *ledger.Store, project *CatalogProject, session *surface.Session) bool {
	enabled, err := store.Enabled(project.Key)
	if err != nil {
		return false
	}
	if enabled {
		return true
	}
	captures, err := store.CapturesForCoordinator(project.Key)
	if err != nil || !captures {
		return false
	}
	spec, err := d.Registry.Coordinator(project.LocalID)
	if err != nil {
		return false
	}
	return spec.SessionID == session.ID
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (d *Daemon) registerLedgerRoutes(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/ledger/capture", d.apiV1Guard(dashboard, "control", d.apiLedgerCaptureHandler))
}
