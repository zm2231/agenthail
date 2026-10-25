package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) routeSource(ctx context.Context, request map[string]any, target *pairedHost) (bool, error) {
	if author, present := request["author"]; present {
		source, _ := request["sourceSessionId"].(string)
		if author != string(surface.AuthorPerson) && author != "" {
			return false, fmt.Errorf(`author must be "person" when set`)
		}
		if author == string(surface.AuthorPerson) && strings.TrimSpace(source) != "" {
			return false, fmt.Errorf("a person's message cannot also name a source session")
		}
	}
	named, err := d.routePersonName(ctx, request, target)
	if err != nil {
		return false, err
	}
	rewritten, err := d.routeSender(ctx, request, target)
	return named || rewritten, err
}

func (d *Daemon) routePersonName(ctx context.Context, request map[string]any, target *pairedHost) (bool, error) {
	rewritten := false
	if _, named := request["personName"]; named {
		if _, fromHost := requestingHost(ctx); !fromHost {
			delete(request, "personName")
			rewritten = true
		}
	}
	if _, named := request["personName"]; named || target == nil || request["author"] != string(surface.AuthorPerson) {
		return rewritten, nil
	}
	name, err := d.Registry.PersonName()
	if err != nil {
		return false, err
	}
	request["personName"] = name
	return true, nil
}

func (d *Daemon) routeSender(ctx context.Context, request map[string]any, target *pairedHost) (bool, error) {
	source, _ := request["sourceSessionId"].(string)
	source = strings.TrimSpace(source)
	senderName, _ := request["sourceName"].(string)
	delete(request, "sourceName")
	if source == "" {
		return senderName != "", nil
	}
	if target != nil {
		delete(request, "sourceSessionId")
		if own, err := d.Registry.ForwardSender(request, source); err != nil || own {
			return own, err
		}
		routed, rawID, err := d.routeSession(source)
		if err != nil {
			return false, err
		}
		if routed != nil {
			return false, fmt.Errorf("%s is a session on host %s; only that host can send as it", source, routed.record.Name)
		}
		request["sourceSessionId"] = hosts.Qualify(d.hostIdentity().ID, rawID)
		request["sourceName"] = d.senderName(rawID)
		return true, nil
	}
	principalHost, fromHost := requestingHost(ctx)
	hostRef, rawID, qualified := hosts.Split(source)
	if !fromHost {
		_, rawID, err := d.routeSession(source)
		if err != nil {
			return false, err
		}
		request["sourceSessionId"] = rawID
		return rawID != source, nil
	}
	if !qualified {
		return false, fmt.Errorf("host_mismatch: a host names its sender qualified with its own host id")
	}
	if principalHost == "" {
		return false, errUnboundHostToken
	}
	if hostRef != principalHost {
		return false, fmt.Errorf("host_mismatch: a host may only name its own sessions as the sender")
	}
	if host, paired := d.pairedHost(hostRef); paired {
		if owner, err := d.Registry.SessionHost(rawID); err == nil && owner == host.record.ID {
			request["sourceSessionId"] = rawID
			return true, nil
		}
	}
	if senderName = strings.TrimSpace(senderName); senderName == "" {
		senderName = rawID
	}
	sender, err := d.Registry.EnsureUnrepliableSender(principalHost, rawID, requestingHostName(ctx)+"/"+senderName)
	if err != nil {
		return false, err
	}
	request["sourceSessionId"] = sender
	return true, nil
}

var errUnboundHostToken = errors.New("this host was paired before hosts could name their sessions as senders; pair it again: run 'agenthail hosts invite' on this machine, then 'agenthail hosts add <pairing-link>' on the other host")

func (d *Daemon) senderName(sessionID string) string {
	if alias, err := d.Registry.ReverseAlias(sessionID); err == nil && alias != "" {
		return "@" + alias
	}
	if _, name, _, err := d.Registry.GetSession(sessionID); err == nil && name != "" {
		return name
	}
	return sessionID
}
