package daemon

import "sync"

type leadWatch struct {
	mu       sync.Mutex
	last     string
	sessions map[string]bool
}

func (d *Daemon) syncLeads() {
	fingerprint, err := d.Registry.LeadsFingerprint()
	if err != nil {
		d.log.Printf("catalog leads: %s", err)
		return
	}
	d.leads.mu.Lock()
	defer d.leads.mu.Unlock()
	if fingerprint == d.leads.last {
		return
	}
	d.leads.last = fingerprint
	if current, err := d.Registry.LocalLeadSessions(); err != nil {
		d.log.Printf("catalog leads: %s", err)
	} else {
		d.reconcileLeadCredentials(d.leads.sessions, current)
		d.leads.sessions = current
	}
	if d.dashboard != nil {
		d.dashboard.invalidate()
	}
	d.publishCatalogPresence()
}
