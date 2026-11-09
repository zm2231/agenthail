package daemon

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

type ledgerCheckpointRequest struct {
	SessionID string `json:"sessionId"`
}

type ledgerCheckpointResponse struct {
	Instruction string `json:"instruction,omitempty"`
}

func (d *Daemon) ledgerCheckpoint(session surface.Session) (string, error) {
	store := d.ledgerStore()
	targets, access, err := d.ledgerTargets(store, session)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, target := range targets {
		scope, err := d.ledgerSince(store, target, session.ID, access)
		if err != nil {
			return "", err
		}
		if !scope.checkpoints {
			continue
		}
		words, err := store.ProvisionalFor(target.key, session.ID)
		if err != nil {
			return "", err
		}
		if text := ledger.ProvisionalNote(target.key, len(words)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func (d *Daemon) apiLedgerCheckpointHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	var req ledgerCheckpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "sessionId is required.")
		return
	}
	session, err := d.Registry.Session(req.SessionID)
	if err != nil {
		writeDashboardJSON(w, http.StatusOK, ledgerCheckpointResponse{})
		return
	}
	text, err := d.ledgerCheckpoint(*session)
	if err != nil {
		d.log.Printf("ledger checkpoint for %s: %s", req.SessionID, err)
		writeAPIError(w, http.StatusInternalServerError, "checkpoint_failed", "The provisional words could not be counted.")
		return
	}
	writeDashboardJSON(w, http.StatusOK, ledgerCheckpointResponse{Instruction: text})
}
