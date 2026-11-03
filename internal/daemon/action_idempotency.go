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
		d.idempotentReply(principalFromRequest(r).ID, key, raw, func(body []byte) actionReply {
			r.Body = io.NopCloser(bytes.NewReader(body))
			recorder := newActionRecorder()
			next(recorder, r)
			return recorder.reply()
		}).write(w)
	}
}

func (d *Daemon) idempotentReply(principalID, rawKey string, body []byte, run func([]byte) actionReply) actionReply {
	key := strings.TrimSpace(rawKey)
	if key == "" {
		return run(body)
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
	hash := sha256.Sum256(canonical)
	requestHash := hex.EncodeToString(hash[:])
	reservation, err := d.Registry.ReserveActionReceipt(principalID, key, requestHash, time.Now().UTC())
	if err != nil {
		return replyWith(func(w http.ResponseWriter) {
			writeAPIError(w, http.StatusInternalServerError, "action_reservation_failed", "The action reservation could not be recorded.")
		})
	}
	if !reservation.Acquired {
		return replyWith(func(w http.ResponseWriter) {
			switch {
			case reservation.Receipt.RequestHash != requestHash:
				writeAPIError(w, http.StatusConflict, "action_idempotency_mismatch", "The Idempotency-Key was already used for a different action request.")
			case reservation.Receipt.Status == registry.ActionReceiptCompleted:
				if err := replayActionReceipt(w, reservation.Receipt.Receipt); err != nil {
					writeAPIError(w, http.StatusInternalServerError, "action_receipt_invalid", "The stored action receipt is invalid.")
				}
			default:
				writeActionSubmitted(w)
			}
		})
	}
	reply := run(body)
	replay, err := json.Marshal(actionReplay{Status: reply.Status, Headers: actionReplayHeaders(reply.Header), Body: append([]byte(nil), reply.Body...)})
	if len(reply.Body) > actionReplayBodyLimit || err != nil || len(replay) > actionReplayBodyLimit {
		return replyWith(writeActionSubmitted)
	}
	if err := d.Registry.CompleteActionReceipt(principalID, key, requestHash, replay, time.Now().UTC()); err != nil {
		return replyWith(writeActionSubmitted)
	}
	return reply
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
