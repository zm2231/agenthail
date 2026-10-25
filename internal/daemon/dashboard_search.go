package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) dashboardSearchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if r.URL.Query().Get("surface") != "codex" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "only Codex history search is available")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(query)) < 3 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "search requires at least 3 characters")
		return
	}
	adapter := d.adapters().ForSurface(surface.KindCodex)
	searcher, ok := adapter.(surface.SessionSearcher)
	if !ok {
		writeAPIError(w, http.StatusNotImplemented, "request_failed", "Codex history search is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	stored, err := d.Registry.SearchSessions(surface.KindCodex, query, 20)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("search saved Codex conversations: %s", err))
		return
	}
	results := make([]surface.SessionSearchResult, 0, len(stored)+20)
	for _, session := range stored {
		results = append(results, surface.SessionSearchResult{Session: session, Snippet: "Saved by Agenthail"})
	}
	remote, remoteErr := searcher.SearchSessions(ctx, query, 20)
	remoteError := ""
	if remoteErr != nil {
		if errors.Is(remoteErr, surface.ErrUnsupported) {
			remoteError = "Codex history search is unavailable in this Codex version"
		} else {
			remoteError = remoteErr.Error()
		}
	} else {
		results = mergeDashboardSearchResults(results, remote)
	}
	payload := make([]map[string]any, 0, len(results))
	for _, result := range results {
		if err := d.Registry.RegisterSession(result.Session); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("store search result: %s", err))
			return
		}
		alias, _ := d.Registry.ReverseAlias(result.Session.ID)
		effective := surface.EffectiveCapabilities(adapter, &result.Session)
		row := dashboardSession{ID: result.Session.ID, Surface: result.Session.Surface, Name: result.Session.Name, Cwd: result.Session.Cwd, Alias: alias, Status: result.Session.Status, WaitingOn: result.Session.WaitingOn, LastActive: result.Session.LastActive, Capabilities: effective.Capabilities, ReadOnly: effective.ReadOnly, ReadOnlyReason: effective.ReadOnlyReason, Source: result.Session.Source, Transport: result.Session.Transport, Subagent: result.Session.Subagent, Delegated: result.Session.Delegated()}
		row.RuntimeKind, row.BackgroundTasks, row.UsageLimit, row.Activity = result.Session.RuntimeKind, result.Session.BackgroundTasks, result.Session.UsageLimit, result.Session.Activity
		presentation, err := d.readSessionView(ctx, sessionViewRequest{Scope: "all", IDs: []string{row.ID}, Detail: &row})
		if err != nil {
			writeAPIError(w, 500, "internal_error", err.Error())
			return
		}
		payload = append(payload, map[string]any{"session": presentation.Sessions[0], "snippet": result.Snippet, "presentationPosition": presentation.PresentationPosition})
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"results": payload, "remoteError": remoteError})
}

func mergeDashboardSearchResults(left, right []surface.SessionSearchResult) []surface.SessionSearchResult {
	byID := make(map[string]surface.SessionSearchResult, len(left)+len(right))
	for _, result := range left {
		byID[result.Session.ID] = result
	}
	for _, result := range right {
		byID[result.Session.ID] = result
	}
	output := make([]surface.SessionSearchResult, 0, len(byID))
	for _, result := range byID {
		output = append(output, result)
	}
	sort.Slice(output, func(i, j int) bool { return output[i].Session.LastActive.After(output[j].Session.LastActive) })
	return output
}
