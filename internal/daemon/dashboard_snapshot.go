package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

const (
	dashboardStateCacheTTL = 30 * time.Second
	dashboardRefreshBudget = 18 * time.Second
)

func (d *Daemon) dashboardStateCached(dashboard *dashboardServer, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	page, pageErr := catalogPageRequestFromHTTP(r)
	if pageErr != nil {
		code := "invalid_catalog_page"
		if strings.Contains(pageErr.Error(), "cursor") {
			code = "invalid_cursor"
		}
		writeAPIError(w, http.StatusBadRequest, code, pageErr.Error())
		return
	}
	cacheable := page == nil && !catalogMirror(r.Context())
	hostEpoch, catalogSeq, catalogErr := d.Registry.CatalogState()
	dashboard.stateMu.Lock()
	version := dashboard.stateVersion.Load()
	if cacheable && catalogErr == nil && r.URL.Query().Get("fresh") != "1" && dashboard.cachedVersion == version && dashboard.state.HostEpoch == hostEpoch && dashboard.state.CatalogSeq == catalogSeq && !dashboard.stateAt.IsZero() && time.Since(dashboard.stateAt) < dashboardStateCacheTTL {
		state, tag := dashboard.state, dashboardSnapshotTag(dashboard.state, dashboard.cachedVersion, dashboard.stateAt)
		dashboard.stateMu.Unlock()
		if r.URL.RawQuery == "" {
			w.Header().Set("ETag", tag)
			if r.Header.Get("If-None-Match") == tag {
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		d.writeDashboardSnapshot(w, r, state)
		return
	}
	previous, hadPrevious := dashboard.state, !dashboard.stateAt.IsZero()
	dashboard.stateMu.Unlock()
	refreshCtx, cancel := context.WithTimeout(r.Context(), dashboardRefreshBudget)
	defer cancel()
	var state dashboardState
	var err error
	if page == nil {
		state, err = d.dashboardState(refreshCtx)
	} else {
		state, err = d.dashboardState(refreshCtx, *page)
	}
	if err != nil {
		if cacheable && hadPrevious {
			stale := previous
			stale.Daemon = cloneDashboardDaemon(stale.Daemon)
			stale.Daemon["stale"] = true
			stale.Daemon["refreshError"] = err.Error()
			d.writeDashboardSnapshot(w, r, stale)
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if cacheable {
		dashboard.stateMu.Lock()
		dashboard.state = state
		dashboard.stateAt = time.Now()
		dashboard.cachedVersion = version
		tag := dashboardSnapshotTag(state, version, dashboard.stateAt)
		dashboard.stateMu.Unlock()
		if r.URL.RawQuery == "" {
			w.Header().Set("ETag", tag)
		}
	}
	d.writeDashboardSnapshot(w, r, state)
}

func dashboardSnapshotTag(state dashboardState, version uint64, builtAt time.Time) string {
	return fmt.Sprintf(`"%s-%d-%d-%d"`, state.HostEpoch, state.CatalogSeq, version, builtAt.UnixNano())
}

func (d *Daemon) writeDashboardSnapshot(w http.ResponseWriter, r *http.Request, state dashboardState) {
	if state.catalogPageApplied {
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			decoded, err := base64.RawURLEncoding.DecodeString(raw)
			var cursor catalogPageCursor
			if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Epoch != state.HostEpoch || cursor.Seq != state.CatalogSeq || cursor.Filter != state.catalogPageFilter || cursor.Offset != state.catalogPageOffset || !cursor.UpdatedAt.Equal(state.UpdatedAt) {
				writeAPIError(w, http.StatusConflict, "catalog_changed", "Reload the catalog before continuing.")
				return
			}
		}
		if state.catalogPageHasMore {
			encoded, _ := json.Marshal(catalogPageCursor{Epoch: state.HostEpoch, Seq: state.CatalogSeq, UpdatedAt: state.UpdatedAt, Offset: state.catalogPageOffset + state.catalogPageLimit, Filter: state.catalogPageFilter})
			state.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
		}
		writeDashboardJSON(w, http.StatusOK, state)
		return
	}
	scope := r.URL.Query().Get("scope")
	if !validCatalogScope(scope) {
		writeAPIError(w, http.StatusBadRequest, "invalid_scope", "Use running, current, recent, all, or archived.")
		return
	}
	projectID, query := r.URL.Query().Get("projectId"), strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	rows := make([]dashboardSession, 0, len(state.Sessions))
	for _, row := range state.Sessions {
		if scope != "" && !catalogScopeMatches(scope, row) {
			continue
		}
		if projectID != "" && (row.Project == nil || row.Project.ID != projectID) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(row.Name+" "+row.Cwd+" "+row.Alias), query) {
			continue
		}
		rows = append(rows, row)
	}
	state.Sessions = rows
	state.TotalSessions = len(rows)
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > 200 {
			writeAPIError(w, http.StatusBadRequest, "invalid_limit", "Use a limit between 1 and 200.")
			return
		}
		filter := scope + "\x00" + projectID + "\x00" + query
		offset := 0
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			decoded, err := base64.RawURLEncoding.DecodeString(raw)
			var cursor catalogPageCursor
			if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Offset < 0 || cursor.Filter != filter {
				writeAPIError(w, http.StatusBadRequest, "invalid_cursor", "The catalog page cursor is invalid.")
				return
			}
			if cursor.Epoch != state.HostEpoch || cursor.Seq != state.CatalogSeq {
				writeAPIError(w, http.StatusConflict, "catalog_changed", "Reload the catalog before continuing.")
				return
			}
			offset = cursor.Offset
		}
		if offset > len(rows) {
			writeAPIError(w, http.StatusBadRequest, "invalid_cursor", "The catalog page cursor is invalid.")
			return
		}
		end := min(len(rows), offset+limit)
		state.Sessions = rows[offset:end]
		if end < len(rows) {
			encoded, _ := json.Marshal(catalogPageCursor{Epoch: state.HostEpoch, Seq: state.CatalogSeq, UpdatedAt: state.UpdatedAt, Offset: end, Filter: filter})
			state.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
		}
	} else if r.URL.Query().Get("cursor") != "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_cursor", "A page cursor requires a limit.")
		return
	}
	writeDashboardJSON(w, http.StatusOK, state)
}

type catalogPageCursor struct {
	Epoch     string    `json:"epoch"`
	Seq       uint64    `json:"seq"`
	UpdatedAt time.Time `json:"updatedAt"`
	Offset    int       `json:"offset"`
	Filter    string    `json:"filter"`
}

func catalogPageRequestFromHTTP(r *http.Request) (*registry.CatalogPageRequest, error) {
	rawLimit := r.URL.Query().Get("limit")
	if rawLimit == "" {
		if r.URL.Query().Get("cursor") != "" {
			return nil, fmt.Errorf("a page cursor requires a limit")
		}
		return nil, nil
	}
	limit, err := strconv.Atoi(rawLimit)
	if err != nil || limit < 1 || limit > 200 {
		return nil, fmt.Errorf("use a limit between 1 and 200")
	}
	scope := r.URL.Query().Get("scope")
	if !validCatalogScope(scope) {
		return nil, fmt.Errorf("use running, current, recent, all, or archived")
	}
	projectID, query := r.URL.Query().Get("projectId"), strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	parentID := r.URL.Query().Get("parentId")
	filter := scope + "\x00" + projectID + "\x00" + query + "\x00" + parentID
	request := &registry.CatalogPageRequest{Scope: scope, ProjectID: projectID, ParentID: parentID, Query: query, Limit: limit}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		var cursor catalogPageCursor
		if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Offset < 0 || cursor.Filter != filter || cursor.UpdatedAt.IsZero() {
			return nil, fmt.Errorf("the catalog page cursor is invalid")
		}
		request.Offset = cursor.Offset
		request.Now = cursor.UpdatedAt
	} else {
		request.Now = time.Now().UTC()
	}
	return request, nil
}

func cloneDashboardDaemon(input map[string]any) map[string]any {
	output := make(map[string]any, len(input)+2)
	for key, value := range input {
		output[key] = value
	}
	return output
}
