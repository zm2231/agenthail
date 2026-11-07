package daemon

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/sse"
)

const ledgerHTTPPageBudget = 1 << 20

var ledgerWorkFilters = []string{"q", "state", "type", "priority", "label", "assignee", "clause", "found", "all", "sort"}

func (d *Daemon) ledgerRead(key string, req ledger.ReadRequest) (map[string]any, error) {
	if last, ok := d.ledgerSync.last(key); ok {
		req.Sync = &ledger.SyncHealth{OwnerHead: last.Head, Error: last.Error}
	}
	result, err := d.ledgerStore().Read(key, req)
	if err != nil {
		return nil, err
	}
	refusals, err := d.Registry.ProjectMoveRefusalsOf(key)
	if err != nil || len(refusals) == 0 {
		return result, err
	}
	result["notCarried"] = refusals
	return result, nil
}

// ledgerETag hashes the exact bytes served, so two different answers never share one.
func ledgerETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + base64.RawURLEncoding.EncodeToString(sum[:18]) + `"`
}

func (d *Daemon) ledgerVersion(key string) (string, error) {
	result, err := d.ledgerRead(key, ledger.ReadRequest{Budget: ledgerHTTPPageBudget})
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return ledgerETag(body), nil
}

func ledgerReadQuery(query url.Values) (ledger.ReadRequest, error) {
	req := ledger.ReadRequest{View: query.Get("view"), ID: query.Get("id"), Cursor: query.Get("cursor"), Budget: ledgerHTTPPageBudget}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return req, fmt.Errorf("limit must be a number")
		}
		req.Limit = limit
	}
	filtered := false
	for _, name := range ledgerWorkFilters {
		filtered = filtered || query.Has(name)
	}
	if !filtered {
		return req, nil
	}
	w := &ledger.WorkQuery{}
	req.Work = w
	for _, term := range query["q"] {
		w.Terms = append(w.Terms, strings.Fields(term)...)
	}
	for _, state := range query["state"] {
		w.States = append(w.States, ledger.WorkState(state))
	}
	for _, typ := range query["type"] {
		w.Types = append(w.Types, ledger.WorkType(typ))
	}
	w.Priorities, w.Labels = query["priority"], query["label"]
	w.Assignee, w.Clause, w.Sort = query.Get("assignee"), query.Get("clause"), query.Get("sort")
	for name, flag := range map[string]*bool{"all": &w.All, "found": &w.Found} {
		if raw := query.Get(name); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				return req, fmt.Errorf("%s must be true or false", name)
			}
			*flag = value
		}
	}
	return req, nil
}

func (d *Daemon) apiLedgerReadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	query := r.URL.Query()
	key := query.Get("projectKey")
	if !d.knownLedgerProject(w, r, key, "") {
		return
	}
	req, err := ledgerReadQuery(query)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := d.ledgerRead(key, req)
	var refusal *ledger.Refusal
	var missing *ledger.NotFound
	switch {
	case errors.As(err, &refusal):
		writeAPIError(w, http.StatusBadRequest, "invalid_request", refusal.Reason)
		return
	case errors.As(err, &missing):
		writeAPIError(w, http.StatusNotFound, "not_found", missing.Error()+".")
		return
	case err != nil:
		writeLedgerError(w, err)
		return
	}
	body, err := json.Marshal(result)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "encode_failed", err.Error())
		return
	}
	etag := ledgerETag(body)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if ledgerETagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func ledgerETagMatches(header, etag string) bool {
	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == etag || candidate == "*" {
			return true
		}
	}
	return false
}

type ledgerChange struct {
	ProjectKey string `json:"projectKey"`
	Version    string `json:"version"`
}

func writeLedgerChange(w http.ResponseWriter, change ledgerChange) {
	payload, _ := json.Marshal(change)
	fmt.Fprintf(w, "event: ledger.changed\ndata: %s\n\n", payload)
}

func (d *Daemon) apiLedgerEventsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "stream_unavailable", "Streaming is unavailable.")
		return
	}
	hub := d.ledgerChanges()
	id, wake, cancel := hub.subscribe()
	defer cancel()
	keys, err := d.ledgerStore().ProjectKeys()
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	sse.Open(w)
	for _, key := range keys {
		version, err := d.ledgerVersion(key)
		if err != nil {
			d.log.Printf("ledger events: version of %s: %s", key, err)
			continue
		}
		writeLedgerChange(w, ledgerChange{ProjectKey: key, Version: version})
	}
	flusher.Flush()
	authorized := func() bool { return d.apiEventStreamAuthorized(r) }
	sse.Follow(r.Context(), w, wake, func(_ struct{}, open bool) bool {
		if !open || !authorized() {
			return false
		}
		for _, change := range hub.take(id) {
			writeLedgerChange(w, change)
		}
		return sse.Flush(w) == nil
	}, authorized)
}

func (d *Daemon) ledgerChanges() *ledgerChangeHub {
	d.ledgerStore()
	return d.ledgerHub
}

func (d *Daemon) ledgerChanged(key string) {
	d.ledgerChanges().changed(key)
}

var ledgerChangeWait = time.Second
