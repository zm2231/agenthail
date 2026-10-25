package daemon

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) dashboardModelsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	adapter := d.adapters().ForSurface(surface.SurfaceKind(r.URL.Query().Get("surface")))
	lister, ok := adapter.(surface.ModelLister)
	if adapter == nil || !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "this surface does not list models")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	models, err := lister.Models(ctx)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "bad_gateway", fmt.Sprintf("list models: %s", err))
		return
	}
	if models == nil {
		models = []surface.ModelOption{}
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (d *Daemon) dashboardSessionMetadataHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	session, err := d.Registry.Session(r.URL.Query().Get("id"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	adapter, adapterErr := d.adapters().For(session)
	if adapterErr != nil {
		writeAPIError(w, http.StatusConflict, "conflict", "surface is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	effective := surface.EffectiveCapabilities(adapter, session)
	requests := map[string]func() (any, error){}
	if provider, ok := adapter.(surface.ContextUsageProvider); ok {
		requests["context"] = func() (any, error) { return provider.ContextUsage(ctx, session) }
	}
	if effective.GoalStatus {
		requests["goal"] = func() (any, error) { return adapter.GoalGet(ctx, session) }
	}
	if effective.Model {
		requests["model"] = func() (any, error) { return adapter.Model(ctx, session, "") }
		if reader, ok := adapter.(surface.PendingModelReader); ok {
			requests["pendingModel"] = func() (any, error) { return reader.PendingModel(session.ID), nil }
		}
		if lister, ok := adapter.(surface.ModelLister); ok {
			requests["models"] = func() (any, error) { return lister.Models(ctx) }
		}
	}
	if observer, ok := adapter.(surface.ClaudeRunObserver); ok {
		requests["claudeRuns"] = func() (any, error) {
			runs, err := observer.ObserveClaudeRuns(ctx)
			filtered := []surface.ClaudeRunObservation{}
			for _, run := range runs {
				if run.SessionID == session.ID || run.ResumeSessionID == session.ID {
					filtered = append(filtered, run)
				}
			}
			return filtered, err
		}
		allSubagents := r.URL.Query().Get("subagents") == "all"
		requests["claudeSubagents"] = func() (any, error) {
			links, err := observer.ObserveClaudeSubagents(ctx, session)
			if err != nil {
				return nil, err
			}
			return newClaudeSubagentPage(links, allSubagents), nil
		}
	}
	response := map[string]any{"sessionId": session.ID}
	type metadataResult struct {
		field string
		value any
		err   error
	}
	results := make(chan metadataResult, len(requests))
	for field, read := range requests {
		go func() { value, err := read(); results <- metadataResult{field: field, value: value, err: err} }()
	}
	errorsByField := map[string]string{}
	for len(requests) > 0 {
		select {
		case result := <-results:
			delete(requests, result.field)
			if result.err != nil {
				errorsByField[result.field] = "Metadata unavailable."
			} else if page, ok := result.value.(claudeSubagentPage); ok {
				links, working := displayClaudeSubagents(page)
				response[result.field] = links
				response["claudeSubagentsWorking"] = working
				response["claudeSubagentsRemaining"] = page.total - len(page.links)
				response["claudeSubagentsTotal"] = page.total
			} else {
				response[result.field] = result.value
			}
		case <-ctx.Done():
			for field := range requests {
				errorsByField[field] = "Metadata unavailable."
			}
			requests = nil
		}
	}
	if len(errorsByField) > 0 {
		response["errors"] = errorsByField
	}
	writeDashboardJSON(w, http.StatusOK, response)
}
