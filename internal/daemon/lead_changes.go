package daemon

import "sync"

type leadWatch struct {
	mu   sync.Mutex
	last string
}

func (d *Daemon) leadsChanged() { d.syncLeads().Wait() }

// syncLeads returns once credentials are reconciled; the deliveries it starts run on, so a slow runtime never holds up the status loop.
func (d *Daemon) syncLeads() *sync.WaitGroup {
	var deliveries sync.WaitGroup
	fingerprint, err := d.Registry.LeadsFingerprint()
	if err != nil {
		d.log.Printf("catalog leads: %s", err)
		return &deliveries
	}
	d.leads.mu.Lock()
	undelivered, err := d.reconcileCredentials()
	if err != nil {
		d.log.Printf("mcp: credentials are retried: %s", err)
	}
	changed := fingerprint != d.leads.last
	d.leads.last = fingerprint
	d.leads.mu.Unlock()
	d.deliverCredentials(&deliveries, undelivered)
	if !changed {
		return &deliveries
	}
	if d.dashboard != nil {
		d.dashboard.invalidate()
	}
	d.publishCatalogPresence()
	return &deliveries
}
