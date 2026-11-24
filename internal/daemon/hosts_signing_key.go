package daemon

import (
	"net/http"
	"time"
)

func (d *Daemon) apiHostSigningKeyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	if !d.requestHasScope(r, "settings") {
		writeAPIError(w, http.StatusForbidden, "forbidden", "Rotating this host's signing key needs the settings scope.")
		return
	}
	var request struct {
		Revoke bool `json:"revoke"`
	}
	if err := decodeAPIV1JSON(w, r, &request, true); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "The body is {\"revoke\": true|false}.")
		return
	}
	key, _, err := d.Registry.RotateHostKey(request.Revoke, time.Now())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "rotate_failed", err.Error())
		return
	}
	if err := d.catalog.flushCommitted(); err != nil {
		d.log.Printf("publish rotated host key: %s", err)
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"key": key, "revoked": request.Revoke})
}
