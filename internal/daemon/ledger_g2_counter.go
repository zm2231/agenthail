package daemon

import (
	"net/http"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/daemonapi"
)

const g2CountEventInterval = time.Hour

type g2Counter struct {
	mu        sync.Mutex
	since     time.Time
	published time.Time
	projects  map[string]*daemonapi.G2Count
}

func newG2Counter(since time.Time) *g2Counter {
	return &g2Counter{since: since, published: since, projects: map[string]*daemonapi.G2Count{}}
}

func (c *g2Counter) record(projectKey string, gated bool, now time.Time) (snapshot []daemonapi.G2Count, publish bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := c.projects[projectKey]
	if count == nil {
		count = &daemonapi.G2Count{ProjectKey: projectKey, Since: c.since}
		c.projects[projectKey] = count
	}
	count.Checked++
	if gated {
		count.Gated++
	}
	if now.Sub(c.published) < g2CountEventInterval {
		return nil, false
	}
	c.published = now
	for _, p := range c.projects {
		snapshot = append(snapshot, *p)
	}
	return snapshot, true
}

func (c *g2Counter) project(projectKey string) daemonapi.G2Count {
	c.mu.Lock()
	defer c.mu.Unlock()
	if count := c.projects[projectKey]; count != nil {
		return *count
	}
	return daemonapi.G2Count{ProjectKey: projectKey, Since: c.since}
}

func (d *Daemon) countG2Check(projectKey string, gated bool) {
	if snapshot, publish := d.g2.record(projectKey, gated, time.Now()); publish {
		d.publishEvent("ledger.g2.checked", "", snapshot)
	}
}

func (d *Daemon) apiLedgerG2Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	key := r.URL.Query().Get("projectKey")
	if key == "" {
		writeAPIError(w, http.StatusBadRequest, "project_required", "Pass projectKey.")
		return
	}
	writeDashboardJSON(w, http.StatusOK, d.g2.project(key))
}
