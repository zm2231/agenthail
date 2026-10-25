package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) dashboardSessionAction(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	if request.SessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "sessionId is required")
		return
	}
	session, err := d.Registry.Session(request.SessionID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	if request.Action == "limit-resume" || request.Action == "limit-resume-cancel" {
		d.limitResumeAction(w, session.ID, request.Action == "limit-resume")
		return
	}
	if request.Action == "session-focus" {
		if session.Runtime == nil || !session.Runtime.Focusable || session.Runtime.Location == nil {
			writeAPIError(w, http.StatusConflict, "conflict", "session is not focusable")
			return
		}
		if d.transportResolver == nil {
			writeAPIError(w, http.StatusConflict, "conflict", "launcher transport is unavailable")
			return
		}
		launcher, ok := d.transportResolver.Launcher(session.Runtime.Launcher)
		focuser, focusable := launcher.(surface.Focuser)
		if !ok || !focusable {
			writeAPIError(w, http.StatusConflict, "conflict", "launcher cannot focus sessions")
			return
		}
		adapter, adapterErr := d.adapters().For(session)
		if adapterErr != nil {
			session.Runtime.Focusable = false
			_ = d.Registry.SaveSessionRuntime(session.ID, session.Runtime)
			writeAPIError(w, http.StatusConflict, "conflict", "session surface is unavailable")
			return
		}
		located, err := adapter.List(r.Context())
		if err != nil {
			session.Runtime.Focusable = false
			_ = d.Registry.SaveSessionRuntime(session.ID, session.Runtime)
			writeAPIError(w, http.StatusConflict, "conflict", "session location is unavailable")
			return
		}
		locations := launcher.Locate(r.Context(), located)
		current, found := locations[session.ID]
		if !found || current != *session.Runtime.Location {
			session.Runtime.Focusable = false
			_ = d.Registry.SaveSessionRuntime(session.ID, session.Runtime)
			writeAPIError(w, http.StatusConflict, "conflict", "session location is stale")
			return
		}
		if err := focuser.Focus(r.Context(), *session.Runtime.Location); err != nil {
			writeAPIError(w, http.StatusBadGateway, "bad_gateway", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "launcher": session.Runtime.Launcher, "location": session.Runtime.Location, "sessionId": session.ID})
		return
	}
	if request.Action == "session-archive" || request.Action == "session-unarchive" {
		d.sessionArchiveAction(w, request.Action, session.ID)
		return
	}
	if request.Action == "alias" {
		alias := strings.TrimPrefix(strings.TrimSpace(request.Alias), "@")
		if alias == "" || strings.ContainsAny(alias, " \t\r\n/#") || len(alias) > 80 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "name must be 1 to 80 characters without spaces, /, or #")
			return
		}
		if err := d.Registry.SetAlias(alias, session.ID); err != nil {
			var taken registry.AliasTakenError
			var reserved registry.AliasReservedError
			if errors.As(err, &taken) {
				writeAPIError(w, http.StatusConflict, "conflict", fmt.Sprintf("@%s is already used by another session", alias))
				return
			}
			if errors.As(err, &reserved) {
				writeAPIError(w, http.StatusConflict, "conflict", reserved.Error())
				return
			}
			writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("name conversation: %s", err))
			return
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "identified", SessionID: session.ID, Result: "@" + alias})
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "alias": alias})
		return
	}
	adapter, transportErr := d.adapters().For(session)
	if transportErr != nil {
		writeAPIError(w, http.StatusConflict, "conflict", transportErr.Error())
		return
	}
	if request.PID != 0 {
		if err := pinHolder(r.Context(), adapter, session, request.PID, request.PIDStartedAt); err != nil {
			writeAPIError(w, http.StatusConflict, "conflict", err.Error())
			return
		}
	}
	if request.Action == "session-adopt" {
		d.dashboardSessionAdopt(w, r, adapter, session, surface.AdoptRequest{StopTerminal: request.StopTerminal})
		return
	}
	if request.Action == "session-fork" || request.Action == "native-queue" || strings.HasPrefix(request.Action, "session-lifecycle-") {
		d.dashboardSessionOperation(w, r, adapter, session, request.Action, request.Fork, strings.TrimPrefix(strings.TrimSpace(request.Alias), "@"), strings.TrimSpace(request.SourceSessionID), request.NativeQueue)
		return
	}
	if err := d.ensureDashboardWritable(r.Context(), adapter, session); err != nil {
		writeAPIError(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	effective := surface.EffectiveCapabilities(adapter, session)
	ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	var result any
	switch request.Action {
	case "send":
		if strings.TrimSpace(request.Message) == "" && len(request.Attachments) == 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "message is required")
			return
		}
		attached, uploadErr := d.resolveUploads(request.Attachments)
		if uploadErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", uploadErr.Error())
			return
		}
		if len(attached) > 0 && !effective.Attach {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session cannot take attached files")
			return
		}
		if request.Author != "" && request.Author != surface.AuthorPerson {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "author must be \"person\" when set")
			return
		}
		if request.Author == surface.AuthorPerson && request.SourceSessionID != "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "a person's message cannot also name a source session")
			return
		}
		if request.SourceSessionID != "" {
			if _, err := d.Registry.Session(request.SourceSessionID); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "source session not found")
				return
			}
		}
		if request.RelayHops != 0 {
			if len(attached) > 0 {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "a relay leg carries no attached files")
				return
			}
			d.queueHostRelay(w, r, session, request.Message, request.SourceSessionID, request.BusyDelivery, request.RelayHops)
			return
		}
		options := surface.SendOptions{Model: request.Model, SourceSessionID: request.SourceSessionID, BusyDelivery: request.BusyDelivery, TurnOptions: request.TurnOptions, Author: request.Author, PersonName: request.PersonName, Uploads: attached}
		receipt, actionErr := coordinator.Sender{Service: d.coordinator()}.DeliverWithOptions(ctx, adapter, session, request.Message, "", options)
		if actionErr != nil {
			writeAPIError(w, http.StatusBadGateway, "bad_gateway", actionErr.Error())
			return
		}
		result = receipt
	case "steer":
		if !effective.Steer || strings.TrimSpace(request.Message) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session cannot be steered")
			return
		}
		if request.Author != "" && request.Author != surface.AuthorPerson {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "author must be \"person\" when set")
			return
		}
		if request.Author == surface.AuthorPerson && request.SourceSessionID != "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "a person's message cannot also name a source session")
			return
		}
		if request.SourceSessionID != "" {
			if _, sourceErr := d.Registry.Session(request.SourceSessionID); sourceErr != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "source session not found")
				return
			}
			ctx = surface.WithSourceSessionID(ctx, request.SourceSessionID)
		}
		if request.PersonName != "" {
			ctx = surface.WithPersonName(ctx, request.PersonName)
		}
		result, err = d.dispatcher().Steer(ctx, adapter, session, request.Message, request.Author)
	case "interrupt":
		if !effective.Interrupt {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session cannot be interrupted")
			return
		}
		result, err = d.control(ctx, adapter, session, surface.ControlRequest{Action: "interrupt"})
	case "compact":
		if !effective.Compact {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session cannot be compacted")
			return
		}
		result, err = d.dispatcher().Compact(ctx, adapter, session)
	case "goal-set", "goal-edit":
		result, err = d.dispatcher().GoalSet(ctx, adapter, session, request.Message, request.Action == "goal-edit")
	case "goal-clear":
		result, err = d.dispatcher().GoalClear(ctx, adapter, session)
	case "goal-pause", "goal-resume", "goal-budget":
		controller, ok := adapter.(surface.GoalController)
		if !effective.Goal || !ok {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session does not support typed goal controls")
			return
		}
		var update surface.GoalUpdate
		switch request.Action {
		case "goal-pause", "goal-resume":
			status := surface.GoalStatusPaused
			if request.Action == "goal-resume" {
				status = surface.GoalStatusActive
			}
			update = surface.GoalUpdate{Status: &status}
		case "goal-budget":
			budget := strings.TrimSpace(request.Message)
			if budget == "" {
				update = surface.GoalUpdate{ClearTokenBudget: true}
				break
			}
			value, parseErr := strconv.ParseInt(budget, 10, 64)
			if parseErr != nil || value < 0 {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "token budget must be a non-negative integer")
				return
			}
			update = surface.GoalUpdate{TokenBudget: &value}
		}
		err = controller.UpdateGoal(ctx, session, update)
	case "model":
		if !effective.Model {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session does not support model switching")
			return
		}
		if strings.TrimSpace(request.Model) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "model is required")
			return
		}
		result, err = d.control(ctx, adapter, session, surface.ControlRequest{Action: "model", Value: request.Model})
		if err == nil {
			d.recordSetting(session, "model", strings.TrimSpace(request.Model), actionActor(r))
		}
		if err == nil && session.Surface == surface.KindClaude {
			if persistErr := d.Registry.RegisterSession(*session); persistErr != nil {
				err = fmt.Errorf("persist Claude model state: %w", persistErr)
			}
		}
	case "permission-mode":
		if !effective.PermissionMode {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session's permission mode cannot change while it runs")
			return
		}
		mode := strings.TrimSpace(request.PermissionMode)
		result, err = d.control(ctx, adapter, session, surface.ControlRequest{Action: "permission-mode", Value: mode})
		if err == nil {
			d.recordSetting(session, "permission-mode", mode, actionActor(r))
		}
	case "respond":
		d.respond(ctx, w, adapter, session, strings.TrimSpace(request.RequestID), request.Decision, actionActor(r))
		return
	case "context", "usage", "status", "effort", "rename":
		if _, ok := adapter.(surface.Controller); !ok || !effective.Commands {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session does not support Claude Remote Control commands")
			return
		}
		result, err = d.control(ctx, adapter, session, surface.ControlRequest{Action: request.Action, Value: request.Message})
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "unsupported dashboard action")
		return
	}
	if err != nil {
		if control, ok := result.(*surface.ControlResult); ok && control != nil {
			writeAPIErrorWith(w, http.StatusBadGateway, "control_failed", err.Error(), map[string]any{"result": control})
			return
		}
		if surface.DeliveryTerminalReason(err) == surface.DeliveryInvalidRequest {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeAPIError(w, http.StatusBadGateway, "bad_gateway", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}
