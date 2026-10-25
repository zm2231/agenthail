package daemon

import (
	"net/http"

	"github.com/zm2231/agenthail/internal/registry"
)

type hostView struct {
	registry.PairedHost
	Detail       string `json:"detail,omitempty"`
	Notice       string `json:"notice,omitempty"`
	SessionCount int    `json:"sessionCount"`
}

func (d *Daemon) registerHostAPI(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/host", d.apiV1Guard(dashboard, "read", d.apiHostSelfHandler))
	mux.HandleFunc("/api/v1/hosts", d.apiV1Guard(dashboard, "read", d.apiHostsHandler))
	mux.HandleFunc("/api/v1/resolve", d.apiV1Guard(dashboard, "read", d.apiResolveHandler))
}

func (d *Daemon) apiHostSelfHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeDashboardJSON(w, http.StatusOK, map[string]any{"host": d.localHost()})
	case http.MethodPut:
		if !d.requestHasScope(r, "settings") {
			writeAPIError(w, http.StatusForbidden, "forbidden", "Renaming this host needs the settings scope.")
			return
		}
		var request struct {
			Name string `json:"name"`
		}
		if err := decodeAPIV1JSON(w, r, &request, false); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "A host name is required.")
			return
		}
		identity, err := d.Registry.SetHostName(request.Name)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_host_name", err.Error())
			return
		}
		d.setHostIdentity(identity)
		writeDashboardJSON(w, http.StatusOK, map[string]any{"host": d.localHost()})
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or PUT for this endpoint.")
	}
}

func (d *Daemon) requestHasScope(r *http.Request, scope string) bool {
	token := bearerToken(r)
	if token == "" || d.dashboard == nil || constantTokenEqual(token, d.dashboard.token) {
		return true
	}
	_, err := d.Registry.AuthenticateDevice(token, scope)
	return err == nil
}

func (d *Daemon) hostViews() ([]hostView, error) {
	records, err := d.Registry.ListHosts()
	if err != nil {
		return nil, err
	}
	snapshot, err := d.Registry.CatalogSnapshot()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, state := range snapshot.Sessions {
		if state.HostID != "" {
			counts[state.HostID]++
		}
	}
	views := make([]hostView, 0, len(records))
	for _, record := range records {
		view := hostView{PairedHost: record, SessionCount: counts[record.ID]}
		if host, found := d.pairedHost(record.ID); found {
			view.State, view.Detail = host.status()
			view.Notice = host.notice()
		}
		views = append(views, view)
	}
	return views, nil
}

func (d *Daemon) apiHostsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		views, err := d.hostViews()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "host_list_failed", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"host": d.localHost(), "hosts": views})
	case http.MethodPost:
		if !d.requestHasScope(r, "settings") {
			writeAPIError(w, http.StatusForbidden, "forbidden", "Pairing a host needs the settings scope.")
			return
		}
		d.addHostHandler(w, r)
	case http.MethodDelete:
		if !d.requestHasScope(r, "settings") {
			writeAPIError(w, http.StatusForbidden, "forbidden", "Removing a host needs the settings scope.")
			return
		}
		d.removeHostHandler(w, r)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET, POST or DELETE for this endpoint.")
	}
}
