package daemon

import (
	"context"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// A Claude folder the host has not listed takes seconds, and the host gives
// up on a listing after 15.
const sessionCommandsWait = 20 * time.Second

func (d *Daemon) sessionCommandsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET.")
		return
	}
	session, err := d.Registry.Session(r.URL.Query().Get("id"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "Session not found.")
		return
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "surface_unavailable", "This session's surface is not configured.")
		return
	}
	if effective := surface.EffectiveCapabilities(adapter, session); effective.ReadOnly {
		writeDashboardJSON(w, http.StatusOK, surface.RuntimeCommands{Commands: []surface.RuntimeCommand{}, Unavailable: effective.ReadOnlyReason})
		return
	}
	provider, ok := adapter.(surface.CommandsProvider)
	if !ok {
		writeDashboardJSON(w, http.StatusOK, surface.RuntimeCommands{Commands: []surface.RuntimeCommand{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), sessionCommandsWait)
	defer cancel()
	commands, err := provider.Commands(ctx, session)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "commands_unavailable", "The session's runtime did not list its commands: "+err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, commands)
}
