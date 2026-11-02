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
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	var req ledgerCaptureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.SessionID == "" || req.Prompt == "" || req.PromptID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "sessionId, promptId and prompt are required.")
		return
	}
	if !surfaces.IsTypedHumanPrompt(req.Prompt) {
		writeDashboardJSON(w, http.StatusOK, ledgerCaptureResponse{})
		return
	}
	session, err := d.Registry.Session(req.SessionID)
	if err != nil {
		writeDashboardJSON(w, http.StatusOK, ledgerCaptureResponse{})
		return
	}
	ref := ledger.PromptRef(req.PromptID)
	key, captured, err := d.recordPersonWords(*session, ledger.Utterance{Ref: ref, Surface: "terminal", Text: req.Prompt, At: time.Now()})
	if err != nil {
		d.log.Printf("ledger capture: record prompt %s for %s in project %s: %s", ref, req.SessionID, key, err)
		writeAPIError(w, http.StatusInternalServerError, "capture_failed", "The prompt was not recorded in the ledger.")
		return
	}
	if !captured {
		writeDashboardJSON(w, http.StatusOK, ledgerCaptureResponse{})
		return
	}
	writeDashboardJSON(w, http.StatusOK, ledgerCaptureResponse{Captured: true, ProjectKey: key, Ref: ref})
}

func (d *Daemon) registerLedgerRoutes(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/ledger/capture", d.apiV1Guard(dashboard, "control", d.apiLedgerCaptureHandler))
}
