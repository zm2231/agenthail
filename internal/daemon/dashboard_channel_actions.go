package daemon

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) doChannelCreate(_ context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	if strings.TrimSpace(request.Channel) == "" {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", "channel name is required")
	}
	name := strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")
	if err := broadcast.ValidateChannelName(d.Registry, name); err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	if _, err := d.Registry.CreateChannel(name); err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	return actionOK(map[string]any{"ok": true}), nil
}

func (d *Daemon) doChannelDelete(_ context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	if err := d.Registry.DeleteChannel(strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")); err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	return actionOK(map[string]any{"ok": true}), nil
}

func (d *Daemon) doChannelSend(ctx context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	if strings.TrimSpace(request.Channel) == "" || strings.TrimSpace(request.Message) == "" {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", "channel and message are required")
	}
	target, err := broadcast.Resolve(d.Registry, request.Channel)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	if request.Author != "" && request.Author != surface.AuthorPerson || request.Author == surface.AuthorPerson && request.SourceSessionID != "" {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", "author must be \"person\" and name no source session when set")
	}
	service := d.coordinator()
	source, label := request.SourceSessionID, "the Agenthail CLI"
	switch {
	case source != "":
		label = d.resolveDisplay(service.Canonical(source))
	case request.Author == surface.AuthorPerson:
		source = surface.PersonSessionID
		if label, err = d.Registry.PersonName(); err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
		}
	}
	broadcaster := broadcast.Broadcaster{Registry: d.Registry, Coordinators: service, Delivery: daemonBroadcastDelivery{d: d, sender: coordinator.Sender{Service: service}}}
	result, err := broadcaster.Send(operationCtx, target, request.Message, label, source)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	sent, queued, submitted, failed := result.Count(broadcast.OutcomeSent), result.Count(broadcast.OutcomeQueued), result.Count(broadcast.OutcomeSubmitted), result.Count(broadcast.OutcomeFailed)
	fields := result.Fields()
	if failed > 0 {
		outcomes := make([]string, 0, len(result.Recipients))
		for _, recipient := range result.Recipients {
			label := recipient.Coordinator
			if label == "" {
				label = d.resolveDisplay(recipient.SessionID)
			}
			outcome := label + " " + recipient.Outcome
			if recipient.Outcome == broadcast.OutcomeFailed && recipient.Detail != "" {
				outcome += ": " + recipient.Detail
			}
			outcomes = append(outcomes, outcome)
		}
		return actionResult{}, &actionError{Status: http.StatusConflict, Code: "conflict", Message: fmt.Sprintf("channel delivery: %d sent, %d queued, %d submitted, %d failed (%s)", sent, queued, submitted, failed, strings.Join(outcomes, "; ")), Extra: fields}
	}
	fields["ok"] = true
	return actionOK(fields), nil
}

func (d *Daemon) doChannelRename(_ context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	name := strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")
	newName := strings.TrimPrefix(strings.TrimSpace(request.NewChannel), "#")
	if err := broadcast.ValidateChannelName(d.Registry, newName); err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	if err := d.Registry.RenameChannel(name, newName); err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	return actionOK(map[string]any{"ok": true}), nil
}

func (d *Daemon) doChannelMembership(ctx context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	if request.TargetID == "" || strings.TrimSpace(request.Channel) == "" {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", "channel and targetId are required")
	}
	targetID, resolveErr := d.resolveLinkTarget(request.TargetID)
	if resolveErr != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", fmt.Sprintf("resolve agent: %s", resolveErr))
	}
	var actionErr error
	if request.Action == "channel-add" {
		target, targetErr := d.Registry.Session(targetID)
		if targetErr != nil {
			return actionResult{}, actionFailed(http.StatusNotFound, "not_found", "session not found")
		}
		if !d.hostOwned(target.ID) {
			adapter, adapterErr := d.adapters().For(target)
			if adapterErr != nil {
				return actionResult{}, actionFailed(http.StatusConflict, "conflict", "target surface is not configured")
			}
			if err := d.ensureDashboardWritable(ctx, adapter, target); err != nil {
				return actionResult{}, actionFailed(http.StatusConflict, "conflict", err.Error())
			}
		}
		actionErr = d.Registry.AddToChannel(strings.TrimPrefix(strings.TrimSpace(request.Channel), "#"), targetID)
	} else {
		actionErr = d.Registry.RemoveFromChannel(strings.TrimPrefix(strings.TrimSpace(request.Channel), "#"), targetID)
	}
	if actionErr != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", actionErr.Error())
	}
	return actionOK(map[string]any{"ok": true}), nil
}
