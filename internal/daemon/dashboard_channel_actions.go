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
	name, home, err := d.channelDestination(ctx, request)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	if home != "" {
		operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
		defer cancel()
		return d.postToChannelHome(operationCtx, home, name, request)
	}
	request.Channel = name
	target, resolveErr := broadcast.Resolve(d.Registry, request.Channel)
	if err := d.channelPostScope(ctx, request, resolveErr, target, request.SourceSessionID); err != nil {
		return actionResult{}, err
	}
	if resolveErr != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", resolveErr.Error())
	}
	if _, fromHost := requestingHost(ctx); fromHost && target.ProjectID != "" {
		scoped := request
		scoped.ProjectID = target.ProjectID
		if err := channelDeliverScope(scoped); err != nil {
			return actionResult{}, err
		}
	}
	operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	source, label, err := d.channelSender(request)
	if err != nil {
		return actionResult{}, err
	}
	var legs []channelHostLeg
	var remote []broadcast.Recipient
	done := make(chan struct{})
	go func() {
		defer close(done)
		remote, legs = d.channelHostLegs(operationCtx, target, request.Message, source, request.ExceptHosts)
	}()
	result, sendErr := d.channelBroadcaster("").Send(operationCtx, target, request.Message, label, source)
	<-done
	if sendErr != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", sendErr.Error())
	}
	result.Recipients = append(result.Recipients, remote...)
	if target.ProjectID != "" && len(result.Recipients) == 0 && len(legs) == 0 {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", fmt.Sprintf("#%s has no members other than the sender", target.Name))
	}
	return d.channelReply(result, legs, request.ExceptHosts)
}

func (d *Daemon) channelSender(request dashboardActionRequest) (string, string, error) {
	if request.Author != "" && request.Author != surface.AuthorPerson || request.Author == surface.AuthorPerson && request.SourceSessionID != "" {
		return "", "", actionFailed(http.StatusBadRequest, "invalid_request", "author must be \"person\" and name no source session when set")
	}
	switch {
	case request.SourceSessionID != "":
		return request.SourceSessionID, d.resolveDisplay(d.coordinator().Canonical(request.SourceSessionID)), nil
	case request.Author == surface.AuthorPerson && request.PersonName != "":
		return surface.PersonSessionID, request.PersonName, nil
	case request.Author == surface.AuthorPerson:
		name, err := d.Registry.PersonName()
		if err != nil {
			return "", "", actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
		}
		return surface.PersonSessionID, name, nil
	}
	return "", "the Agenthail CLI", nil
}

func (d *Daemon) channelBroadcaster(personName string) broadcast.Broadcaster {
	service := d.coordinator()
	return broadcast.Broadcaster{Registry: d.Registry, Coordinators: service, Delivery: daemonBroadcastDelivery{d: d, sender: coordinator.Sender{Service: service}, personName: personName}}
}

func (d *Daemon) channelReply(result broadcast.Result, legs []channelHostLeg, except []string) (actionResult, error) {
	fields := result.Fields()
	if len(except) > 0 {
		fields["exceptHosts"] = except
	}
	unreachable := []string{}
	problems := []string{}
	for _, leg := range legs {
		switch leg.State {
		case channelLegHeld, channelLegUnknown:
			unreachable = append(unreachable, leg.Name)
		case channelLegRefused:
			problems = append(problems, fmt.Sprintf("host %s refused: %s", leg.Name, leg.Detail))
		}
	}
	if legs != nil {
		fields["hosts"], fields["unreachable"] = legs, unreachable
	}
	for _, recipient := range result.Recipients {
		if recipient.Outcome != broadcast.OutcomeFailed {
			continue
		}
		label := recipient.Coordinator
		if label == "" {
			label = d.resolveDisplay(recipient.SessionID)
		}
		problems = append(problems, label+" failed: "+recipient.Detail)
	}
	if len(problems) == 0 {
		fields["ok"] = true
		return actionOK(fields), nil
	}
	message := fmt.Sprintf("channel delivery: %d sent, %d queued, %d submitted, %d failed (%s)", result.Count(broadcast.OutcomeSent), result.Count(broadcast.OutcomeQueued), result.Count(broadcast.OutcomeSubmitted), result.Count(broadcast.OutcomeFailed), strings.Join(problems, "; "))
	if len(unreachable) > 0 {
		message += "; held for unreachable " + strings.Join(unreachable, ", ")
	}
	return actionResult{}, &actionError{Status: http.StatusConflict, Code: "conflict", Message: message, Extra: fields}
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
