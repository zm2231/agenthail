package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/apitime"
	"github.com/zm2231/agenthail/internal/registry"
)

const decisionsSettle = 250 * time.Millisecond

// Peers mirror own, never rev, so one host's change cannot echo between paired hosts.
type decisionsRevision struct {
	mu    sync.Mutex
	own   string
	rev   string
	peers map[string]string
	wake  chan struct{}
}

func newDecisionsRevision() *decisionsRevision {
	return &decisionsRevision{peers: map[string]string{}, wake: make(chan struct{}, 1)}
}

func (r *decisionsRevision) setPeer(hostID, own string) {
	r.mu.Lock()
	changed := r.peers[hostID] != own
	if own == "" {
		delete(r.peers, hostID)
	} else {
		r.peers[hostID] = own
	}
	r.mu.Unlock()
	if changed {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
}

func (r *decisionsRevision) poke() {
	if r == nil {
		return
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *decisionsRevision) current() (own, rev string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.own, r.rev
}

func (d *Daemon) runDecisionsRevision(ctx context.Context) {
	_, latest, err := d.Registry.CatalogState()
	if err != nil {
		d.log.Printf("decisions revision: %s", err)
		return
	}
	_, follower, err := d.catalog.subscribe(latest)
	if err != nil {
		d.log.Printf("decisions revision: %s", err)
		return
	}
	defer follower.Cancel()
	d.refreshDecisionsRevision(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case _, open := <-follower.Wake:
			if !open {
				return
			}
		case <-d.decisions.wake:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(decisionsSettle):
		}
		d.refreshDecisionsRevision(ctx)
	}
}

func (d *Daemon) refreshDecisionsRevision(ctx context.Context) {
	r := d.decisions
	own, err := d.ownDecisionsRevision(ctx)
	if err != nil {
		d.log.Printf("decisions revision: %s", err)
		return
	}
	r.mu.Lock()
	rev, err := combinedDecisionsRevision(own, r.peers)
	if err != nil || own == r.own && rev == r.rev {
		r.mu.Unlock()
		return
	}
	r.own, r.rev = own, rev
	r.mu.Unlock()
	payload, err := json.Marshal(map[string]string{"rev": rev, "own": own})
	if err != nil {
		return
	}
	now := time.Now().UTC()
	if _, _, err := d.catalog.publish(registry.CatalogEvent{DedupeKey: "decisions.changed:" + rev + ":" + strconv.FormatInt(now.UnixNano(), 10), Type: "decisions.changed", Payload: payload, CreatedAt: now}); err != nil {
		d.log.Printf("decisions revision: %s", err)
	}
}

// Asking sessions stand in their waiting id and open request ids for the surface read, which is what made each fetch costly.
func (d *Daemon) ownDecisionsRevision(ctx context.Context) (string, error) {
	rows, _, err := d.decisionRows(ctx)
	if err != nil {
		return "", err
	}
	queued, err := d.queuedBehind()
	if err != nil {
		return "", err
	}
	waiting := map[string][]string{}
	page := buildDecisions(decisionSources{rows: rows, hearer: d.decisionHearer, queued: queued, parked: d.parkedDecisionsNow, requests: func(asking []dashboardSession) map[string]decisionRequests {
		read := make(map[string]decisionRequests, len(asking))
		for _, row := range asking {
			read[row.ID] = decisionRequests{requests: []openRequestView{{}}}
			if row.WaitingOn != nil {
				waiting[row.ID] = append([]string{row.WaitingOn.ID}, row.WaitingOn.Open...)
			}
		}
		return read
	}})
	return revisionOf(struct {
		Page    decisionsPage       `json:"page"`
		Waiting map[string][]string `json:"waiting"`
	}{page, waiting})
}

func combinedDecisionsRevision(own string, peers map[string]string) (string, error) {
	if len(peers) == 0 {
		return own, nil
	}
	ids := make([]string, 0, len(peers))
	for id := range peers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := []string{own}
	for _, id := range ids {
		parts = append(parts, id+"="+peers[id])
	}
	return revisionOf(parts)
}

func revisionOf(content any) (string, error) {
	encoded, err := apitime.Marshal(content)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:12]), nil
}
