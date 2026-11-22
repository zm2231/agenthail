package daemon

import (
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

type heldWork struct {
	key       string
	projectID string
	item      ledger.WorkItem
}

func (d *Daemon) heldWork(session surface.Session) ([]heldWork, error) {
	store := d.ledgerStore()
	targets, _, err := d.ledgerTargets(store, session)
	if err != nil {
		return nil, err
	}
	var held []heldWork
	for _, target := range targets {
		board, err := store.Board(target.key)
		if err != nil {
			return nil, err
		}
		for _, item := range board.HeldBy(session.ID) {
			held = append(held, heldWork{key: target.key, projectID: target.projectID, item: item})
		}
	}
	return held, nil
}
