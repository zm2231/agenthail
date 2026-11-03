package daemon

import "sync"

type leadWatch struct {
	mu         sync.Mutex
	last       string
	reconciled string
	sessions   map[string]bool
	unsettled  map[string]bool
}

func (w *leadWatch) unsettle(sessionID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.unsettled == nil {
		w.unsettled = map[string]bool{}
	}
	w.unsettled[sessionID] = true
}

func (d *Daemon) settleStartedSessions() {
	for id := range d.leads.unsettled {
		if err := d.reconcileSessionCredential(id); err != nil {
			d.log.Printf("mcp: started coordinator credential is retried: %s", err)
			continue
		}
		delete(d.leads.unsettled, id)
	}
}

func (d *Daemon) syncLeads() {
	fingerprint, err := d.Registry.LeadsFingerprint()
	if err != nil {
		d.log.Printf("catalog leads: %s", err)
		return
	}
	d.leads.mu.Lock()
	if fingerprint != d.leads.reconciled {
		if err := d.reconcileLeads(fingerprint); err != nil {
			d.log.Printf("mcp: lead credentials are retried: %s", err)
		}
	}
	d.settleStartedSessions()
	changed := fingerprint != d.leads.last
	d.leads.last = fingerprint
	d.leads.mu.Unlock()
	if !changed {
		return
	}
	if d.dashboard != nil {
		d.dashboard.invalidate()
	}
	d.publishCatalogPresence()
}

func (d *Daemon) reconcileLeads(fingerprint string) error {
	current, err := d.Registry.LocalLeadSessions()
	if err != nil {
		return err
	}
	if err := d.reconcileLeadCredentials(d.leads.sessions, current); err != nil {
		return err
	}
	d.leads.sessions, d.leads.reconciled = current, fingerprint
	return nil
}
