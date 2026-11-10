package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/zm2231/agenthail/internal/registry"
)

func replyWith(write func(http.ResponseWriter)) actionReply {
	recorder := newActionRecorder()
	write(recorder)
	return recorder.reply()
}

func (d *Daemon) routeHostAction(ctx context.Context, p principal, raw []byte, key string) actionReply {
	if len(raw) > actionRequestBodyLimit {
		return replyWith(func(w http.ResponseWriter) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid dashboard request")
		})
	}
	var request map[string]any
	if json.Unmarshal(raw, &request) != nil {
		return d.decodeAction(ctx, p, raw, key)
	}
	if author, _ := request["author"].(string); p.Kind == principalSession && author != "" {
		return replyWith(func(w http.ResponseWriter) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "a session cannot name an author; its messages are its own")
		})
	}
	host, rewritten, err := d.routeActionRequest(ctx, request)
	if err != nil {
		return replyWith(func(w http.ResponseWriter) {
			var owned registry.SessionOwnedError
			var failure *actionError
			switch {
			case errors.As(err, &owned):
				writeRouteError(w, err)
			case errors.As(err, &failure):
				writeActionResult(w, actionResult{}, err)
			default:
				writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			}
		})
	}
	if host == nil {
		if rewritten {
			raw, _ = json.Marshal(request)
		}
		return d.decodeAction(ctx, p, raw, key)
	}
	if err := d.hostTransit(ctx, host); err != nil {
		return replyWith(func(w http.ResponseWriter) { writeActionResult(w, actionResult{}, err) })
	}
	if err := incompatibleHost(host); err != nil {
		return replyWith(func(w http.ResponseWriter) { writeActionResult(w, actionResult{}, err) })
	}
	body, _ := json.Marshal(request)
	return d.forwardHostAction(ctx, p, key, host, request, body)
}

func (d *Daemon) decodeAction(ctx context.Context, p principal, raw []byte, key string) actionReply {
	var request dashboardActionRequest
	if err := json.NewDecoder(io.LimitReader(bytes.NewReader(raw), actionRequestBodyLimit)).Decode(&request); err != nil {
		return replyWith(func(w http.ResponseWriter) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid dashboard request: "+err.Error())
		})
	}
	return d.doAction(withActionKey(ctx, key), p, request)
}

func apiV1Reply(reply actionReply) actionReply {
	return replyWith(func(w http.ResponseWriter) { apiV1Respond(w, reply.write) })
}
