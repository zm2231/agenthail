package daemon

import (
	"encoding/json"
	"net/http"
	"time"
)

const MCPClaimPath = "/api/v1/mcp/claim"

type mcpClaimRequest struct {
	Ref      string `json:"ref"`
	ThreadID string `json:"threadId"`
}

func (d *Daemon) apiMCPClaimHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	var req mcpClaimRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Ref == "" || req.ThreadID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "ref and threadId are required.")
		return
	}
	claimed, err := d.Registry.ClaimPendingCredential(req.Ref, req.ThreadID, time.Now())
	if err != nil {
		d.log.Printf("mcp: claim credential for %s: %s", req.ThreadID, err)
		writeAPIError(w, http.StatusInternalServerError, "claim_failed", "The credential was not claimed.")
		return
	}
	if !claimed {
		writeAPIError(w, http.StatusConflict, "credential_unclaimable", "The pending credential expired or was already claimed.")
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"claimed": true})
}
