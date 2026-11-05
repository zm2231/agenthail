package daemon

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type ledgerCaptureRequest struct {
	SessionID   string `json:"sessionId"`
	PromptID    string `json:"promptId"`
	Prompt      string `json:"prompt"`
	MessageUUID string `json:"messageUuid,omitempty"`
}

type ledgerCaptureResponse struct {
	Captured   bool     `json:"captured"`
	Attach     bool     `json:"attach,omitempty"`
	ProjectKey string   `json:"projectKey,omitempty"`
	Ref        string   `json:"ref,omitempty"`
	Unattached []string `json:"unattached,omitempty"`
}

const ledgerUnattachedShown = 5

func (d *Daemon) unattachedBefore(key, sessionID, ref string) []string {
	pending, err := d.ledgerStore().UnattachedFor(key, sessionID)
	if err != nil {
		d.log.Printf("ledger capture: list unattached words for %s in %s: %s", sessionID, key, err)
		return nil
	}
	var refs []string
	for _, u := range pending {
		if u.ID != ref && len(refs) < ledgerUnattachedShown {
			refs = append(refs, u.ID)
		}
	}
	return refs
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
	if _, peer := surface.ParsePeerEnvelope(req.Prompt); peer || !surfaces.IsTypedHumanPrompt(req.Prompt) || d.notPerson.has(req.PromptID, time.Now()) {
		writeDashboardJSON(w, http.StatusOK, ledgerCaptureResponse{})
		return
	}
	session, err := d.Registry.Session(req.SessionID)
	if err != nil {
		writeDashboardJSON(w, http.StatusOK, ledgerCaptureResponse{})
		return
	}
	now := time.Now()
	ref, surfaceName := ledger.PromptRef(req.PromptID), "terminal"
	if uuid, delivered := d.delivered.claim(session.ID, req.MessageUUID, req.Prompt, now); delivered {
		ref, surfaceName = ledger.DeliveryRef(uuid), string(session.Surface)
	}
	record, err := d.recordPersonWords(*session, ledger.Utterance{Ref: ref, Surface: surfaceName, Text: req.Prompt, At: now, CapturedBy: ledger.CapturedByHook})
	if err != nil {
		d.log.Printf("ledger capture: record prompt %s for %s in project %s: %s", ref, req.SessionID, record.key, err)
		writeAPIError(w, http.StatusInternalServerError, "capture_failed", "The prompt was not recorded in the ledger.")
		return
	}
	switch {
	case !record.captured:
		writeDashboardJSON(w, http.StatusOK, ledgerCaptureResponse{})
	case !record.attach:
		writeDashboardJSON(w, http.StatusOK, ledgerCaptureResponse{Captured: true, ProjectKey: record.key, Ref: ref})
	default:
		writeDashboardJSON(w, http.StatusOK, ledgerCaptureResponse{Captured: true, Attach: true, ProjectKey: record.key, Ref: ref, Unattached: d.unattachedBefore(record.key, session.ID, ref)})
	}
}

func (d *Daemon) registerLedgerRoutes(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/ledger/capture", d.apiV1Guard(dashboard, "control", d.apiLedgerCaptureHandler))
	mux.HandleFunc("/api/v1/ledger/scope", d.apiV1Guard(dashboard, "control", d.apiLedgerScopeHandler))
	mux.HandleFunc("/api/v1/ledger/checkpoint", d.apiV1Guard(dashboard, "control", d.apiLedgerCheckpointHandler))
	mux.HandleFunc("/api/v1/ledger/goal", d.apiV1Guard(dashboard, "control", d.ledgerWriteRoute(d.apiLedgerGoalHandler)))
	mux.HandleFunc("/api/v1/ledger/attach", d.apiV1Guard(dashboard, "control", d.ledgerWriteRoute(d.apiLedgerAttachHandler)))
	mux.HandleFunc("/api/v1/ledger/question", d.apiV1Guard(dashboard, "control", d.ledgerWriteRoute(d.apiLedgerQuestionHandler)))
	mux.HandleFunc("/api/v1/ledger/work", d.apiV1Guard(dashboard, "control", d.ledgerWriteRoute(d.apiLedgerWorkHandler)))
	mux.HandleFunc("/api/v1/ledger/import", d.apiV1Guard(dashboard, "control", d.ledgerWriteRoute(d.apiLedgerImportHandler)))
	mux.HandleFunc("/api/v1/ledger/clause", d.apiV1Guard(dashboard, "control", d.ledgerWriteRoute(d.apiLedgerClauseHandler)))
	mux.HandleFunc("/api/v1/ledger/g2", d.apiV1Guard(dashboard, "read", d.apiLedgerG2Handler))
	mux.HandleFunc("/api/v1/ledger/check", d.apiV1Guard(dashboard, "control", d.ledgerWriteRoute(d.apiLedgerCheckHandler)))
	mux.HandleFunc("/api/v1/ledger/check-report", d.apiV1Guard(dashboard, "control", d.apiLedgerCheckReportHandler))
	d.registerLedgerSyncRoutes(mux, dashboard)
}
