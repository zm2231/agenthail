package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/zm2231/agenthail/internal/hosts"
)

type browserResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (b *browserResponse) Header() http.Header    { return b.header }
func (b *browserResponse) WriteHeader(status int) { b.status = status }
func (b *browserResponse) Write(data []byte) (int, error) {
	if b.status == 0 {
		b.status = 200
	}
	return b.body.Write(data)
}

func (d *Daemon) dashboardBrowserSession(w http.ResponseWriter, r *http.Request) {
	captured := &browserResponse{header: http.Header{}}
	routed := r.Clone(r.Context())
	d.hostRouted("id", d.dashboardSessionHandler)(captured, routed)
	if captured.status != http.StatusOK {
		for key, values := range captured.header {
			w.Header()[key] = values
		}
		w.WriteHeader(captured.status)
		_, _ = w.Write(captured.body.Bytes())
		return
	}
	var payload map[string]json.RawMessage
	var historical dashboardSession
	if json.Unmarshal(captured.body.Bytes(), &payload) != nil || json.Unmarshal(payload["session"], &historical) != nil {
		writeAPIError(w, 502, "invalid_session", "Invalid session response")
		return
	}
	host, _, err := d.routeSession(historical.ID)
	if err != nil {
		writeRouteError(w, err)
		return
	}
	_ = json.Unmarshal(payload["capabilities"], &historical.Capabilities)
	_ = json.Unmarshal(payload["readOnly"], &historical.ReadOnly)
	_ = json.Unmarshal(payload["readOnlyReason"], &historical.ReadOnlyReason)
	_ = json.Unmarshal(payload["alias"], &historical.Alias)
	_ = json.Unmarshal(payload["limitResume"], &historical.LimitResume)
	local := d.localHost()
	historical.Host = &local
	if host != nil {
		ref := host.ref()
		historical.Host = &ref
		historical.ID = hosts.Qualify(host.record.ID, historical.ID)
	}
	presentation, err := d.readSessionView(r.Context(), sessionViewRequest{Scope: "all", IDs: []string{historical.ID}, Detail: &historical})
	if err != nil {
		writeAPIError(w, 500, "internal_error", err.Error())
		return
	}
	payload["session"], _ = json.Marshal(presentation.Sessions[0])
	payload["presentationPosition"], _ = json.Marshal(presentation.PresentationPosition)
	w.Header().Set("Cache-Control", "no-store")
	writeDashboardJSON(w, http.StatusOK, payload)
}
