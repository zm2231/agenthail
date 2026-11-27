package daemon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

const (
	actionIdempotencyHeader = "Idempotency-Key"
	actionRequestBodyLimit  = 140 << 10
	actionReplayBodyLimit   = registry.ActionReceiptBodyLimit
)

type actionPrincipalContextKey struct{}

type actionReplay struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"headers,omitempty"`
	Body    []byte      `json:"body"`
}

func (d *Daemon) idempotentActionHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next(w, r)
			return
		}
		key := strings.TrimSpace(r.Header.Get(actionIdempotencyHeader))
		if key == "" {
			next(w, r)
			return
		}
		if len(key) > registry.ActionReceiptKeyLimit {
			writeAPIError(w, http.StatusBadRequest, "invalid_idempotency_key", "The Idempotency-Key is too long.")
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, actionRequestBodyLimit+1))
		r.Body.Close()
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "The action request is invalid.")
			return
		}
		d.idempotentReply(principalFromRequest(r).ID, key, raw, mcpRequestHash(r.Context()), func(_ time.Time, body []byte) actionReply {
			r.Body = io.NopCloser(bytes.NewReader(body))
			recorder := newActionRecorder()
			next(recorder, r)
			return recorder.reply()
		}).write(w)
	}
}

func (d *Daemon) idempotentReply(principalID, rawKey string, body []byte, requestHash string, run func(firstSent time.Time, body []byte) actionReply) actionReply {
	key := strings.TrimSpace(rawKey)
	if key == "" {
		return run(time.Now().UTC(), body)
	}
	if len(key) > registry.ActionReceiptKeyLimit {
		return replyWith(func(w http.ResponseWriter) {
			writeAPIError(w, http.StatusBadRequest, "invalid_idempotency_key", "The Idempotency-Key is too long.")
		})
	}
	body, canonical, err := canonicalActionBody(io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return replyWith(func(w http.ResponseWriter) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "The action request is invalid.")
		})
	}
	if requestHash == "" {
		hash := sha256.Sum256(canonical)
		requestHash = hex.EncodeToString(hash[:])
	}
	claim, err := d.actionRuns.claim(d.Registry, principalID, key, requestHash, canonical)
	if err != nil {
		return replyWith(func(w http.ResponseWriter) {
			writeAPIError(w, http.StatusInternalServerError, "action_reservation_failed", "The action reservation could not be recorded.")
		})
	}
	if !claim.Acquired {
		return replyWith(func(w http.ResponseWriter) {
			switch {
			case claim.Receipt.RequestHash != requestHash:
				writeAPIError(w, http.StatusConflict, "action_idempotency_mismatch", "The Idempotency-Key was already used for a different action request.")
			case claim.Receipt.Status == registry.ActionReceiptCompleted:
				if err := replayActionReceipt(w, claim.Receipt.Receipt); err != nil {
					writeAPIError(w, http.StatusInternalServerError, "action_receipt_invalid", "The stored action receipt is invalid.")
				}
			default:
				writeActionSubmitted(w)
			}
		})
	}
	defer d.actionRuns.done(principalID, key)
	firstSent, err := time.Parse(time.RFC3339Nano, claim.Receipt.CreatedAt)
	if err != nil {
		firstSent = time.Now().UTC()
	}
	reply := run(firstSent, body)
	now := time.Now().UTC()
	if provisionalActionReply(reply) {
		if err := d.Registry.RetryActionReceipt(principalID, key, requestHash, now); err != nil {
			d.logRuntimeError("action receipt "+key, err)
		}
		return reply
	}
	replay, err := encodeActionReplay(reply)
	if err == nil {
		err = d.Registry.CompleteActionReceipt(principalID, key, requestHash, replay, now)
	}
	if err != nil {
		d.logRuntimeError("action receipt "+key, err)
	}
	return reply
}

// A reply over the receipt limit is kept as its status and a short body, so a
// resend learns whether the action succeeded rather than waiting forever.
func encodeActionReplay(reply actionReply) ([]byte, error) {
	encode := func(reply actionReply) ([]byte, error) {
		return json.Marshal(actionReplay{Status: reply.Status, Headers: actionReplayHeaders(reply.Header), Body: append([]byte(nil), reply.Body...)})
	}
	replay, err := encode(reply)
	if err == nil && len(reply.Body) <= actionReplayBodyLimit && len(replay) <= actionReplayBodyLimit {
		return replay, nil
	}
	status := reply.Status
	return encode(replyWith(func(w http.ResponseWriter) {
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			writeDashboardJSON(w, status, map[string]any{"ok": true, "status": "completed", "detail": "The action completed; its reply was too large to keep for a resend."})
			return
		}
		writeAPIError(w, status, "action_reply_too_large", "The action failed; its reply was too large to keep for a resend.")
	}))
}

// A paired host that was not reached, did not answer, or is still running the
// request has not given this action's outcome. A resend under the same key
// forwards again, and the host's own receipt for that key keeps it from
// running twice.
func provisionalActionReply(reply actionReply) bool {
	var body map[string]json.RawMessage
	if json.Unmarshal(reply.Body, &body) != nil {
		return false
	}
	var failure struct{ Code string }
	_ = json.Unmarshal(body["error"], &failure)
	switch reply.Status {
	case http.StatusAccepted:
		return len(body) == 2 && string(body["ok"]) == "true" && string(body["status"]) == `"submitted"`
	case http.StatusServiceUnavailable:
		return failure.Code == "host_unreachable"
	case http.StatusGatewayTimeout:
		return failure.Code == "host_outcome_unknown"
	}
	return false
}

func canonicalActionBody(body io.ReadCloser) ([]byte, []byte, error) {
	if body == nil {
		body = io.NopCloser(strings.NewReader(""))
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, actionRequestBodyLimit+1))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > actionRequestBodyLimit {
		return nil, nil, errors.New("action request is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, nil, errors.New("multiple JSON values are not allowed")
		}
		return nil, nil, err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, nil, err
	}
	return raw, canonical, nil
}

func replayActionReceipt(w http.ResponseWriter, encoded []byte) error {
	var replay actionReplay
	if err := json.Unmarshal(encoded, &replay); err != nil || replay.Status < 100 || replay.Status > 599 || len(replay.Body) > actionReplayBodyLimit {
		return errors.New("invalid action replay")
	}
	copyHTTPHeader(w.Header(), replay.Headers)
	w.WriteHeader(replay.Status)
	_, err := w.Write(replay.Body)
	return err
}

func actionReplayHeaders(source http.Header) http.Header {
	result := make(http.Header)
	if values := source.Values("Content-Type"); len(values) > 0 {
		result["Content-Type"] = append([]string(nil), values...)
	}
	return result
}

func writeActionSubmitted(w http.ResponseWriter) {
	writeDashboardJSON(w, http.StatusAccepted, map[string]any{"ok": true, "status": "submitted"})
}
