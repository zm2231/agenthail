package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
)

// ledgerProjectRoute lets a caller name a ledger by the id the snapshot lists
// its project under: the server turns that id into the project's ledger key
// before the route reads projectKey. A ledger key passes through unchanged,
// and so does every paired host's request, which already speaks ledger keys.
func (d *Daemon) ledgerProjectRoute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hostID, isHost := requestingHost(r.Context()); isHost && hostID != "" {
			next(w, r)
			return
		}
		if r.Method == http.MethodGet {
			query := r.URL.Query()
			key, err := d.ledgerKeyNamed(query.Get("projectKey"))
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, "projects_unavailable", err.Error())
				return
			}
			if key != query.Get("projectKey") {
				query.Set("projectKey", key)
				r.URL.RawQuery = query.Encode()
			}
			next(w, r)
			return
		}
		if r.Method != http.MethodPost {
			next(w, r)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, apiRequestBodyLimit))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		var fields map[string]json.RawMessage
		var named string
		if json.Unmarshal(body, &fields) == nil && json.Unmarshal(fields["projectKey"], &named) == nil && named != "" {
			key, err := d.ledgerKeyNamed(named)
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, "projects_unavailable", err.Error())
				return
			}
			if key != named {
				fields["projectKey"], _ = json.Marshal(key)
				body, _ = json.Marshal(fields)
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next(w, r)
	}
}

func (d *Daemon) ledgerKeyNamed(name string) (string, error) {
	if name == "" || strings.HasPrefix(name, registry.LedgerProjectKey("")) {
		return name, nil
	}
	keys, err := d.projectLedgerKeys()
	if err != nil {
		return "", err
	}
	key, named, err := keys.of(name)
	if err != nil || !named || key == "" {
		return name, err
	}
	return key, nil
}
