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
		body = d.stampPark(r.Context(), r.URL.Path, body, time.Now())
		if body, err = d.stampTakeover(r.Context(), r.URL.Path, body); err != nil {
			writeLedgerError(w, err)
			return
		}
		if err := d.refuseReviewPass(r.URL.Path, body); err != nil {
			writeAPIError(w, http.StatusForbidden, "forbidden", err.Error())
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
	reply, failure := d.writeAtLedgerOwner(r.Context(), ownerID, key, r.URL.Path, body)
	if failure != nil {
		writeAPIError(w, failure.status, failure.code, failure.message)
		return
	}
	w.Header().Set("Content-Type", reply.contentType)
	w.WriteHeader(reply.status)
	_, _ = w.Write(reply.body)
}

type ledgerOwnerReply struct {
	status      int
	contentType string
	body        []byte
}

type ledgerOwnerFailure struct {
	status        int
	code, message string
}

func (f *ledgerOwnerFailure) Error() string { return f.message }

func (d *Daemon) writeAtLedgerOwner(ctx context.Context, ownerID, key, path string, body []byte) (ledgerOwnerReply, *ledgerOwnerFailure) {
	host, found := d.pairedHost(ownerID)
	name := ownerID
	if found {
		name = host.record.Name
	}
	unreachable := fmt.Sprintf("The ledger for %s is kept on %s, which this host cannot reach right now; nothing was recorded. Retry once %s is back.", key, name, name)
	fail := func(status int, code, message string) (ledgerOwnerReply, *ledgerOwnerFailure) {
		return ledgerOwnerReply{}, &ledgerOwnerFailure{status: status, code: code, message: message}
	}
	if !found {
		return fail(http.StatusServiceUnavailable, "ledger_owner_unreachable", unreachable)
	}
	if state, _ := host.status(); state != hostStateConnected {
		return fail(http.StatusServiceUnavailable, "ledger_owner_unreachable", unreachable)
	}
	if _, err := d.syncLedgerProject(ctx, host, key); err != nil {
		var outdated ledgerHostOutdated
		if errors.As(err, &outdated) {
			return fail(http.StatusServiceUnavailable, ledgerOwnerOutdatedCode, ledgerOwnerOutdatedMessage(key, name, outdated.refusal))
		}
		return fail(http.StatusServiceUnavailable, "ledger_owner_unreachable", unreachable+" ("+err.Error()+")")
	}
	header := http.Header{"Content-Type": {"application/json"}, actionIdempotencyHeader: {"ledger-" + uuid.NewString()}}
	var response *http.Response
	var err error
	for attempt := 1; attempt <= ledgerProxyAttempts; attempt++ {
		if response, err = host.client.Do(ctx, http.MethodPost, path, nil, body, header); err == nil {
			break
		}
		var notSent hosts.NotSentError
		if errors.As(err, &notSent) || ctx.Err() != nil {
			return fail(http.StatusServiceUnavailable, "ledger_owner_unreachable", unreachable)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
		}
	}
	if err != nil {
		return fail(http.StatusGatewayTimeout, "ledger_owner_unconfirmed", fmt.Sprintf("%s did not confirm the write for %s after %d tries, so it may or may not be recorded; check agenthail ledger board before retrying.", name, key, ledgerProxyAttempts))
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return fail(http.StatusGatewayTimeout, "ledger_owner_unconfirmed", fmt.Sprintf("%s's answer for %s was cut off, so the write may or may not be recorded; check agenthail ledger board before retrying.", name, key))
	}
	var refusal struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if response.StatusCode == http.StatusBadRequest && json.Unmarshal(answer, &refusal) == nil && refusal.Error.Code == ledgerUnknownKeyCode {
		return fail(http.StatusServiceUnavailable, ledgerOwnerOutdatedCode, ledgerOwnerOutdatedMessage(key, name, refusal.Error.Message))
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		host.ledgerMu.Lock()
		head, err := d.pullLedger(ctx, host, key)
		host.ledgerMu.Unlock()
		d.recordLedgerSync(key, head, err)
	}
	return ledgerOwnerReply{status: response.StatusCode, contentType: response.Header.Get("Content-Type"), body: answer}, nil
}

func ledgerOwnerOutdatedMessage(key, owner, refusal string) string {
	return fmt.Sprintf("The ledger for %s is kept on %s, which does not know what this host sent (%s); nothing was recorded. Update Agenthail on %s, then retry.", key, owner, refusal, owner)
}
