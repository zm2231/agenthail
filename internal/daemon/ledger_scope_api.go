package daemon

import (
	"encoding/json"
	"net/http"

	"github.com/zm2231/agenthail/internal/ledger"
)

type ledgerScopeRequest struct {
	ProjectKey string            `json:"projectKey"`
	Enabled    bool              `json:"enabled"`
	Attach     ledger.AttachMode `json:"attach,omitempty"`
	SessionID  string            `json:"sessionId,omitempty"`
}

type ledgerScopeResponse struct {
	Attach     ledger.AttachMode `json:"attach,omitempty"`
	Enabled    *bool             `json:"enabled,omitempty"`
	Granted    *bool             `json:"granted,omitempty"`
	ProjectKey string            `json:"projectKey"`
	SessionID  string            `json:"sessionId,omitempty"`
}

func (d *Daemon) apiLedgerScopeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	var req ledgerScopeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if !d.knownLedgerProject(w, r, req.ProjectKey, "") {
		return
	}
	store := d.ledgerStore()
	if req.SessionID != "" {
		if req.Attach != "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "attach sets when the whole project attaches; a session you add follows it.")
			return
		}
		var err error
		if req.Enabled {
			if !d.knownLedgerSession(w, r, req.SessionID) {
				return
			}
			err = store.GrantSession(req.ProjectKey, req.SessionID)
		} else {
			err = store.RevokeSession(req.ProjectKey, req.SessionID)
		}
		if err != nil {
			writeLedgerError(w, err)
			return
		}
		writeDashboardJSON(w, http.StatusOK, ledgerScopeResponse{ProjectKey: req.ProjectKey, SessionID: req.SessionID, Granted: &req.Enabled})
		return
	}
	if !req.Enabled {
		if req.Attach != "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "attach applies only when enabling the ledger.")
			return
		}
		if err := store.Disable(req.ProjectKey); err != nil {
			writeLedgerError(w, err)
			return
		}
		writeDashboardJSON(w, http.StatusOK, ledgerScopeResponse{ProjectKey: req.ProjectKey, Enabled: &req.Enabled})
		return
	}
	mode, err := ledger.ParseAttachMode(string(req.Attach))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := store.Enable(req.ProjectKey, mode); err != nil {
		writeLedgerError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, ledgerScopeResponse{ProjectKey: req.ProjectKey, Enabled: &req.Enabled, Attach: mode})
}
