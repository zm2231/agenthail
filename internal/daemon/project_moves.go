package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/registry"
)

type repositoryKeyWatch struct {
	mu      sync.Mutex
	seen    map[string]string
	pending atomic.Bool
}

func (d *Daemon) observeRepositoryKey(root, key string) {
	w := &d.repositoryKeys
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen[root] == key {
		return
	}
	outcome, err := d.Registry.ObserveRepositoryKey(root, key)
	if err != nil {
		d.log.Printf("project %s remote: %s", root, err)
		return
	}
	if w.seen == nil {
		w.seen = map[string]string{}
	}
	if !outcome.Waiting {
		w.seen[root] = key
	}
	d.projectMovesApplied([]registry.ProjectMoveOutcome{outcome})
}

const waitingProjectMovesTick = time.Minute

func (d *Daemon) followMoveIns(ctx context.Context) {
	ticker := time.NewTicker(waitingProjectMovesTick)
	defer ticker.Stop()
	var wake <-chan struct{}
	if path, err := d.Registry.ChangeSignal(registry.ChangeMoveIns); err != nil {
		d.log.Printf("follow moves arriving here, releasing the project moves they hold on a timer: %s", err)
	} else {
		follow := filewake.Follow(path)
		defer follow.Release()
		wake = follow.C()
	}
	for {
		d.releaseWaitingProjectMoves()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-wake:
		}
	}
}

func (d *Daemon) releaseWaitingProjectMoves() {
	outcomes, err := d.Registry.ReleaseWaitingProjectMoves()
	if err != nil {
		d.log.Printf("project moves held for a move arriving here: %s", err)
		return
	}
	if len(outcomes) == 0 {
		return
	}
	d.projectMovesApplied(outcomes)
	d.publishProjectMoves()
}

func (d *Daemon) adoptProjectMoves(moves []registry.ProjectMove) ([]registry.ProjectMoveOutcome, error) {
	outcomes, err := d.Registry.AdoptProjectMoves(moves)
	if err != nil {
		return nil, err
	}
	d.projectMovesApplied(outcomes)
	d.publishProjectMoves()
	return outcomes, nil
}

func (d *Daemon) projectMovesApplied(outcomes []registry.ProjectMoveOutcome) {
	for _, outcome := range outcomes {
		switch {
		case outcome.Moved:
			d.ledgerStore().Forget(outcome.Move.From, outcome.Move.To)
			if err := d.refreshProjectLinks(); err != nil {
				d.log.Printf("project links: %s", err)
			}
			if outcome.Move.To != "" {
				d.ledgerChanged(outcome.Move.To)
			}
			d.repositoryKeys.pending.Store(true)
			d.requestCatalogDiscovery()
		case outcome.NewRefusal:
			d.log.Printf("project %s did not move to %s: %s", outcome.Move.From, outcome.Move.To, outcome.Refused.Reason)
		}
	}
}

func (d *Daemon) publishProjectMoves() {
	if d.repositoryKeys.pending.Swap(false) {
		d.projectLinksChanged()
	}
}

func (d *Daemon) sharedProjectMoves() ([]registry.ProjectMove, error) {
	moves, err := d.Registry.ProjectMoves()
	if err != nil {
		return nil, err
	}
	shared := make([]registry.ProjectMove, 0, len(moves))
	for _, move := range moves {
		if move.Shared() {
			shared = append(shared, move)
		}
	}
	return shared, nil
}
