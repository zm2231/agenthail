package daemon

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/claudesurface"
)

type ledgerCaptureRequest struct {
	SessionID   string `json:"sessionId"`
	PromptID    string `json:"promptId"`
	Prompt      string `json:"prompt"`
	MessageUUID string `json:"messageUuid,omitempty"`
}

type ledgerCaptureResponse struct {
	Captured    bool   `json:"captured"`
	Attach      bool   `json:"attach,omitempty"`
	ProjectKey  string `json:"projectKey,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Provisional int    `json:"provisional,omitempty"`
}

func (d *Daemon) provisionalCount(key, sessionID string) int {
	words, err := d.ledgerStore().ProvisionalFor(key, sessionID)
	if err != nil {
		d.log.Printf("ledger capture: count provisional words for %s in %s: %s", sessionID, key, err)
		return 0
	}
	return len(words)
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
	session, err := d.Registry.Session(req.SessionID)
	if err != nil {
		writeDashboardJSON(w, http.StatusOK, ledgerCaptureResponse{})
		return
	}
	now := time.Now()
	if _, peer := surface.ParsePeerEnvelope(req.Prompt); peer || !claudesurface.IsTypedHumanPrompt(req.Prompt) || d.notPerson.has(req.PromptID, now) {
		d.markTurnTyped(session.ID, false)
		writeDashboardJSON(w, http.StatusOK, ledgerCaptureResponse{})
		return
	}
	ref, surfaceName, words := ledger.PromptRef(req.PromptID), "terminal", req.Prompt
	uuid, delivered, claimed := d.delivered.claim(session.ID, req.PromptID, req.MessageUUID, req.Prompt, now)
	d.markTurnTyped(session.ID, !claimed)
	if claimed {
		ref, surfaceName, words = ledger.DeliveryRef(uuid), string(session.Surface), delivered
		// A copy of the hook that could not name the delivery recorded the
		// prompt under its own id first.
		d.reclassifyHookCapture(*session, ledger.PromptRef(req.PromptID), "delivered")
	}
	record, err := d.recordPersonWords(*session, ledger.Utterance{Ref: ref, Surface: surfaceName, Text: words, At: now, CapturedBy: ledger.CapturedByHook})
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
		writeDashboardJSON(w, http.StatusOK, ledgerCaptureResponse{Captured: true, Attach: true, ProjectKey: record.key, Ref: ref, Provisional: d.provisionalCount(record.key, session.ID)})
	}
}

// A typed turn is one the person is at, so the turn judge never pushes it to their devices.
func (d *Daemon) markTurnTyped(sessionID string, typed bool) {
	if err := d.Registry.MarkTurnTyped(sessionID, typed); err != nil {
		d.log.Printf("record who typed the prompt of %s: %s", d.resolveDisplay(sessionID), err)
	}
}

func (d *Daemon) registerLedgerRoutes(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/ledger", d.apiV1Guard(dashboard, "read", d.ledgerProjectRoute(d.apiLedgerReadHandler)))
	mux.HandleFunc("/api/v1/ledger/events", d.apiV1Guard(dashboard, "read", d.apiLedgerEventsHandler))
	mux.HandleFunc("/api/v1/ledger/search", d.apiV1Guard(dashboard, "read", d.apiLedgerSearchHandler))
	mux.HandleFunc("/api/v1/ledger/capture", d.apiV1Guard(dashboard, "control", d.apiLedgerCaptureHandler))
	mux.HandleFunc("/api/v1/ledger/scope", d.apiV1Guard(dashboard, "control", d.ledgerProjectRoute(d.apiLedgerScopeHandler)))
	mux.HandleFunc("/api/v1/ledger/checkpoint", d.apiV1Guard(dashboard, "control", d.apiLedgerCheckpointHandler))
	mux.HandleFunc("/api/v1/ledger/backlog-line", d.apiV1Guard(dashboard, "control", d.apiLedgerBacklogLineHandler))
	mux.HandleFunc("/api/v1/ledger/goal", d.apiV1Guard(dashboard, "control", d.ledgerProjectRoute(d.ledgerWriteRoute(d.apiLedgerGoalHandler))))
	mux.HandleFunc("/api/v1/ledger/attach", d.apiV1Guard(dashboard, "control", d.ledgerProjectRoute(d.ledgerWriteRoute(d.apiLedgerAttachHandler))))
	mux.HandleFunc("/api/v1/ledger/question", d.apiV1Guard(dashboard, "control", d.ledgerProjectRoute(d.ledgerWriteRoute(d.apiLedgerQuestionHandler))))
	mux.HandleFunc("/api/v1/ledger/work", d.apiV1Guard(dashboard, "control", d.ledgerProjectRoute(d.ledgerWriteRoute(d.apiLedgerWorkHandler))))
	mux.HandleFunc(itemQuestionPath, d.apiV1Guard(dashboard, "control", d.ledgerProjectRoute(d.apiLedgerItemQuestionHandler)))
	mux.HandleFunc("/api/v1/ledger/phases", d.apiV1Guard(dashboard, "control", d.ledgerProjectRoute(d.ledgerWriteRoute(d.apiLedgerPhasesHandler))))
	mux.HandleFunc("/api/v1/ledger/import", d.apiV1Guard(dashboard, "control", d.ledgerProjectRoute(d.ledgerWriteRoute(d.apiLedgerImportHandler))))
	mux.HandleFunc("/api/v1/ledger/clause", d.apiV1Guard(dashboard, "control", d.ledgerProjectRoute(d.ledgerWriteRoute(d.apiLedgerClauseHandler))))
	mux.HandleFunc("/api/v1/ledger/g2", d.apiV1Guard(dashboard, "read", d.ledgerProjectRoute(d.apiLedgerG2Handler)))
	mux.HandleFunc("/api/v1/ledger/check", d.apiV1Guard(dashboard, "control", d.ledgerProjectRoute(d.ledgerWriteRoute(d.apiLedgerCheckHandler))))
	mux.HandleFunc("/api/v1/ledger/check-report", d.apiV1Guard(dashboard, "control", d.apiLedgerCheckReportHandler))
	d.registerLedgerSyncRoutes(mux, dashboard)
}
