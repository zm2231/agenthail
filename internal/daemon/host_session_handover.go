package daemon

import (
	"github.com/zm2231/agenthail/internal/hosts"
)

const refusedRowsKept = 1024

func (h *pairedHost) keepRefused(row dashboardSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.refused == nil {
		h.refused = map[string]dashboardSession{}
	}
	if _, kept := h.refused[row.ID]; kept || len(h.refused) < refusedRowsKept {
		h.refused[row.ID] = row
	}
}

func (h *pairedHost) dropRefused(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.refused, id)
}

func (h *pairedHost) takeRefused(id string) (dashboardSession, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	row, kept := h.refused[id]
	delete(h.refused, id)
	return row, kept
}

func (d *Daemon) handOverHostSession(from *pairedHost, sessionID, toID string) bool {
	to, found := d.pairedHost(toID)
	if !found || to == from {
		return false
	}
	d.catalog.mu.Lock()
	handed, err := d.Registry.HandOverHostSession(from.record.ID, sessionID, hosts.Qualify(from.record.ID, sessionID), to.record.ID)
	if err == nil && handed {
		d.publishCommittedHostWriteLocked("host " + from.record.Name + " hand over " + sessionID)
	}
	d.catalog.mu.Unlock()
	if err != nil {
		d.logRuntimeError("host "+from.record.Name+" hand over "+sessionID, err)
		return false
	}
	if !handed {
		return false
	}
	from.setHidden(sessionID, nil)
	if row, kept := to.takeRefused(sessionID); kept {
		if err := d.mirrorOrHide(to, row, true); err != nil {
			d.logRuntimeError("host "+to.record.Name+" mirror "+sessionID, err)
		}
	}
	return true
}

func (d *Daemon) ownedBy(host *pairedHost, sessionID string) bool {
	owner, err := d.Registry.SessionHost(sessionID)
	return err == nil && owner == host.record.ID
}
