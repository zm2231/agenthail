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
	"github.com/zm2231/agenthail/internal/worktree"
)

func daemonSessionAction(action string) bool {
	switch action {
	case "session-lifecycle-stop", "session-lifecycle-status", "session-lifecycle-logs", "session-archive", "work-clean":
		return true
	}
	return false
}

func invalidRequest(message string) error {
	return actionFailed(http.StatusBadRequest, "invalid_request", message)
}

func conflict(message string) error {
	return actionFailed(http.StatusConflict, "conflict", message)
}

func (d *Daemon) doSessionAction(ctx context.Context, p principal, request dashboardActionRequest) (actionResult, error) {
	if request.SessionID == "" {
		return actionResult{}, invalidRequest("sessionId is required")
	}
	session, err := d.Registry.Session(request.SessionID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusNotFound, "not_found", "session not found")
	}
	if daemonOwned, err := d.Registry.StartedByDaemon(session.ID); err != nil || daemonOwned && !daemonSessionAction(request.Action) {
		return actionResult{}, actionFailed(http.StatusConflict, "daemon_session", fmt.Sprint(errors.Join(err, registry.ErrDaemonSession)))
	}
	switch request.Action {
	case "limit-resume", "limit-resume-cancel":
		return d.doLimitResume(session.ID, request.Action == "limit-resume")
	case "limit-move":
		return d.doLimitMove(ctx, session.ID, request.ConfigRoot, request.StopTerminal)
	case "session-focus":
		return d.doSessionFocus(ctx, session)
	case "session-archive", "session-unarchive":
		return d.doSessionArchive(request.Action, session.ID)
	case "session-pin", "session-unpin":
		return d.doSessionPin(request.Action == "session-pin", session.ID)
	case "session-snooze":
		return d.doSessionSnooze(session, request.Until, request.Wake)
	case "session-wake":
		return d.doSessionWake(session.ID)
	case "work-clean":
		return d.doWorkClean(ctx, p, session.ID, worktree.Options{ForceIgnored: request.ForceIgnored})
	case "alias":
		return d.doSessionAlias(session, request.Alias)
	case "pr":
		return d.doPR(ctx, p, session, request)
	}
	adapter, transportErr := d.adapters().For(session)
	if transportErr != nil {
		return actionResult{}, conflict(transportErr.Error())
	}
	if request.PID != 0 {
		if err := pinHolder(ctx, adapter, session, request.PID, request.PIDStartedAt); err != nil {
			return actionResult{}, conflict(err.Error())
		}
	}
	if request.Action == "session-adopt" {
		return d.doSessionAdopt(ctx, adapter, session, surface.AdoptRequest{StopTerminal: request.StopTerminal})
	}
	if request.Action == "session-fork" || request.Action == "native-queue" || strings.HasPrefix(request.Action, "session-lifecycle-") {
		return d.doSessionOperation(ctx, adapter, session, request.Action, request.Fork, strings.TrimPrefix(strings.TrimSpace(request.Alias), "@"), strings.TrimSpace(request.SourceSessionID), request.NativeQueue)
	}
	if err := d.ensureDashboardWritable(ctx, adapter, session); err != nil {
		return actionResult{}, conflict(err.Error())
	}
	effective := surface.EffectiveCapabilities(adapter, session)
	ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	var result any
	switch request.Action {
	case "send":
		if strings.TrimSpace(request.Message) == "" && len(request.Attachments) == 0 {
			return actionResult{}, invalidRequest("message is required")
		}
		attached, uploadErr := d.resolveUploads(request.Attachments)
		if uploadErr != nil {
			return actionResult{}, invalidRequest(uploadErr.Error())
		}
		if len(attached) > 0 && !effective.Attach {
			return actionResult{}, invalidRequest("this session cannot take attached files")
		}
		if err := checkMessageAuthor(d, request); err != nil {
			return actionResult{}, err
		}
		if request.RelayHops != 0 {
			if len(attached) > 0 {
				return actionResult{}, invalidRequest("a relay leg carries no attached files")
			}
			return d.doHostRelay(ctx, session, request.Message, request.SourceSessionID, request.BusyDelivery, request.RelayHops)
		}
		options := surface.SendOptions{Model: request.Model, SourceSessionID: request.SourceSessionID, BusyDelivery: request.BusyDelivery, TurnOptions: request.TurnOptions, Author: request.Author, PersonName: request.PersonName, Uploads: attached}
		receipt, actionErr := coordinator.Sender{Service: d.coordinator()}.DeliverWithOptions(ctx, adapter, session, request.Message, "", options)
		if actionErr != nil {
			return actionResult{}, d.sendFailed(http.StatusBadGateway, "bad_gateway", actionErr)
		}
		result = receipt
	case "steer":
		if !effective.Steer || strings.TrimSpace(request.Message) == "" {
			return actionResult{}, invalidRequest("this session cannot be steered")
		}
		if err := checkMessageAuthor(d, request); err != nil {
			return actionResult{}, err
		}
		if request.SourceSessionID != "" {
			ctx = surface.WithSourceSessionID(ctx, request.SourceSessionID)
		}
		if request.PersonName != "" {
			ctx = surface.WithPersonName(ctx, request.PersonName)
		}
		result, err = d.dispatcher().Steer(ctx, adapter, session, request.Message, request.Author)
	case "interrupt":
		if !effective.Interrupt {
			return actionResult{}, invalidRequest("this session cannot be interrupted")
		}
		result, err = d.control(ctx, adapter, session, surface.ControlRequest{Action: "interrupt"})
	case "compact":
		if !effective.Compact {
			return actionResult{}, invalidRequest("this session cannot be compacted")
		}
		result, err = d.dispatcher().Compact(ctx, adapter, session)
	case "goal-set", "goal-edit":
		result, err = d.dispatcher().GoalSet(ctx, adapter, session, request.Message, request.Action == "goal-edit")
	case "goal-clear":
		result, err = d.dispatcher().GoalClear(ctx, adapter, session)
	case "goal-pause", "goal-resume", "goal-budget":
		controller, ok := adapter.(surface.GoalController)
		if !effective.Goal || !ok {
			return actionResult{}, invalidRequest("this session does not support typed goal controls")
		}
		update, updateErr := goalUpdate(request)
		if updateErr != nil {
			return actionResult{}, updateErr
		}
		err = controller.UpdateGoal(ctx, session, update)
	case "model":
		if !effective.Model {
			return actionResult{}, invalidRequest("this session does not support model switching")
		}
		if strings.TrimSpace(request.Model) == "" {
			return actionResult{}, invalidRequest("model is required")
		}
		result, err = d.control(ctx, adapter, session, surface.ControlRequest{Action: "model", Value: request.Model})
		if err == nil {
			d.recordSetting(session, "model", strings.TrimSpace(request.Model), p.actor())
		}
		if err == nil && session.Surface == surface.KindClaude {
			if persistErr := d.Registry.RegisterSession(*session); persistErr != nil {
				err = fmt.Errorf("persist Claude model state: %w", persistErr)
			}
		}
	case "turn-setting":
		provider, ok := adapter.(surface.TurnSettingsProvider)
		if !ok {
			return actionResult{}, invalidRequest("this session has no turn settings")
		}
		key, value := strings.TrimSpace(request.Setting), strings.TrimSpace(request.Value)
		if key == "" || value == "" {
			return actionResult{}, invalidRequest("setting and value are required")
		}
		err = provider.SetTurnSetting(ctx, session, key, value)
		if version, ok := surface.IsRuntimeVersionError(err); ok {
			return actionResult{}, &actionError{Status: http.StatusConflict, Code: "runtime_too_old", Message: err.Error(), Extra: map[string]any{"runtime": version.Runtime, "minimum": version.Minimum}}
		}
		if errors.Is(err, surface.ErrInvalidTurnSetting) || errors.Is(err, surface.ErrUnsupported) {
			return actionResult{}, invalidRequest(err.Error())
		}
		if err == nil {
			d.recordSetting(session, key, value, p.actor())
			result = &surface.ControlResult{SessionID: session.ID, Action: "turn-setting", Evidence: surface.EvidenceDelivered}
		}
		if err == nil && key == surface.TurnSettingModel && session.Surface == surface.KindClaude {
			if persistErr := d.Registry.RegisterSession(*session); persistErr != nil {
				err = fmt.Errorf("persist Claude model state: %w", persistErr)
			}
		}
	case "permission-mode":
		if !effective.PermissionMode {
			return actionResult{}, invalidRequest("this session's permission mode cannot change while it runs")
		}
		mode := strings.TrimSpace(request.PermissionMode)
		result, err = d.control(ctx, adapter, session, surface.ControlRequest{Action: "permission-mode", Value: mode})
		if err == nil {
			d.recordSetting(session, "permission-mode", mode, p.actor())
		}
	case "respond":
		requestID := strings.TrimSpace(request.RequestID)
		if isTurnEndRequest(requestID) {
			if p.Kind == principalSession {
				return actionResult{}, actionFailed(http.StatusForbidden, "forbidden", "an ended turn's questions are answered by the person")
			}
			personName := request.PersonName
			if personName == "" {
				personName, _ = d.Registry.PersonName()
			}
			return d.answerTurnEnd(ctx, adapter, session, requestID, request.Decision, personName, p.actor())
		}
		return d.respond(ctx, adapter, session, requestID, request.Decision, p.actor())
	case "request-withdraw":
		return d.withdrawReplacedRequest(ctx, adapter, session, strings.TrimSpace(request.RequestID), p.actor())
	case "context", "usage", "status", "effort", "rename":
		if _, ok := adapter.(surface.Controller); !ok || !effective.Commands {
			return actionResult{}, invalidRequest("this session does not support Claude Remote Control commands")
		}
		result, err = d.control(ctx, adapter, session, surface.ControlRequest{Action: request.Action, Value: request.Message})
	default:
		return actionResult{}, invalidRequest("unsupported dashboard action")
	}
	if err != nil {
		if control, ok := result.(*surface.ControlResult); ok && control != nil {
			return actionResult{}, &actionError{Status: http.StatusBadGateway, Code: "control_failed", Message: err.Error(), Extra: map[string]any{"result": control}}
		}
		if surface.DeliveryTerminalReason(err) == surface.DeliveryInvalidRequest {
			return actionResult{}, d.sendFailed(http.StatusBadRequest, "invalid_request", err)
		}
		return actionResult{}, d.sendFailed(http.StatusBadGateway, "bad_gateway", err)
	}
	return actionOK(map[string]any{"ok": true, "result": result}), nil
}

func checkMessageAuthor(d *Daemon, request dashboardActionRequest) error {
	if request.Author != "" && request.Author != surface.AuthorPerson {
		return invalidRequest("author must be \"person\" when set")
	}
	if request.Author == surface.AuthorPerson && request.SourceSessionID != "" {
		return invalidRequest("a person's message cannot also name a source session")
	}
	return d.knownSource(request.SourceSessionID)
}

func (d *Daemon) knownSource(source string) error {
	if source == "" {
		return nil
	}
	if err := d.Registry.EnsureSender(source); err != nil {
		return err
	}
	if _, err := d.Registry.Session(source); err != nil {
		return invalidRequest("source session not found")
	}
	return nil
}

func goalUpdate(request dashboardActionRequest) (surface.GoalUpdate, error) {
	if request.Action != "goal-budget" {
		status := surface.GoalStatusPaused
		if request.Action == "goal-resume" {
			status = surface.GoalStatusActive
		}
		return surface.GoalUpdate{Status: &status}, nil
	}
	budget := strings.TrimSpace(request.Message)
	if budget == "" {
		return surface.GoalUpdate{ClearTokenBudget: true}, nil
	}
	value, err := strconv.ParseInt(budget, 10, 64)
	if err != nil || value < 0 {
		return surface.GoalUpdate{}, invalidRequest("token budget must be a non-negative integer")
	}
	return surface.GoalUpdate{TokenBudget: &value}, nil
}

func (d *Daemon) doSessionFocus(ctx context.Context, session *surface.Session) (actionResult, error) {
	if session.Runtime == nil || !session.Runtime.Focusable || session.Runtime.Location == nil {
		return actionResult{}, conflict("session is not focusable")
	}
	if d.transportResolver == nil {
		return actionResult{}, conflict("launcher transport is unavailable")
	}
	launcher, ok := d.transportResolver.Launcher(session.Runtime.Launcher)
	focuser, focusable := launcher.(surface.Focuser)
	if !ok || !focusable {
		return actionResult{}, conflict("launcher cannot focus sessions")
	}
	unfocusable := func(message string) (actionResult, error) {
		session.Runtime.Focusable = false
		_ = d.Registry.SaveSessionRuntime(session.ID, session.Runtime)
		return actionResult{}, conflict(message)
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		return unfocusable("session surface is unavailable")
	}
	located, err := adapter.List(ctx)
	if err != nil {
		return unfocusable("session location is unavailable")
	}
	current, found := launcher.Locate(ctx, located)[session.ID]
	if !found || current != *session.Runtime.Location {
		return unfocusable("session location is stale")
	}
	if err := focuser.Focus(ctx, *session.Runtime.Location); err != nil {
		return actionResult{}, actionFailed(http.StatusBadGateway, "bad_gateway", err.Error())
	}
	return actionOK(map[string]any{"ok": true, "launcher": session.Runtime.Launcher, "location": session.Runtime.Location, "sessionId": session.ID}), nil
}

func (d *Daemon) doSessionAlias(session *surface.Session, requested string) (actionResult, error) {
	alias := strings.TrimPrefix(strings.TrimSpace(requested), "@")
	if alias == "" || strings.ContainsAny(alias, " \t\r\n/#") || len(alias) > 80 {
		return actionResult{}, invalidRequest("name must be 1 to 80 characters without spaces, /, or #")
	}
	if err := d.Registry.SetAlias(alias, session.ID); err != nil {
		var taken registry.AliasTakenError
		var reserved registry.AliasReservedError
		if errors.As(err, &taken) {
			return actionResult{}, conflict(fmt.Sprintf("@%s is already used by another session", alias))
		}
		if errors.As(err, &reserved) {
			return actionResult{}, conflict(reserved.Error())
		}
		return actionResult{}, invalidRequest(fmt.Sprintf("name conversation: %s", err))
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "identified", SessionID: session.ID, Result: "@" + alias})
	return actionOK(map[string]any{"ok": true, "alias": alias}), nil
}
