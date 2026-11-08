package daemon

import (
	"encoding/json"
	"net/http"

	"github.com/zm2231/agenthail/internal/apitime"
)

func writeVerbatimJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if encoded, err := json.Marshal(value); err == nil {
		_, _ = w.Write(append(encoded, '\n'))
	}
}

func writeDashboardJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	encoded, err := apitime.Marshal(value)
	if err == nil {
		_, _ = w.Write(append(encoded, '\n'))
	}
}
