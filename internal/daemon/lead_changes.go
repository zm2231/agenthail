package daemon

import "sync"

type leadWatch struct {
	mu         sync.Mutex
	last       string
	reconciled string
	sessions   map[string]bool
	unsettled  map[string]bool
}

func (w *leadWatch) unsettle(sessionIDs ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.unsettled == nil {
		w.unsettled = map[string]bool{}
	}
	for _, id := range sessionIDs {
		w.unsettled[id] = true
	}
}

func (d *Daemon) settleSessions() []func() {
	var deliveries []func()
	for id := range d.leads.unsettled {
		deliver, err := d.reconcileSessionCredential(id)
		if err != nil {
			d.log.Printf("mcp: credential is retried: %s", err)
			continue
		}
		delete(d.leads.unsettled, id)
		deliveries = appendDelivery(deliveries, deliver)
	}
	return deliveries
}

func (d *Daemon) syncLeads() {
	fingerprint, err := d.Registry.LeadsFingerprint()
	if err != nil {
		d.log.Printf("catalog leads: %s", err)
		return
	}
	var deliveries []func()
	d.leads.mu.Lock()
	if fingerprint != d.leads.reconciled {
		reconciled, err := d.reconcileLeads(fingerprint)
		if err != nil {
			d.log.Printf("mcp: lead credentials are retried: %s", err)
		}
		deliveries = reconciled
	}
	deliveries = append(deliveries, d.settleSessions()...)
	changed := fingerprint != d.leads.last
	d.leads.last = fingerprint
	d.leads.mu.Unlock()
	for _, deliver := range deliveries {
		deliver()
	}
	if !changed {
		return
	}
	if d.dashboard != nil {
		d.dashboard.invalidate()
	}
	d.publishCatalogPresence()
}

func (d *Daemon) reconcileLeads(fingerprint string) ([]func(), error) {
	current, err := d.Registry.LocalLeadSessions()
	if err != nil {
		return nil, err
	}
	deliveries, err := d.reconcileLeadCredentials(d.leads.sessions, current)
	if err != nil {
		return deliveries, err
	}
	d.leads.sessions, d.leads.reconciled = current, fingerprint
	return deliveries, nil
}
