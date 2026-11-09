package daemon

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
)

type actionFormat int

const (
	actionFormatDashboard actionFormat = iota
	actionFormatAPIv1
)

type actionReply struct {
	Status int
	Header http.Header
	Body   []byte
}

func (reply actionReply) write(w http.ResponseWriter) {
	copyHTTPHeader(w.Header(), reply.Header)
	w.WriteHeader(reply.Status)
	_, _ = w.Write(reply.Body)
}

type actionRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newActionRecorder() *actionRecorder {
	return &actionRecorder{header: make(http.Header)}
}

func (w *actionRecorder) Header() http.Header { return w.header }

func (w *actionRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *actionRecorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(body)
}

func (w *actionRecorder) reply() actionReply {
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	return actionReply{Status: status, Header: w.header, Body: w.body.Bytes()}
}

type actionKeyContextKey struct{}

// performAction runs one action for any front end: idempotency, routing to the
// host that owns its target, decoding and dispatch.
func (d *Daemon) performAction(ctx context.Context, p principal, format actionFormat, body []byte, idempotencyKey string) actionReply {
	ctx = p.context(ctx)
	return d.idempotentReply(p.ID, idempotencyKey, body, mcpRequestHash(ctx), func(body []byte) actionReply {
		reply := d.routeHostAction(ctx, p, body, idempotencyKey)
		if format == actionFormatAPIv1 {
			reply = apiV1Reply(reply)
		}
		return reply
	})
}

func withActionKey(ctx context.Context, idempotencyKey string) context.Context {
	return context.WithValue(ctx, actionKeyContextKey{}, idempotencyKey)
}

func actionKey(ctx context.Context) string {
	key, _ := ctx.Value(actionKeyContextKey{}).(string)
	return key
}

// doAction dispatches one decoded action and announces a state change when it succeeds.
func (d *Daemon) doAction(ctx context.Context, p principal, request dashboardActionRequest) actionReply {
	recorder := newActionRecorder()
	result, err := d.dispatchAction(p.context(ctx), p, request)
	writeActionResult(recorder, result, d.movedAnswer(err))
	reply := recorder.reply()
	if reply.Status >= http.StatusOK && reply.Status < http.StatusMultipleChoices {
		d.publishEvent("state.changed", "", map[string]string{"source": "action"})
	}
	return reply
}

func (d *Daemon) actionsHTTPHandler(format actionFormat) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		key := r.Header.Get(actionIdempotencyHeader)
		if len(strings.TrimSpace(key)) > registry.ActionReceiptKeyLimit {
			writeAPIError(w, http.StatusBadRequest, "invalid_idempotency_key", "The Idempotency-Key is too long.")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, actionRequestBodyLimit+1))
		r.Body.Close()
		if err != nil {
			if strings.TrimSpace(key) != "" {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "The action request is invalid.")
				return
			}
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid dashboard request")
			return
		}
		d.performAction(r.Context(), principalFromRequest(r), format, body, key).write(w)
	}
}
