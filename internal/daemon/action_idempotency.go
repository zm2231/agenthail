package daemon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

type actionCaptureWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func newActionCaptureWriter() *actionCaptureWriter {
	return &actionCaptureWriter{header: make(http.Header)}
}

func (w *actionCaptureWriter) Header() http.Header { return w.header }

func (w *actionCaptureWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *actionCaptureWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len()+len(body) > actionReplayBodyLimit {
		w.overflow = true
		return 0, fmt.Errorf("action response exceeds %d bytes", actionReplayBodyLimit)
	}
	return w.body.Write(body)
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
		body, canonical, err := canonicalActionBody(r.Body)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "The action request is invalid.")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		hash := sha256.Sum256(canonical)
		principal := actionPrincipal(r)
		reservation, err := d.Registry.ReserveActionReceipt(principal, key, hex.EncodeToString(hash[:]), time.Now().UTC())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "action_reservation_failed", "The action reservation could not be recorded.")
			return
		}
		if !reservation.Acquired {
			if reservation.Receipt.RequestHash != hex.EncodeToString(hash[:]) {
				writeAPIError(w, http.StatusConflict, "action_idempotency_mismatch", "The Idempotency-Key was already used for a different action request.")
				return
			}
			if reservation.Receipt.Status == registry.ActionReceiptCompleted {
				if err := replayActionReceipt(w, reservation.Receipt.Receipt); err != nil {
					writeAPIError(w, http.StatusInternalServerError, "action_receipt_invalid", "The stored action receipt is invalid.")
				}
				return
			}
			writeActionSubmitted(w)
			return
		}

		capture := newActionCaptureWriter()
		next(capture, r)
		status := capture.status
		if status == 0 {
			status = http.StatusOK
		}
		replay, err := json.Marshal(actionReplay{Status: status, Headers: actionReplayHeaders(capture.header), Body: append([]byte(nil), capture.body.Bytes()...)})
		if capture.overflow || err != nil || len(replay) > actionReplayBodyLimit {
			writeActionSubmitted(w)
			return
		}
		if err := d.Registry.CompleteActionReceipt(principal, key, hex.EncodeToString(hash[:]), replay, time.Now().UTC()); err != nil {
			writeActionSubmitted(w)
			return
		}
		copyHTTPHeader(w.Header(), capture.header)
		w.WriteHeader(status)
		_, _ = w.Write(capture.body.Bytes())
	}
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

func actionPrincipal(r *http.Request) string {
	if principal, ok := r.Context().Value(actionPrincipalContextKey{}).(string); ok && principal != "" {
		return principal
	}
	return "local-dashboard"
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
