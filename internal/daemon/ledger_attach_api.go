package daemon

import (
	"errors"
	"net/http"

	"github.com/zm2231/agenthail/internal/ledger"
)

type ledgerGoalRequest struct {
	ProjectKey string `json:"projectKey"`
	SessionID  string `json:"sessionId"`
	Name       string `json:"name"`
}

type ledgerGoalResponse struct {
	Goal    ledger.Goal `json:"goal"`
	Created bool        `json:"created"`
}

func (d *Daemon) ledgerStore() *ledger.Store {
	return ledger.NewStore(d.Registry.WriteDB(), d.Registry.ReadDB())
}

func decodeLedgerWrite(w http.ResponseWriter, r *http.Request, into any) bool {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return false
	}
	if err := decodeAPIV1JSON(w, r, into, false); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func (d *Daemon) knownLedgerSession(w http.ResponseWriter, sessionID string) bool {
	if sessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "sessionId is required: run this from inside a session, or pass --from.")
		return false
	}
	if _, err := d.Registry.Session(sessionID); err != nil {
		writeAPIError(w, http.StatusNotFound, "session_not_found", "No session "+sessionID+" in this Agenthail.")
		return false
	}
	return true
}

func (d *Daemon) knownLedgerProject(w http.ResponseWriter, key string) bool {
	if key == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "projectKey is required.")
		return false
	}
	projects, err := d.Registry.ListProjects()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "projects_unavailable", err.Error())
		return false
	}
	for _, p := range projects {
		if p.Key == key {
			return true
		}
	}
	writeAPIError(w, http.StatusNotFound, "project_not_found", "No project "+key+" in this Agenthail; see agenthail projects.")
	return false
}

func writeLedgerError(w http.ResponseWriter, err error) {
	var refusal *ledger.Refusal
	if errors.As(err, &refusal) {
		writeAPIError(w, http.StatusConflict, "refused", refusal.Reason)
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "ledger_write_failed", err.Error())
}

func (d *Daemon) apiLedgerGoalHandler(w http.ResponseWriter, r *http.Request) {
	var req ledgerGoalRequest
	if !decodeLedgerWrite(w, r, &req) || !d.knownLedgerSession(w, req.SessionID) || !d.knownLedgerProject(w, req.ProjectKey) {
		return
	}
	goal, created, err := d.ledgerStore().EnsureGoal(req.ProjectKey, req.Name, req.SessionID)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, ledgerGoalResponse{Goal: goal, Created: created})
}

func (d *Daemon) apiLedgerAttachHandler(w http.ResponseWriter, r *http.Request) {
	var req ledger.AttachRequest
	if !decodeLedgerWrite(w, r, &req) || !d.knownLedgerSession(w, req.SessionID) || !d.knownLedgerProject(w, req.ProjectKey) {
		return
	}
	result, err := d.ledgerStore().Attach(req)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, result)
}
