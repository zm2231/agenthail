package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
)

// reidentifyCatalogRows gives every local row the project its folder belongs
// to now, once per change to the added projects or links. A pass re-resolves
// only the sessions its surface lists, so without this an ended session keeps
// the project it had before an add, join or link, under an id that project
// no longer has.
func (d *Daemon) reidentifyCatalogRows(ctx context.Context) {
	generation := d.catalogIdentities.Generation()
	if d.catalogReidentified.Load() == generation {
		return
	}
	snapshot, err := d.Registry.CatalogSnapshot()
	if err != nil {
		d.log.Printf("catalog rows to re-identify: %s", err)
		return
	}
	view, err := d.loadCatalogViewState()
	if err != nil {
		d.log.Printf("catalog view state: %s", err)
		return
	}
	settled := true
	for _, record := range snapshot.Sessions {
		if record.HostID != "" || record.ProjectionFingerprint == "" {
			continue
		}
		if !d.reidentifyCatalogRow(ctx, record, view) {
			settled = false
		}
	}
	if err := d.catalog.flushCommitted(); err != nil {
		d.log.Printf("publish committed catalog events: %s", err)
	}
	if settled {
		d.catalogReidentified.Store(generation)
	}
}

func (d *Daemon) reidentifyCatalogRow(ctx context.Context, record registry.CatalogSessionState, view catalogViewState) bool {
	identityCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	identity := d.catalogIdentities.Identity(identityCtx, record.Session, time.Now().UTC(), func() *projectresolve.CatalogProject { return d.storedCatalogProject(record.Session.ID) })
	cancel()
	if identity.Problem != "" && identity.Project == nil {
		return true
	}
	project, err := json.Marshal(identity.Project)
	if err != nil {
		return false
	}
	if bytes.Equal(project, record.Project) {
		return true
	}
	checkout, err := json.Marshal(identity.Checkout)
	if err != nil {
		return false
	}
	var row dashboardSession
	if json.Unmarshal([]byte(record.ProjectionFingerprint), &row) != nil {
		return true
	}
	row.Project, row.Checkout = identity.Project, &identity.Checkout
	applyCatalogView(&row, view)
	fingerprint, err := json.Marshal(row)
	if err != nil {
		return false
	}
	if _, _, err := d.Registry.ReidentifyCatalogSession(record.Session.ID, record.ProjectionFingerprint, string(fingerprint), project, checkout); err != nil {
		var conflict *registry.CatalogQueueProjectionConflict
		if !errors.As(err, &conflict) {
			d.log.Printf("catalog project of %s: %s", d.resolveDisplay(record.Session.ID), err)
		}
		return false
	}
	return true
}
