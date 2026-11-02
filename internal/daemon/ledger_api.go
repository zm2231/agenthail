package daemon

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type ledgerCaptureRequest struct {
	SessionID string `json:"sessionId"`
	PromptID  string `json:"promptId"`
	Prompt    string `json:"prompt"`
}

type ledgerCaptureResponse struct {
	Captured   bool   `json:"captured"`
	ProjectKey string `json:"projectKey,omitempty"`
	Ref        string `json:"ref,omitempty"`
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
	if req.SessionID == "" || req.Prompt == "" || req.PromptID == "" {
		http.Error(w, "sessionId, promptId and prompt are required", http.StatusBadRequest)
		return
	}
	if !surfaces.IsTypedHumanPrompt(req.Prompt) {
		writeJSON(w, ledgerCaptureResponse{})
		return
	}
	session, err := d.Registry.Session(req.SessionID)
	if err != nil {
		writeJSON(w, ledgerCaptureResponse{})
		return
	}
	ref := ledger.PromptRef(req.PromptID)
	key, captured, err := d.recordPersonWords(*session, ledger.Utterance{Ref: ref, Surface: "terminal", Text: req.Prompt, At: time.Now()})
	if err != nil {
		d.log.Printf("ledger capture: record prompt %s for %s in project %s: %s", ref, req.SessionID, key, err)
		http.Error(w, "capture failed", http.StatusInternalServerError)
		return
	}
	if !captured {
		writeJSON(w, ledgerCaptureResponse{})
		return
	}
	writeJSON(w, ledgerCaptureResponse{Captured: true, ProjectKey: key, Ref: ref})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (d *Daemon) registerLedgerRoutes(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/ledger/capture", d.apiV1Guard(dashboard, "control", d.apiLedgerCaptureHandler))
}
