package daemon

import (
	"context"
	"sync"
	"time"
)

// ledgerBoardWatch follows the catalog once for every open Work page and reads
// each watched project's board at most once per wait, a second unless a test
// sets it, however many pages show it.
type ledgerBoardWatch struct {
	mu     sync.Mutex
	wait   time.Duration
	read   func(projectID string) string
	pages  map[string]map[uint64]chan string
	shown  map[string]string
	nextID uint64
	stop   context.CancelFunc
}

func (d *Daemon) watchLedgerBoard(projectID string) (<-chan string, func()) {
	w := &d.ledgerBoards
	w.mu.Lock()
	if w.read == nil {
		w.read = d.ledgerBoardVersion
	}
	if w.wait == 0 {
		w.wait = time.Second
	}
	read := w.read
	_, watched := w.shown[projectID]
	w.mu.Unlock()
	var initial string
	if !watched {
		initial = read(projectID)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pages == nil {
		w.pages, w.shown = map[string]map[uint64]chan string{}, map[string]string{}
	}
	if w.pages[projectID] == nil {
		w.pages[projectID] = map[uint64]chan string{}
		w.shown[projectID] = initial
	}
	w.nextID++
	id, changed := w.nextID, make(chan string, 1)
	w.pages[projectID][id] = changed
	if w.stop == nil {
		ctx, stop := context.WithCancel(context.Background())
		w.stop = stop
		go d.followLedgerBoards(ctx, w.wait)
	}
	return changed, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.pages[projectID], id)
		if len(w.pages[projectID]) == 0 {
			delete(w.pages, projectID)
			delete(w.shown, projectID)
		}
		if len(w.pages) == 0 && w.stop != nil {
			w.stop()
			w.stop = nil
		}
	}
}

func (d *Daemon) followLedgerBoards(ctx context.Context, wait time.Duration) {
	w := &d.ledgerBoards
	var follower *catalogFollower
	for follower == nil {
		_, latest, err := d.Registry.CatalogState()
		if err == nil {
			_, follower, err = d.catalog.subscribe(latest)
		}
		if err != nil {
			d.logRuntimeError("ledger-page:catalog", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(catalogDiscoveryInterval):
			}
		}
	}
	defer follower.Cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case <-follower.Wake:
		}
		w.mu.Lock()
		read, projects := w.read, make([]string, 0, len(w.pages))
		for projectID := range w.pages {
			projects = append(projects, projectID)
		}
		w.mu.Unlock()
		for _, projectID := range projects {
			if ctx.Err() != nil {
				return
			}
			version := read(projectID)
			w.mu.Lock()
			if shown, watched := w.shown[projectID]; watched && shown != version {
				w.shown[projectID] = version
				for _, changed := range w.pages[projectID] {
					select {
					case <-changed:
					default:
					}
					changed <- version
				}
			}
			w.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
