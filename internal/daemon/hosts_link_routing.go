package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
)

func (d *Daemon) routeLinkAction(ctx context.Context, request map[string]any, action string, named *pairedHost, namedLocal, rewritten bool) (*pairedHost, bool, error) {
	owner := named
	if action == "relay-add" {
		from, _ := request["fromId"].(string)
		routed, rawID, err := d.routeSession(from)
		if err != nil {
			return nil, false, err
		}
		if (named != nil || namedLocal) && routed != named {
			return nil, false, fmt.Errorf("a relay lives on the host of its source; %s is not on the host this request names", from)
		}
		owner = routed
		if rawID != from {
			request["fromId"], rewritten = rawID, true
		}
	}
	for _, field := range []string{"toId", "targetId", "sessionId"} {
		value, ok := request[field].(string)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		relative, err := d.sessionRelativeTo(owner, value)
		if err != nil {
			return nil, false, err
		}
		if relative != value {
			request[field], rewritten = relative, true
		}
	}
	sourceRewritten, err := d.routeSource(ctx, request, owner)
	if err != nil {
		return nil, false, err
	}
	return owner, rewritten || sourceRewritten, nil
}

func (d *Daemon) sessionRelativeTo(owner *pairedHost, ref string) (string, error) {
	routed, rawID, err := d.routeSession(ref)
	if errors.Is(err, errUnknownHost) {
		return "", fmt.Errorf("%s is on a host this host has not paired; pair it here first: run 'agenthail hosts invite' on that host, then 'agenthail hosts add <pairing-link>' here", ref)
	}
	if err != nil {
		return "", err
	}
	switch {
	case routed == owner:
		return rawID, nil
	case routed == nil:
		return hosts.Qualify(d.hostIdentity().ID, rawID), nil
	case owner == nil:
		return rawID, nil
	default:
		return "", fmt.Errorf("%s is on host %s; a relay or channel on host %s can reach only that host's and this host's sessions", ref, routed.record.Name, owner.record.Name)
	}
}

func (d *Daemon) hostOwned(sessionID string) bool {
	owner, err := d.Registry.SessionHost(sessionID)
	return err == nil && owner != ""
}

func (d *Daemon) resolveLinkTarget(ref string) (string, error) {
	if d.hostOwned(ref) {
		return ref, nil
	}
	return d.Registry.ResolveTarget(ref)
}
