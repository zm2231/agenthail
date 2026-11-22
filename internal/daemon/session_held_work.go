package daemon

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

const heldWorkLogLimit = 280

type heldWorkRow struct {
	ItemID     string       `json:"itemId"`
	Text       string       `json:"text"`
	State      string       `json:"state"`
	ProjectKey string       `json:"projectKey"`
	Log        *heldWorkLog `json:"log,omitempty"`
}

type heldWorkLog struct {
	Type string    `json:"type"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

type heldWorkCache struct {
	mu      sync.Mutex
	built   bool
	changes uint64
	held    map[string][]heldWorkRow
}

func (d *Daemon) heldWorkBySession() (map[string][]heldWorkRow, error) {
	store := d.ledgerStore()
	changes := store.Generation()
	c := &d.heldWorkRows
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.built && c.changes == changes {
		return c.held, nil
	}
	held, err := readHeldWork(store)
	if err != nil {
		return nil, err
	}
	c.built, c.changes, c.held = true, changes, held
	return held, nil
}

func readHeldWork(store *ledger.Store) (map[string][]heldWorkRow, error) {
	keys, err := store.ProjectsWith(ledger.EventWorkItem)
	if err != nil {
		return nil, err
	}
	held := map[string][]heldWorkRow{}
	for _, key := range keys {
		board, err := store.Board(key)
		if err != nil {
			return nil, err
		}
		for _, item := range board.WorkItems() {
			if !item.Held() {
				continue
			}
			row := heldWorkRow{ItemID: item.ID, Text: item.Text, State: string(item.State), ProjectKey: key}
			if entry := item.LatestLog(); entry != nil {
				row.Log = &heldWorkLog{Type: entry.LogType, Text: clip(entry.Text, heldWorkLogLimit), At: entry.At}
			}
			held[item.Assignee] = append(held[item.Assignee], row)
		}
	}
	for _, rows := range held {
		slices.SortFunc(rows, func(a, b heldWorkRow) int { return strings.Compare(a.ItemID, b.ItemID) })
	}
	return held, nil
}

// Only work events move the presentation version among ledger writes, so a captured prompt does not start a catalog pass.
func (d *Daemon) republishHeldWork() {
	key, err := d.Registry.ReadCatalogPresentationKey(context.Background())
	if err == nil && d.heldWorkVersion.Swap(key.Version) != key.Version {
		d.requestCatalogDiscovery()
	}
}
