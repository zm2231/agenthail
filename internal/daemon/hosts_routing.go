package daemon

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
)

func (d *Daemon) hostRouted(param string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		var host *pairedHost
		explicitLocal := false
		if hostRef := strings.TrimSpace(query.Get("host")); hostRef != "" {
			query.Del("host")
			explicitLocal = d.isLocalHostRef(hostRef)
			if !explicitLocal {
				found, ok := d.pairedHost(hostRef)
				if !ok {
					writeAPIError(w, http.StatusNotFound, "host_not_found", fmt.Sprintf("No paired host is named %q.", hostRef))
					return
				}
				host = found
			}
			r.URL.RawQuery = query.Encode()
		}
		if value := query.Get(param); value != "" {
			routed, rawID, err := d.routeSession(value)
			if err != nil {
				writeRouteError(w, err)
				return
			}
			if routed != nil && (explicitLocal || host != nil && host != routed) {
				writeAPIError(w, http.StatusConflict, "host_mismatch", fmt.Sprintf("%s belongs to host %s, not the host this request names.", value, routed.record.Name))
				return
			}
			if routed != nil {
				host = routed
			}
			query.Set(param, rawID)
			r.URL.RawQuery = query.Encode()
		}
		if host == nil {
			next(w, r)
			return
		}
		if !writeIncompatibleHost(w, host) {
			return
		}
		if strings.HasSuffix(r.URL.Path, "-stream") {
			d.relayHostStream(w, r, host, query)
			return
		}
		var body []byte
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			read, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxForwardedBodyBytes))
			if err != nil {
				writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "The request body is too large to forward.")
				return
			}
			body = read
		}
		d.forwardHostRequest(w, r, host, r.Method, query, body)
	}
}

func writeIncompatibleHost(w http.ResponseWriter, host *pairedHost) bool {
	state, detail := host.status()
	if state != hostStateIncompatible {
		return true
	}
	writeAPIError(w, http.StatusConflict, "host_incompatible", fmt.Sprintf("Host %s needs an update: %s", host.record.Name, detail))
	return false
}

func writeRouteError(w http.ResponseWriter, err error) {
	var owned registry.SessionOwnedError
	if errors.As(err, &owned) {
		writeAPIError(w, http.StatusConflict, "session_owned", err.Error())
		return
	}
	writeAPIError(w, http.StatusNotFound, "host_not_found", err.Error())
}

func apiPath(path string) string {
	if strings.HasPrefix(path, "/api/v1/") {
		return path
	}
	return "/api/v1/" + strings.TrimPrefix(path, "/api/")
}
