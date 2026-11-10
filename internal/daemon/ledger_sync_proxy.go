package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/zm2231/agenthail/internal/hosts"
)

const ledgerProxyAttempts = 3

type ledgerProxyKey struct{}

func ledgerProxiedBy(r *http.Request) string {
	host, _ := r.Context().Value(ledgerProxyKey{}).(string)
	return host
}

func (d *Daemon) ledgerWriteRoute(next http.HandlerFunc) http.HandlerFunc {
	return d.idempotentActionHandler(func(w http.ResponseWriter, r *http.Request) {
		if hostID, isHost := requestingHost(r.Context()); isHost && hostID != "" {
			next(w, r.WithContext(context.WithValue(r.Context(), ledgerProxyKey{}, hostID)))
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
		r.Body = io.NopCloser(bytes.NewReader(body))
		var head struct {
			ProjectKey string `json:"projectKey"`
			SessionID  string `json:"sessionId"`
			Owner      string `json:"owner"`
		}
		_ = json.Unmarshal(body, &head)
		owner, err := d.ledgerStore().RemoteOwner(head.ProjectKey)
		if err != nil {
			writeLedgerError(w, err)
			return
		}
		if owner == "" {
			next(w, r)
			return
		}
		if !d.knownLedgerSession(w, r, head.SessionID) || !d.knownLedgerProject(w, r, head.ProjectKey, head.SessionID) {
			return
		}
		if head.Owner != "" && !d.knownLedgerSession(w, r, head.Owner) {
			return
		}
		d.proxyLedgerWrite(w, r, owner, head.ProjectKey, body)
	})
}

func (d *Daemon) proxyLedgerWrite(w http.ResponseWriter, r *http.Request, ownerID, key string, body []byte) {
	host, found := d.pairedHost(ownerID)
	name := ownerID
	if found {
		name = host.record.Name
	}
	unreachable := fmt.Sprintf("The ledger for %s is kept on %s, which this host cannot reach right now; nothing was recorded. Retry once %s is back.", key, name, name)
	if !found {
		writeAPIError(w, http.StatusServiceUnavailable, "ledger_owner_unreachable", unreachable)
		return
	}
	if state, _ := host.status(); state != hostStateConnected {
		writeAPIError(w, http.StatusServiceUnavailable, "ledger_owner_unreachable", unreachable)
		return
	}
	if _, err := d.syncLedgerProject(r.Context(), host, key); err != nil {
		var outdated ledgerHostOutdated
		if errors.As(err, &outdated) {
			writeLedgerOwnerOutdated(w, key, name, outdated.refusal)
			return
		}
		writeAPIError(w, http.StatusServiceUnavailable, "ledger_owner_unreachable", unreachable+" ("+err.Error()+")")
		return
	}
	header := http.Header{"Content-Type": {"application/json"}, actionIdempotencyHeader: {"ledger-" + uuid.NewString()}}
	var response *http.Response
	var err error
	for attempt := 1; attempt <= ledgerProxyAttempts; attempt++ {
		if response, err = host.client.Do(r.Context(), http.MethodPost, r.URL.Path, nil, body, header); err == nil {
			break
		}
		var notSent hosts.NotSentError
		if errors.As(err, &notSent) || r.Context().Err() != nil {
			writeAPIError(w, http.StatusServiceUnavailable, "ledger_owner_unreachable", unreachable)
			return
		}
		select {
		case <-r.Context().Done():
		case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
		}
	}
	if err != nil {
		writeAPIError(w, http.StatusGatewayTimeout, "ledger_owner_unconfirmed", fmt.Sprintf("%s did not confirm the write for %s after %d tries, so it may or may not be recorded; check agenthail ledger board before retrying.", name, key, ledgerProxyAttempts))
		return
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		writeAPIError(w, http.StatusGatewayTimeout, "ledger_owner_unconfirmed", fmt.Sprintf("%s's answer for %s was cut off, so the write may or may not be recorded; check agenthail ledger board before retrying.", name, key))
		return
	}
	var refusal struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if response.StatusCode == http.StatusBadRequest && json.Unmarshal(answer, &refusal) == nil && refusal.Error.Code == ledgerUnknownKeyCode {
		writeLedgerOwnerOutdated(w, key, name, refusal.Error.Message)
		return
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		host.ledgerMu.Lock()
		head, err := d.pullLedger(r.Context(), host, key)
		host.ledgerMu.Unlock()
		d.recordLedgerSync(key, head, err)
	}
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(answer)
}

func writeLedgerOwnerOutdated(w http.ResponseWriter, key, owner, refusal string) {
	writeAPIError(w, http.StatusServiceUnavailable, ledgerOwnerOutdatedCode, fmt.Sprintf("The ledger for %s is kept on %s, which does not know what this host sent (%s); nothing was recorded. Update Agenthail on %s, then retry.", key, owner, refusal, owner))
}
