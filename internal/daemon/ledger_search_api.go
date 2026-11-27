package daemon

import (
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/projectresolve"
)

type ledgerSearchItem struct {
	ledger.WorkHit
	ProjectName   string `json:"projectName"`
	Host          string `json:"host,omitempty"`
	AssigneeAlias string `json:"assigneeAlias,omitempty"`
}

type ledgerSearchResponse struct {
	Items     []ledgerSearchItem `json:"items"`
	Truncated bool               `json:"truncated"`
}

func (d *Daemon) apiLedgerSearchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	query := r.URL.Query()
	req := ledger.WorkSearch{Text: query.Get("q")}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "limit is a number of items.")
			return
		}
		req.Limit = n
	}
	for _, state := range commaSeparated(query.Get("state")) {
		req.States = append(req.States, ledger.WorkState(state))
	}
	if project := strings.TrimSpace(query.Get("project")); project != "" {
		key, err := d.ledgerKeyNamed(project)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "projects_unavailable", err.Error())
			return
		}
		req.Projects = []string{key}
	}
	store := d.ledgerStore()
	result, err := store.SearchWork(req)
	var refusal *ledger.Refusal
	switch {
	case errors.As(err, &refusal):
		writeAPIError(w, http.StatusBadRequest, "invalid_request", refusal.Reason)
		return
	case err != nil:
		writeLedgerError(w, err)
		return
	}
	keys, err := d.projectLedgerKeys()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "projects_unavailable", err.Error())
		return
	}
	names, err := keys.names()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "projects_unavailable", err.Error())
		return
	}
	owners, aliases := map[string]string{}, map[string]string{}
	response := ledgerSearchResponse{Items: make([]ledgerSearchItem, len(result.Items)), Truncated: result.Truncated}
	for i, hit := range result.Items {
		owner, seen := owners[hit.ProjectKey]
		if !seen {
			if owner, err = store.RemoteOwner(hit.ProjectKey); err != nil {
				writeLedgerError(w, err)
				return
			}
			owners[hit.ProjectKey] = owner
		}
		name := names[hit.ProjectKey]
		if name == "" {
			name = hit.ProjectKey
		}
		alias, seen := aliases[hit.Assignee]
		if !seen && hit.Assignee != "" {
			alias = d.sessionAlias(hit.Assignee)
			aliases[hit.Assignee] = alias
		}
		response.Items[i] = ledgerSearchItem{WorkHit: hit, ProjectName: name, Host: owner, AssigneeAlias: alias}
	}
	writeLedgerRead(w, r, response)
}

func commaSeparated(v string) []string {
	var out []string
	for part := range strings.SplitSeq(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (p *projectLedgerKeys) names() (map[string]string, error) {
	names := map[string]string{}
	add := func(id, name string) error {
		if name == "" {
			return nil
		}
		key, _, err := p.of(id)
		if err == nil && key != "" && names[key] == "" {
			names[key] = name
		}
		return err
	}
	listed := func(seen map[string]*projectresolve.CatalogProject) error {
		for _, id := range slices.Sorted(maps.Keys(seen)) {
			if err := add(id, seen[id].DisplayName); err != nil {
				return err
			}
		}
		return nil
	}
	if err := listed(p.local); err != nil {
		return nil, err
	}
	for _, record := range p.keys.Records() {
		if record.Added {
			if err := add(record.ID, record.Title); err != nil {
				return nil, err
			}
		}
	}
	if err := listed(p.remote); err != nil {
		return nil, err
	}
	return names, nil
}
