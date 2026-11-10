package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
)

var actionSessionFields = []string{"sessionId", "targetId", "fromId", "toId"}

func (d *Daemon) routeActionRequest(ctx context.Context, request map[string]any) (*pairedHost, bool, error) {
	if err := d.checkNamedSender(ctx, request); err != nil {
		return nil, false, err
	}
	var host *pairedHost
	local := false
	rewritten := false
	var named *pairedHost
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
	if action == "respond" && d.holdsAsk(request) {
		return nil, rewritten, nil
	}
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
		queueRowOnNamedHost := named == host && action == "queue-cancel"
		if strings.HasPrefix(action, "queue-") && !queueRowOnNamedHost {
			return nil, false, fmt.Errorf("%s is not available for sessions on another host yet", action)
		}
	}
	if err := d.movedFromHere(ctx, host, request); err != nil {
		return nil, false, err
	}
	sourceRewritten, err := d.routeSource(ctx, request, host)
	if err != nil {
		return nil, false, err
	}
	return host, rewritten || sourceRewritten, nil
}

// A message this host failed to relay to a paired host's session raises its ask here, not on that host.
func (d *Daemon) holdsAsk(request map[string]any) bool {
	id, _ := request["requestId"].(string)
	sessionID, _ := request["sessionId"].(string)
	if id == "" || sessionID == "" {
		return false
	}
	ask, found, err := d.Registry.Ask(id)
	return err == nil && found && ask.SessionID == sessionID
}
