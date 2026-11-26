package daemon

import (
	"slices"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/targets"
)

// A session an agent started stays that agent's on every computer and in
// every project. Only the computer that runs a session records who started
// it, so its starter chain decides: this host's own record for a session
// here, the owning host's answer (or, while it is down, its last published
// rows) for a session there.

type starterTie uint8

const (
	untied starterTie = iota
	// tieStartedCaller: the paired host's session started the caller.
	tieStartedCaller
	// tieCallerStarted: the caller started the paired host's session.
	tieCallerStarted
)

func (d *Daemon) remoteStarterTie(caller *mcpCaller, remote targets.Remote) (starterTie, error) {
	if slices.Contains(remote.StartedBy, caller.session.ID) {
		return tieCallerStarted, nil
	}
	chain, err := d.starterChain(caller.session.ID)
	if err != nil {
		return untied, err
	}
	if slices.Contains(chain, remote.ID) {
		return tieStartedCaller, nil
	}
	return untied, nil
}

func (d *Daemon) starterChain(sessionID string) ([]string, error) {
	ancestors, err := d.Registry.SessionAncestors(sessionID)
	if err != nil {
		return nil, err
	}
	chain := make([]string, 0, len(ancestors))
	for _, id := range ancestors {
		chain = append(chain, d.hostQualified(id))
	}
	return chain, nil
}

func (d *Daemon) startedVia(sourceID, sessionID string) (bool, error) {
	if sourceID == "" {
		return false, nil
	}
	chain, err := d.starterChain(sessionID)
	return slices.Contains(chain, d.hostQualified(sourceID)), err
}

func (d *Daemon) hostQualified(id string) string {
	host, _ := d.Registry.SessionHost(id)
	return registry.QualifiedStarter(host, id)
}

// fromPairedHost names, as this host does, a session id a paired host
// reported: one of that host's own qualified by it, one of this host's
// unqualified.
func (d *Daemon) fromPairedHost(hostID, id string) string {
	id = hosts.Qualify(hostID, id)
	if ref, raw, qualified := hosts.Split(id); qualified && d.isLocalHostRef(ref) {
		return raw
	}
	return id
}

func mirroredStarterChain(id string, rows map[string]registry.HostSessionRef) []string {
	var chain []string
	row, found := rows[id]
	for found && row.StartedBy != "" && len(chain) < maxStarterChain {
		chain = append(chain, row.StartedBy)
		host, _, qualified := hosts.Split(row.StartedBy)
		if !qualified || host != row.HostID {
			break
		}
		row, found = rows[row.StartedBy]
	}
	return chain
}

const maxStarterChain = 64

func mirroredRows(refs []registry.HostSessionRef) map[string]registry.HostSessionRef {
	rows := make(map[string]registry.HostSessionRef, len(refs))
	for _, ref := range refs {
		rows[hosts.Qualify(ref.HostID, ref.ID)] = ref
	}
	return rows
}
