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

func (d *Daemon) doSessionOperation(ctx context.Context, adapter surface.Surface, session *surface.Session, action string, forkOptions surface.ForkOptions, forkName, creator string, queue surface.NativeQueueRequest) (actionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
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
				return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", fmt.Sprintf("sourceSessionId %s is not a session here", creator))
			}
		}
		if forkOptions.BeforeTurnID != "" {
			if forkOptions.LastTurnID != "" {
				return actionResult{}, actionFailed(http.StatusBadRequest, "fork_point_conflict", "A fork takes lastTurnId or beforeTurnId, not both.")
			}
			previous, found, lookupErr := d.Registry.JournalTurnBefore(session.ID, forkOptions.BeforeTurnID)
			switch {
			case lookupErr != nil:
				return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", fmt.Sprintf("read the session's turns: %s", lookupErr))
			case !found:
				return actionResult{}, actionFailed(http.StatusConflict, "fork_turn_unknown", "Agenthail no longer holds that part of this conversation, so it can't fork from there.")
			case previous == "":
				return actionResult{}, actionFailed(http.StatusConflict, "fork_first_turn", "That prompt started the conversation, so there is nothing before it to fork.")
			}
			forkOptions = surface.ForkOptions{Model: forkOptions.Model, LastTurnID: previous}
		}
		name, reserveErr := d.reserveName(forkName)
		if reserveErr != nil {
			return actionResult{}, reserveErr
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
		code := "session_operation_failed"
		if surface.IsDeliveryOutcomeUnknown(err) {
			code = "session_operation_unknown"
		}
		return actionResult{}, actionFailed(http.StatusBadGateway, code, err.Error())
	}
	data, _ := json.Marshal(result)
	if action != "session-lifecycle-status" && action != "session-lifecycle-logs" && !(action == "native-queue" && queue.Action == "list") {
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "session-operation", SessionID: session.ID, Result: action + ": " + string(data)})
	}
	return actionOK(map[string]any{"ok": true, "result": result}), nil
}

const sessionAdoptTimeout = 2 * time.Minute

// dashboardSessionAdopt opens a session that runs elsewhere on this computer
// in the host. A busy session, or a terminal one without consent to stop it,
// is refused with 409.
func (d *Daemon) doSessionAdopt(ctx context.Context, adapter surface.Surface, session *surface.Session, request surface.AdoptRequest) (actionResult, error) {
	adopter, ok := adapter.(surface.SessionAdopter)
	if !ok {
		return actionResult{}, actionFailed(http.StatusBadRequest, "session_adopt_unsupported", fmt.Sprintf("%s sessions cannot be opened in Agenthail", session.Surface))
	}
	ctx, cancel := context.WithTimeout(ctx, sessionAdoptTimeout)
	defer cancel()
	result, err := adopter.AdoptSession(ctx, session, request)
	if err != nil {
		var busy *surface.AdoptBusyError
		var consent *surface.AdoptConsentError
		status := http.StatusBadGateway
		code := "session_adopt_failed"
		if errors.As(err, &busy) {
			status = http.StatusConflict
			code = "session_adopt_busy"
		} else if errors.As(err, &consent) {
			status = http.StatusConflict
			code = "session_adopt_needs_consent"
		} else if surface.IsDeliveryTerminal(err) {
			status = http.StatusConflict
			code = "session_adopt_refused"
		} else if surface.IsDeliveryOutcomeUnknown(err) {
			code = "session_adopt_unknown"
		}
		extra := map[string]any{"session": session}
		if busy != nil {
			extra["busy"] = busy.Reason
		}
		return actionResult{}, &actionError{Status: status, Code: code, Message: err.Error(), Extra: extra}
	}
	if result.Session != nil {
		if err := d.Registry.RegisterSession(*result.Session); err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "session_adopt_unrecorded", fmt.Sprintf("session %s opened in Agenthail, but its registration failed: %s", session.ID, err))
		}
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "session-operation", SessionID: session.ID, Result: "adopt"})
	return actionOK(map[string]any{"ok": true, "result": result}), nil
}
