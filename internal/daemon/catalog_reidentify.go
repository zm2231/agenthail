package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	catalogReidentifyWorkers = 4
	catalogReidentifyBudget  = 30 * time.Second
)

// requestCatalogReidentify gives every local row the project its folder
// belongs to now, once per change to the added projects or links. A pass
// re-resolves only the sessions its surface lists, so without this an ended
// session keeps the project it had before an add, join or link, under an id
// that project no longer has. The sweep runs beside the passes; one cut
// short by its budget or a concurrent write is retried after the next pass.
func (d *Daemon) requestCatalogReidentify() {
	generation := d.catalogIdentities.Generation()
	if d.catalogReidentified.Load() == generation || !d.catalogReidentifying.CompareAndSwap(false, true) {
		return
	}
	if !d.background.start(func(ctx context.Context) {
		defer d.catalogReidentifying.Store(false)
		if d.reidentifyCatalogRows(ctx) {
			d.catalogReidentified.Store(generation)
		}
	}) {
		d.catalogReidentifying.Store(false)
	}
}

func (d *Daemon) reidentifyCatalogRows(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, catalogReidentifyBudget)
	defer cancel()
	snapshot, err := d.Registry.CatalogSnapshot()
	if err != nil {
		d.log.Printf("catalog rows to re-identify: %s", err)
		return false
	}
	view, err := d.loadCatalogViewState()
	if err != nil {
		d.log.Printf("catalog view state: %s", err)
		return false
	}
	byFolder := map[string][]registry.CatalogSessionState{}
	for _, record := range snapshot.Sessions {
		if record.HostID == "" && record.ProjectionFingerprint != "" {
			byFolder[record.Session.Cwd] = append(byFolder[record.Session.Cwd], record)
		}
	}
	folders := make(chan []registry.CatalogSessionState)
	var settled atomic.Bool
	settled.Store(true)
	var workers sync.WaitGroup
	for range catalogReidentifyWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for records := range folders {
				if !d.reidentifyFolder(ctx, records, view) {
					settled.Store(false)
				}
			}
		}()
	}
send:
	for _, records := range byFolder {
		select {
		case folders <- records:
		case <-ctx.Done():
			settled.Store(false)
			break send
		}
	}
	close(folders)
	workers.Wait()
	if err := d.catalog.flushCommitted(); err != nil {
		d.log.Printf("publish committed catalog events: %s", err)
	}
	return settled.Load() && ctx.Err() == nil
}

func (d *Daemon) reidentifyFolder(ctx context.Context, records []registry.CatalogSessionState, view catalogViewState) bool {
	first := records[0]
	identityCtx, cancel := context.WithTimeout(ctx, projectresolve.CatalogIdentityGitTimeout)
	identity := d.catalogIdentities.Identity(identityCtx, first.Session, time.Now().UTC(), func() *projectresolve.CatalogProject { return d.storedCatalogProject(first.Session.ID) })
	cancel()
	if ctx.Err() != nil {
		return false
	}
	if !replacesStoredProject(identity, first.Session.Cwd) {
		return true
	}
	project, err := json.Marshal(identity.Project)
	if err != nil {
		return false
	}
	checkout, err := json.Marshal(identity.Checkout)
	if err != nil {
		return false
	}
	settled := true
	for _, record := range records {
		if !d.reidentifyCatalogRow(record, identity, project, checkout, view) {
			settled = false
		}
	}
	return settled
}

// replacesStoredProject reports whether a resolved identity may replace the
// project a row holds. A folder that is gone resolves through whatever
// surrounds it, so only an added project, which holds the path itself, is
// evidence of where its session belongs.
func replacesStoredProject(identity projectresolve.CatalogIdentity, cwd string) bool {
	if identity.Problem != "" && identity.Project == nil {
		return false
	}
	if cwd == "" {
		return false
	}
	if _, err := os.Stat(cwd); err != nil {
		return identity.Project != nil && identity.Project.Added
	}
	return true
}

func (d *Daemon) reidentifyCatalogRow(record registry.CatalogSessionState, identity projectresolve.CatalogIdentity, project, checkout json.RawMessage, view catalogViewState) bool {
	if bytes.Equal(record.Project, project) {
		return true
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
	written, err := d.catalog.reidentify(record.Session.ID, record.ProjectionFingerprint, string(fingerprint), project, checkout)
	if err != nil {
		var conflict *registry.CatalogQueueProjectionConflict
		if !errors.As(err, &conflict) {
			d.log.Printf("catalog project of %s: %s", d.resolveDisplay(record.Session.ID), err)
		}
		return false
	}
	return written
}
