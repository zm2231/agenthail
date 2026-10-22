package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) dashboardSessionOperation(w http.ResponseWriter, r *http.Request, adapter surface.Surface, session *surface.Session, action string, forkOptions surface.ForkOptions, forkName, creator string, queue surface.NativeQueueRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	var result any
	var err error
	switch action {
	case "session-fork":
		forker, ok := adapter.(surface.SessionForker)
		if !ok {
			err = surface.ErrUnsupported
			break
		}
		if creator != "" {
			if _, lookupErr := d.Registry.Session(creator); lookupErr != nil {
				http.Error(w, fmt.Sprintf("sourceSessionId %s is not a session here", creator), http.StatusBadRequest)
				return
			}
		}
		name, reserved := d.reserveStartName(w, forkName)
		if !reserved {
			return
		}
		defer func() { _ = d.Registry.ReleaseAliasReservation(name) }()
		var fork *surface.Session
		fork, err = forker.ForkSession(ctx, session, forkOptions)
		if err == nil && fork == nil {
			err = fmt.Errorf("fork returned no session")
		}
		if err == nil {
			err = d.Registry.RegisterSession(*fork)
			result = map[string]any{"session": fork}
		}
		if err == nil {
			if bindErr := d.Registry.BindReservedAlias(name, fork.ID); bindErr != nil {
				err = fmt.Errorf("fork %s was created, but its name could not be persisted: %w", fork.ID, bindErr)
			}
		}
		if err == nil {
			if originErr := d.Registry.RecordSessionOrigin(registry.OriginOf(fork.ID, creator)); originErr != nil {
				err = fmt.Errorf("fork %s was created, but who started it could not be recorded: %w", fork.ID, originErr)
			}
		}
	case "native-queue":
		native, ok := adapter.(surface.NativeQueuer)
		if !ok {
			err = surface.ErrUnsupported
			break
		}
		result, err = native.NativeQueue(ctx, session, queue)
	default:
		lifecycle, ok := adapter.(surface.SessionLifecycle)
		if !ok {
			err = surface.ErrUnsupported
			break
		}
		addressed := *session
		result, err = lifecycle.SessionAction(ctx, session, strings.TrimPrefix(action, "session-lifecycle-"))
		if err == nil {
			err = d.dispatcher().RegisterReached(&addressed, session)
		}
	}
	if err != nil {
		writeDashboardJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "unknown": surface.IsDeliveryOutcomeUnknown(err), "error": err.Error()})
		return
	}
	data, _ := json.Marshal(result)
	if action != "session-lifecycle-status" && action != "session-lifecycle-logs" && !(action == "native-queue" && queue.Action == "list") {
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "session-operation", SessionID: session.ID, Result: action + ": " + string(data)})
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}

const sessionAdoptTimeout = 2 * time.Minute

// dashboardSessionAdopt opens a session that runs elsewhere on this computer
// in the host. A busy session, or a terminal one without consent to stop it,
// is refused with 409.
func (d *Daemon) dashboardSessionAdopt(w http.ResponseWriter, r *http.Request, adapter surface.Surface, session *surface.Session, request surface.AdoptRequest) {
	adopter, ok := adapter.(surface.SessionAdopter)
	if !ok {
		writeDashboardJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("%s sessions cannot be opened in Agenthail", session.Surface)})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), sessionAdoptTimeout)
	defer cancel()
	result, err := adopter.AdoptSession(ctx, session, request)
	if err != nil {
		var busy *surface.AdoptBusyError
		var consent *surface.AdoptConsentError
		status := http.StatusBadGateway
		body := map[string]any{"ok": false, "unknown": surface.IsDeliveryOutcomeUnknown(err), "error": err.Error()}
		if errors.As(err, &busy) {
			status = http.StatusConflict
			body["busy"] = busy.Reason
		} else if errors.As(err, &consent) {
			status = http.StatusConflict
			body["needsConsent"] = true
		} else if surface.IsDeliveryTerminal(err) {
			status = http.StatusConflict
		}
		writeDashboardJSON(w, status, body)
		return
	}
	if result.Session != nil {
		if err := d.Registry.RegisterSession(*result.Session); err != nil {
			writeDashboardJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": fmt.Sprintf("session %s opened in Agenthail, but its registration failed: %s", session.ID, err)})
			return
		}
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "session-operation", SessionID: session.ID, Result: "adopt"})
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}
