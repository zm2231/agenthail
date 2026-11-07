package daemon

import (
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
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
	d.ledgerOnce.Do(func() {
		d.ledger = ledger.NewCachedStore(d.Registry.WriteDB(), d.Registry.ReadDB())
		d.ledgerHub = newLedgerChangeHub(ledgerChangeWait, d.ledgerVersion, d.log.Printf)
		d.ledger.OnChange(d.ledgerHub.changed)
		d.ledger.OnRecord(d.wakeLedgerOwner)
	})
	return d.ledger
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

func (d *Daemon) knownLedgerSession(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	if sessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "sessionId is required: run this from inside a session, or pass --from.")
		return false
	}
	if ledgerProxiedBy(r) != "" {
		return true
	}
	if _, err := d.Registry.Session(sessionID); err != nil {
		writeAPIError(w, http.StatusNotFound, "session_not_found", "No session "+sessionID+" in this Agenthail.")
		return false
	}
	return true
}

func (d *Daemon) knownLedgerProject(w http.ResponseWriter, r *http.Request, key, sessionID string) bool {
	if key == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "projectKey is required.")
		return false
	}
	if ledgerProxiedBy(r) != "" {
		if owner, _, err := d.ledgerStore().Owner(key); err != nil || owner != d.hostIdentity().ID {
			writeAPIError(w, http.StatusConflict, "refused", "This host does not own the ledger for "+key+".")
			return false
		}
		return true
	}
	known, err := d.ledgerProjectKnown(key, sessionID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "projects_unavailable", err.Error())
		return false
	}
	if !known {
		writeAPIError(w, http.StatusNotFound, "project_not_found", "No project "+key+" in this Agenthail; see agenthail projects.")
	}
	return known
}

func (d *Daemon) ledgerProjectKnown(key, sessionID string) (bool, error) {
	projects, err := d.Registry.ListProjects()
	if err != nil {
		return false, err
	}
	for _, p := range projects {
		if p.Key == key || p.Added && registry.LedgerProjectKey(p.ID) == key {
			return true, nil
		}
	}
	if session, err := d.Registry.Session(sessionID); err == nil {
		targets, _, err := d.ledgerTargets(d.ledgerStore(), *session)
		if err != nil {
			return false, err
		}
		for _, target := range targets {
			if target.key == key {
				return true, nil
			}
		}
	}
	return d.Registry.LocalCatalogProjectKey(key)
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
	if !decodeLedgerWrite(w, r, &req) || !d.knownLedgerSession(w, r, req.SessionID) || !d.knownLedgerProject(w, r, req.ProjectKey, req.SessionID) {
		return
	}
	goal, created, err := d.ledgerStore().EnsureGoal(req.ProjectKey, req.Name, req.SessionID)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, ledgerGoalResponse{Goal: goal, Created: created})
}

func (d *Daemon) apiLedgerWorkHandler(w http.ResponseWriter, r *http.Request) {
	var req ledger.WorkRequest
	if !decodeLedgerWrite(w, r, &req) || !d.knownLedgerSession(w, r, req.SessionID) || !d.knownLedgerProject(w, r, req.ProjectKey, req.SessionID) {
		return
	}
	result, err := d.ledgerStore().Work(req, time.Now())
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, result)
}

func (d *Daemon) apiLedgerImportHandler(w http.ResponseWriter, r *http.Request) {
	var req ledger.WorkImportRequest
	if !decodeLedgerWrite(w, r, &req) || !d.knownLedgerSession(w, r, req.SessionID) || !d.knownLedgerProject(w, r, req.ProjectKey, req.SessionID) {
		return
	}
	result, err := d.ledgerStore().ImportWork(req, time.Now())
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, result)
}

type ledgerAttachBody struct {
	ledger.AttachRequest
	Fragments []ledger.AttachRequest `json:"fragments,omitempty"`
}

func (d *Daemon) apiLedgerAttachHandler(w http.ResponseWriter, r *http.Request) {
	var body ledgerAttachBody
	if !decodeLedgerWrite(w, r, &body) || !d.knownLedgerSession(w, r, body.SessionID) || !d.knownLedgerProject(w, r, body.ProjectKey, body.SessionID) {
		return
	}
	req := body.AttachRequest
	if len(body.Fragments) > 0 {
		if !reflect.DeepEqual(req, ledger.AttachRequest{ProjectKey: req.ProjectKey, SessionID: req.SessionID}) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "Give the fragments, or one attach, not both.")
			return
		}
		result, err := d.ledgerStore().AttachEach(req.ProjectKey, req.SessionID, body.Fragments)
		if err != nil {
			writeLedgerError(w, err)
			return
		}
		writeDashboardJSON(w, http.StatusOK, result)
		return
	}
	result, err := d.ledgerStore().Attach(req)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, result)
}
