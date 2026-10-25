package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

func (d *Daemon) hostRoutedAction(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next(w, r)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, actionRequestBodyLimit+1))
		r.Body.Close()
		if err != nil || len(raw) > actionRequestBodyLimit {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid dashboard request")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var request map[string]any
		if json.Unmarshal(raw, &request) != nil {
			next(w, r)
			return
		}
		host, rewritten, err := d.routeActionRequest(r.Context(), request)
		if err != nil {
			var owned registry.SessionOwnedError
			if errors.As(err, &owned) {
				writeRouteError(w, err)
				return
			}
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if host == nil {
			if rewritten {
				raw, _ = json.Marshal(request)
				r.Body = io.NopCloser(bytes.NewReader(raw))
			}
			next(w, r)
			return
		}
		if !writeIncompatibleHost(w, host) {
			return
		}
		body, _ := json.Marshal(request)
		d.forwardHostAction(w, r, host, request, body)
	}
}

var actionSessionFields = []string{"sessionId", "targetId", "fromId", "toId"}

func (d *Daemon) routeActionRequest(ctx context.Context, request map[string]any) (*pairedHost, bool, error) {
	var host *pairedHost
	local := false
	rewritten := false
	assign := func(candidate *pairedHost) error {
		if candidate == nil {
			local = true
		} else if host != nil && host != candidate {
			return fmt.Errorf("an action cannot span hosts %s and %s", host.record.Name, candidate.record.Name)
		} else {
			host = candidate
		}
		if host != nil && local {
			return fmt.Errorf("an action cannot span this host and host %s", host.record.Name)
		}
		return nil
	}
	if hostRef, ok := request["host"].(string); ok {
		delete(request, "host")
		rewritten = true
		if hostRef = strings.TrimSpace(hostRef); hostRef != "" {
			var named *pairedHost
			if !d.isLocalHostRef(hostRef) {
				found, exists := d.pairedHost(hostRef)
				if !exists {
					return nil, false, fmt.Errorf("no paired host is named %q", hostRef)
				}
				named = found
			}
			if err := assign(named); err != nil {
				return nil, false, err
			}
		}
	}
	if value, ok := request["projectId"].(string); ok {
		if hostRef, target, qualified := hosts.Split(strings.TrimSpace(value)); qualified {
			var owner *pairedHost
			if !d.isLocalHostRef(hostRef) {
				found, exists := d.pairedHost(hostRef)
				if !exists {
					return nil, false, fmt.Errorf("%w %q", errUnknownHost, hostRef)
				}
				owner = found
			}
			if err := assign(owner); err != nil {
				return nil, false, err
			}
			request["projectId"] = target
			rewritten = true
		}
	}
	action, _ := request["action"].(string)
	if strings.HasPrefix(action, "channel-") || strings.HasPrefix(action, "relay-") {
		return d.routeLinkAction(ctx, request, action, host, local, rewritten)
	}
	for _, field := range actionSessionFields {
		value, ok := request[field].(string)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		routed, rawID, err := d.routeSession(value)
		if err != nil {
			return nil, false, err
		}
		if err := assign(routed); err != nil {
			return nil, false, err
		}
		if rawID != value {
			request[field] = rawID
			rewritten = true
		}
	}
	if host != nil {
		if action == "session-focus" {
			return nil, false, fmt.Errorf("opening the terminal is not available for sessions on another host yet; this session runs on host %s", host.record.Name)
		}
		if strings.HasPrefix(action, "queue-") || action == "delivery-dismiss" {
			return nil, false, fmt.Errorf("%s is not available for sessions on another host yet", action)
		}
	}
	sourceRewritten, err := d.routeSource(ctx, request, host)
	if err != nil {
		return nil, false, err
	}
	return host, rewritten || sourceRewritten, nil
}
