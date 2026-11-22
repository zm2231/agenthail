package daemon

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/zm2231/agenthail/internal/daemonapi"
)

func (d *Daemon) apiLedgerWords(w http.ResponseWriter, r *http.Request, key string, query url.Values) {
	ref := query.Get("id")
	if ref == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "view=words needs the id of the captured words.")
		return
	}
	around := 0
	if raw := query.Get("context"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > daemonapi.WordsMaxAround {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("--context takes 0 to %d turns", daemonapi.WordsMaxAround))
			return
		}
		around = n
	}
	words, err := ReadWords(d.Registry, d.ledgerStore(), key, ref, around)
	var elsewhere WordsElsewhere
	var missing WordsNotFound
	switch {
	case errors.As(err, &elsewhere):
		writeAPIError(w, http.StatusConflict, "unavailable", err.Error())
	case errors.As(err, &missing):
		writeAPIError(w, http.StatusNotFound, "not_found", err.Error())
	case err != nil:
		writeAPIError(w, http.StatusInternalServerError, "words_failed", err.Error())
	default:
		writeLedgerRead(w, r, words)
	}
}
