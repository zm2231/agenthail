package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

func (d *Daemon) portHandler(served *servedMount) http.Handler {
	if served.name == "" {
		return d.pageHandler(served)
	}
	return d.mountHandler(served)
}

func (d *Daemon) pageHandler(served *servedMount) http.Handler {
	id := served.id
	mux := http.NewServeMux()
	route := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", id)
			handler(w, r)
		})
	}
	route("GET /_ah.js", d.extensionSDKHandler)
	route("POST /a/{action}", d.extensionActionHandler)
	route("GET /s", d.extensionSourcesHandler)
	route("GET /events", d.extensionEventsHandler)
	route("GET /{path...}", d.extensionPageHandler)
	return d.extensionPortHandler(func(w http.ResponseWriter, r *http.Request, dashboard *dashboardServer) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", extensionPageCSP+" "+strings.Join(d.shellOrigins(r, dashboard), " "))
		mux.ServeHTTP(w, r)
	})
}

// A page on another site that rebinds its hostname to 127.0.0.1 is refused
// before any credential is checked.
func (d *Daemon) extensionPortHandler(next func(http.ResponseWriter, *http.Request, *dashboardServer)) http.Handler {
	return d.portFrontDoor(func(host string) bool { return d.extensions.mounts.allowedHost(host) }, next)
}

func (d *Daemon) portFrontDoor(allowedHost func(string) bool, next func(http.ResponseWriter, *http.Request, *dashboardServer)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedHost(r.Host) {
			writeAPIError(w, http.StatusMisdirectedRequest, "misdirected", "This port answers only for this machine's own names.")
			return
		}
		dashboard := d.dashboard.Load()
		if dashboard == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "The dashboard is off, so this port cannot check who is asking.")
			return
		}
		scope := "control"
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			scope = "read"
		}
		d.apiV1Guard(dashboard, scope, func(w http.ResponseWriter, r *http.Request) {
			next(w, r, dashboard)
		})(w, r.WithContext(context.WithValue(r.Context(), extensionPortContextKey{}, true)))
	})
}

func (d *Daemon) extensionSDKHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := json.Marshal(r.PathValue("id"))
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, "const changesAfter = %d;\nconst extensionId = %s;\n", d.events.cursor(), id)
	_, _ = w.Write(extensionSDK)
}

func (d *Daemon) extensionEventsHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d.streamEvents(w, r, func(event apiEvent) bool {
		if event.Type != "extension.changed" && event.Type != "extension.source" {
			return false
		}
		var data struct {
			Extension  string   `json:"extension"`
			Extensions []string `json:"extensions"`
		}
		_ = json.Unmarshal(event.Data, &data)
		return data.Extension == id || slices.Contains(data.Extensions, id)
	})
}
