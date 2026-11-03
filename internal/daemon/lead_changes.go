package daemon

import "sync"

type leadWatch struct {
	mu   sync.Mutex
	last string
}

func (d *Daemon) syncLeads() {
	fingerprint, err := d.Registry.LeadsFingerprint()
	if err != nil {
		d.log.Printf("catalog leads: %s", err)
		return
	}
	d.leads.mu.Lock()
	undelivered, err := d.reconcileCredentials()
	if err != nil {
		d.log.Printf("mcp: credentials are retried: %s", err)
	}
	changed := fingerprint != d.leads.last
	d.leads.last = fingerprint
	d.leads.mu.Unlock()
	d.deliverCredentials(undelivered)
	if !changed {
		return
	}
	if d.dashboard != nil {
		d.dashboard.invalidate()
	}
	d.publishCatalogPresence()
}
