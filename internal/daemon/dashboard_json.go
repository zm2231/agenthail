package daemon

import (
	"net/http"
)

func writeDashboardJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	encoded, err := marshalAPIJSON(value)
	if err == nil {
		_, _ = w.Write(append(encoded, '\n'))
	}
}
