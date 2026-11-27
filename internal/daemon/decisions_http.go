package daemon

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"sort"
	"sync"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
)

func (d *Daemon) registerDecisionsAPI(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/decisions", d.apiV1Guard(dashboard, "read", apiV1JSONHandler(d.hostRouted("", d.decisionsHandler))))
}

func (d *Daemon) decisionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for decisions.")
		return
	}
	rows, epoch, err := d.decisionRows(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	queued, err := d.queuedBehind()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	read := func(asking []dashboardSession) map[string]decisionRequests {
		return d.readDecisionRequests(r.Context(), asking)
	}
	page := buildDecisions(decisionSources{rows: rows, hearer: d.decisionHearer, requests: read, queued: queued, parked: d.parkedDecisionsNow, dismissed: d.decisionDismissals(), lead: d.decisionLead(), workTitle: d.decisionWorkTitle, reviews: d.reviewEntries})
	tag, err := contentTag(epoch, page)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeTagged(w, r, tag, page)
}

func (d *Daemon) decisionRows(ctx context.Context) ([]dashboardSession, string, error) {
	d.presentation.Lock()
	defer d.presentation.Unlock()
	if d.presentation.epoch == "" {
		d.presentation.epoch = uuid.NewString()
	}
	build, err := d.sessionViewBuildFor(ctx, sessionViewRequest{Scope: "current"}, d.presentation.now())
	if err != nil {
		return nil, "", err
	}
	rows := make([]dashboardSession, 0, len(build.rows))
	for _, id := range slices.Sorted(maps.Keys(build.rows)) {
		rows = append(rows, *build.rows[id])
	}
	return rows, build.hostEpoch, nil
}

func (d *Daemon) queuedBehind() (map[string][]string, error) {
	senders, err := d.Registry.QueuedSenders()
	if err != nil {
		return nil, err
	}
	sources := map[string]map[string]bool{}
	for _, sender := range senders {
		target := d.publicSessionID(sender.SessionID)
		if sources[target] == nil {
			sources[target] = map[string]bool{}
		}
		sources[target][d.publicSessionID(sender.SourceSessionID)] = true
	}
	queued := make(map[string][]string, len(sources))
	for target, from := range sources {
		for id := range from {
			queued[target] = append(queued[target], id)
		}
		sort.Strings(queued[target])
	}
	return queued, nil
}

func (d *Daemon) decisionLead() func(dashboardSession) (string, bool) {
	leads, err := d.loadLeadDirectory()
	if err != nil {
		d.log.Printf("decisions: read the leads: %s", err)
		return nil
	}
	return func(row dashboardSession) (string, bool) {
		lead, ok := leads.leadOf(row.Project)
		return d.publicSessionID(lead.sessionID), ok
	}
}

func (d *Daemon) decisionWorkTitle(ledgerKey, itemID string) string {
	board, err := d.ledgerStore().Board(ledgerKey)
	if err != nil {
		return ""
	}
	item, _ := board.WorkItem(itemID)
	return item.Text
}

// decisionHearer is the agent a session's oldest permission request is routed to, while one is.
func (d *Daemon) decisionHearer(row dashboardSession) (decisionHearer, bool) {
	open, err := d.Registry.OpenPermissionAsks(row.ID)
	if err != nil || len(open) == 0 || open[0].RoutedTo == registry.AskToPerson {
		return decisionHearer{}, false
	}
	to, role := open[0].RoutedTo, "lead"
	if session, err := d.Registry.Session(row.ID); err == nil && session != nil {
		if hearer, ok := d.noticeRecipient(*session); ok && hearer.sessionID == to {
			role = hearer.role
		}
	}
	return decisionHearer{sessionID: d.publicSessionID(to), role: role}, true
}

func (d *Daemon) readDecisionRequests(ctx context.Context, rows []dashboardSession) map[string]decisionRequests {
	var mu sync.Mutex
	var wait sync.WaitGroup
	read := make(map[string]decisionRequests, len(rows))
	for _, row := range rows {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			answer := decisionRequests{}
			if session, err := d.Registry.Session(id); err != nil {
				answer.err = err
			} else {
				var open openRequests
				open, answer.err = d.openRequests(ctx, session)
				answer.requests = open.requests
			}
			mu.Lock()
			read[id] = answer
			mu.Unlock()
		}(row.ID)
	}
	wait.Wait()
	return read
}
