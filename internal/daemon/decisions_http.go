package daemon

import (
	"context"
	"net/http"
	"sort"
	"sync"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) registerDecisionsAPI(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/decisions", d.apiV1Guard(dashboard, "read", apiV1JSONHandler(d.decisionsHandler)))
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
	page := buildDecisions(decisionSources{rows: rows, hearer: d.decisionHearer, requests: read, queued: queued})
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
	for _, row := range build.rows {
		rows = append(rows, *row)
	}
	return rows, build.hostEpoch, nil
}

func (d *Daemon) queuedBehind() (map[string][]string, error) {
	queue, err := d.Registry.ListQueue(false, d.adapters())
	if err != nil {
		return nil, err
	}
	sources := map[string]map[string]bool{}
	for _, row := range queue {
		source := row.SourceSessionID
		if row.Status == "dead" || source == "" || source == row.SessionID || source == surface.SystemSessionID || source == surface.PersonSessionID || row.Author == surface.AuthorPerson {
			continue
		}
		target := d.publicSessionID(row.SessionID)
		if sources[target] == nil {
			sources[target] = map[string]bool{}
		}
		sources[target][d.publicSessionID(source)] = true
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

func (d *Daemon) decisionHearer(row dashboardSession) (decisionHearer, bool) {
	session, err := d.Registry.Session(row.ID)
	if err != nil || session == nil {
		return decisionHearer{}, false
	}
	to, ok := d.noticeRecipient(*session)
	if !ok {
		return decisionHearer{}, false
	}
	return decisionHearer{sessionID: d.publicSessionID(to.sessionID), role: to.role}, true
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
				answer.requests, _, answer.err = d.openRequests(ctx, session)
			}
			mu.Lock()
			read[id] = answer
			mu.Unlock()
		}(row.ID)
	}
	wait.Wait()
	return read
}
