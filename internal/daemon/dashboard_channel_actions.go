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

func (d *Daemon) dashboardChannelCreate(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	if strings.TrimSpace(request.Channel) == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "channel name is required")
		return
	}
	name := strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")
	if err := broadcast.ValidateChannelName(d.Registry, name); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if _, err := d.Registry.CreateChannel(name); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
	return
}

func (d *Daemon) dashboardChannelDelete(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	if err := d.Registry.DeleteChannel(strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
	return
}

func (d *Daemon) dashboardChannelSend(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	if strings.TrimSpace(request.Channel) == "" || strings.TrimSpace(request.Message) == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "channel and message are required")
		return
	}
	target, err := broadcast.Resolve(d.Registry, request.Channel)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	operationCtx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	if request.Author != "" && request.Author != surface.AuthorPerson || request.Author == surface.AuthorPerson && request.SourceSessionID != "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "author must be \"person\" and name no source session when set")
		return
	}
	service := d.coordinator()
	source, label := request.SourceSessionID, "the Agenthail CLI"
	switch {
	case source != "":
		label = d.resolveDisplay(service.Canonical(source))
	case request.Author == surface.AuthorPerson:
		source = surface.PersonSessionID
		if label, err = d.Registry.PersonName(); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}
	broadcaster := broadcast.Broadcaster{Registry: d.Registry, Coordinators: service, Delivery: daemonBroadcastDelivery{d: d, sender: coordinator.Sender{Service: service}}}
	result, err := broadcaster.Send(operationCtx, target, request.Message, label, source)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	sent, queued, submitted, failed := result.Count(broadcast.OutcomeSent), result.Count(broadcast.OutcomeQueued), result.Count(broadcast.OutcomeSubmitted), result.Count(broadcast.OutcomeFailed)
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
		writeAPIError(w, http.StatusConflict, "conflict", fmt.Sprintf("channel delivery: %d sent, %d queued, %d submitted, %d failed (%s)", sent, queued, submitted, failed, strings.Join(outcomes, "; ")))
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "target": result.Target, "recipients": result.Recipients, "sent": sent, "queued": queued, "submitted": submitted, "failed": failed})
	return
}

func (d *Daemon) dashboardChannelRename(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	name := strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")
	newName := strings.TrimPrefix(strings.TrimSpace(request.NewChannel), "#")
	if err := broadcast.ValidateChannelName(d.Registry, newName); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := d.Registry.RenameChannel(name, newName); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
	return
}

func (d *Daemon) dashboardChannelMembership(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	if request.TargetID == "" || strings.TrimSpace(request.Channel) == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "channel and targetId are required")
		return
	}
	targetID, resolveErr := d.resolveLinkTarget(request.TargetID)
	if resolveErr != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("resolve agent: %s", resolveErr))
		return
	}
	var actionErr error
	if request.Action == "channel-add" {
		target, targetErr := d.Registry.Session(targetID)
		if targetErr != nil {
			writeAPIError(w, http.StatusNotFound, "not_found", "session not found")
			return
		}
		if !d.hostOwned(target.ID) {
			adapter, adapterErr := d.adapters().For(target)
			if adapterErr != nil {
				writeAPIError(w, http.StatusConflict, "conflict", "target surface is not configured")
				return
			}
			if err := d.ensureDashboardWritable(r.Context(), adapter, target); err != nil {
				writeAPIError(w, http.StatusConflict, "conflict", err.Error())
				return
			}
		}
		actionErr = d.Registry.AddToChannel(strings.TrimPrefix(strings.TrimSpace(request.Channel), "#"), targetID)
	} else {
		actionErr = d.Registry.RemoveFromChannel(strings.TrimPrefix(strings.TrimSpace(request.Channel), "#"), targetID)
	}
	if actionErr != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", actionErr.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
	return
}
