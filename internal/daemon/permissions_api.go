package daemon

import (
	"net/http"
	"os"

	"github.com/zm2231/agenthail/internal/permissions"
)

// permissionsDaemon is this daemon as macOS knows it for its grants.
var permissionsDaemon = func() (permissions.Daemon, error) {
	exe, err := os.Executable()
	return permissions.Daemon{Executable: exe, Service: os.Getenv("XPC_SERVICE_NAME")}, err
}

func (d *Daemon) registerPermissionsAPI(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/permissions", d.apiV1Guard(dashboard, "read", d.apiPermissionsHandler))
	mux.HandleFunc("/api/v1/permissions/request", d.apiV1Guard(dashboard, "settings", d.apiPermissionsRequestHandler))
}

// apiPermissionsHandler reads the daemon's privacy grants. Only the daemon
// can: macOS answers for the process responsible for the one asking.
func (d *Daemon) apiPermissionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	daemon, err := permissionsDaemon()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "executable_unknown", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, permissions.Check(r.Context(), daemon))
}

// apiPermissionsRequestHandler has macOS ask the person on the Mac for each
// grant the daemon does not hold yet.
func (d *Daemon) apiPermissionsRequestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	daemon, err := permissionsDaemon()
	if err == nil {
		err = permissions.Request(daemon)
	}
	if err != nil {
		writeAPIError(w, http.StatusConflict, "permissions_unavailable", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusAccepted, map[string]bool{"asked": true})
}
