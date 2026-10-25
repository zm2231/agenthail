package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func (d *Daemon) dashboardActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	mutation := &mutationResponseWriter{ResponseWriter: w}
	w = mutation
	defer func() {
		if mutation.succeeded() {
			d.publishEvent("state.changed", "", map[string]string{"source": "action"})
		}
	}()
	defer r.Body.Close()
	var request dashboardActionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 140<<10)).Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid dashboard request")
		return
	}
	if len(request.Attachments) > 0 && request.Action != "send" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "attachments are only accepted with send")
		return
	}
	if strings.HasPrefix(request.Action, "project-") {
		d.projectAction(r, w, projectRequest{Action: request.Action, ProjectID: request.ProjectID, Path: request.Path, Title: request.Title})
		return
	}
	if strings.HasPrefix(request.Action, "coordinator-") {
		d.coordinatorAction(r, w, coordinatorRequest{Action: request.Action, SessionID: request.SessionID, SourceSessionID: request.SourceSessionID, ProjectID: request.ProjectID, Name: request.Name, Surface: request.Surface, Cwd: request.Cwd, Path: request.Path, Model: request.Model, PermissionMode: request.PermissionMode, Instructions: request.Instructions, Replace: request.Replace, ChangedAt: request.ChangedAt, ChangedBy: request.ChangedBy, Message: request.Message, Author: request.Author, PersonName: request.PersonName})
		return
	}
	if request.Action == "delivery-dismiss" {
		d.dashboardDeliveryDismiss(w, r, request)
		return
	}
	if request.Action == "session-create" {
		d.dashboardSessionCreate(w, r, request)
		return
	}
	if request.Action == "notion-create" {
		d.dashboardNotionCreate(w, r, request)
		return
	}
	if request.Action == "queue-retry" {
		d.dashboardQueueRetry(w, r, request)
		return
	}
	if request.Action == "queue-cancel" {
		d.dashboardQueueCancel(w, r, request)
		return
	}
	if request.Action == "channel-create" {
		d.dashboardChannelCreate(w, r, request)
		return
	}
	if request.Action == "channel-delete" {
		d.dashboardChannelDelete(w, r, request)
		return
	}
	if request.Action == "channel-send" {
		d.dashboardChannelSend(w, r, request)
		return
	}
	if request.Action == "channel-rename" {
		d.dashboardChannelRename(w, r, request)
		return
	}
	if request.Action == "channel-add" || request.Action == "channel-remove" {
		d.dashboardChannelMembership(w, r, request)
		return
	}
	if request.Action == "relay-add" {
		d.dashboardRelayAdd(w, r, request)
		return
	}
	if request.Action == "relay-remove" {
		d.dashboardRelayRemove(w, r, request)
		return
	}
	if request.Action == "codex-launch" {
		d.dashboardCodexLaunch(w, r, request)
		return
	}
	if request.Action == "runtime-ensure" {
		d.dashboardRuntimeEnsure(w, r, request)
		return
	}
	d.dashboardSessionAction(w, r, request)
}
