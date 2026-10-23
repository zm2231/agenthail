package daemon

import (
	"net/http"

	"github.com/zm2231/agenthail/internal/registry"
)

func (d *Daemon) registerWebPushAPI(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/browser/push", d.apiBrowserPushHandler(dashboard))
}

type browserPushStatus struct {
	PublicKey  string `json:"publicKey"`
	Subscribed bool   `json:"subscribed"`
	Withdraw   bool   `json:"withdraw"`
}

// apiBrowserPushHandler lets a signed-in browser read this host's push key
// and set or remove its own subscription. Only the browser's cookie names
// it: a subscription belongs to the browser that made it. Its notification
// worker reports with PATCH when closing a notification left it shown.
func (d *Daemon) apiBrowserPushHandler(dashboard *dashboardServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := dashboard.browserDevice(r)
		if !ok {
			writeAPIError(w, http.StatusUnauthorized, "browser_unauthorized", "Sign this browser in to the dashboard first.")
			return
		}
		if r.Method != http.MethodGet && !sameOrigin(r) {
			writeAPIError(w, http.StatusForbidden, "cross_origin", "Cross-origin dashboard requests are not allowed.")
			return
		}
		switch r.Method {
		case http.MethodGet:
			keys, err := webPushKey()
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, "push_key_unavailable", "This host could not read its push key.")
				return
			}
			subscribed, withdraw, err := d.Registry.BrowserPushSubscriptionState(device.ID)
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, "push_state_unavailable", err.Error())
				return
			}
			writeDashboardJSON(w, http.StatusOK, browserPushStatus{PublicKey: keys.PublicKey, Subscribed: subscribed, Withdraw: withdraw})
		case http.MethodPut:
			defer r.Body.Close()
			var request struct {
				Endpoint string `json:"endpoint"`
				Keys     struct {
					P256DH string `json:"p256dh"`
					Auth   string `json:"auth"`
				} `json:"keys"`
			}
			if err := decodeAPIV1JSON(w, r, &request, true); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "The push subscription is invalid.")
				return
			}
			if err := d.Registry.SaveBrowserPushSubscription(registry.BrowserPushSubscription{DeviceID: device.ID, Endpoint: request.Endpoint, P256DH: request.Keys.P256DH, Auth: request.Keys.Auth}); err != nil {
				writeAPIError(w, http.StatusBadRequest, "push_registration_failed", err.Error())
				return
			}
			d.publishEvent("device.push.updated", device.ID, map[string]any{"enabled": true})
			writeDashboardJSON(w, http.StatusOK, map[string]bool{"ok": true})
		case http.MethodPatch:
			defer r.Body.Close()
			var request struct {
				Endpoint string `json:"endpoint"`
				Withdraw *bool  `json:"withdraw"`
			}
			if err := decodeAPIV1JSON(w, r, &request, true); err != nil || request.Endpoint == "" || request.Withdraw == nil || *request.Withdraw {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "Report an endpoint whose notifications cannot be closed.")
				return
			}
			if err := d.Registry.StopBrowserPushWithdraws(device.ID, request.Endpoint); err != nil {
				writeAPIError(w, http.StatusInternalServerError, "push_state_failed", err.Error())
				return
			}
			writeDashboardJSON(w, http.StatusOK, map[string]bool{"ok": true})
		case http.MethodDelete:
			if err := d.Registry.RemoveBrowserPushSubscription(device.ID); err != nil {
				writeAPIError(w, http.StatusInternalServerError, "push_remove_failed", err.Error())
				return
			}
			d.publishEvent("device.push.updated", device.ID, map[string]any{"enabled": false})
			writeDashboardJSON(w, http.StatusOK, map[string]bool{"ok": true})
		default:
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET, PUT, PATCH or DELETE for this endpoint.")
		}
	}
}
