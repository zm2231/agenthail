package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type actionOperation func(*Daemon, context.Context, principal, dashboardActionRequest) (actionResult, error)

var typedActions = map[string]actionOperation{
	"delivery-dismiss": (*Daemon).doDeliveryDismiss,
	"queue-retry":      (*Daemon).doQueueRetry,
	"queue-cancel":     (*Daemon).doQueueCancel,
	"channel-create":   (*Daemon).doChannelCreate,
	"channel-delete":   (*Daemon).doChannelDelete,
	"channel-send":     (*Daemon).doChannelSend,
	"channel-rename":   (*Daemon).doChannelRename,
	"channel-add":      (*Daemon).doChannelMembership,
	"channel-remove":   (*Daemon).doChannelMembership,
	"relay-add":        (*Daemon).doRelayAdd,
	"relay-remove":     (*Daemon).doRelayRemove,
}

func (d *Daemon) dashboardActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	defer r.Body.Close()
	var request dashboardActionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 140<<10)).Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid dashboard request")
		return
	}
	d.doAction(withActionKey(r.Context(), r.Header.Get(actionIdempotencyHeader)), principalFromRequest(r), request).write(w)
}

func (d *Daemon) routeAction(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	if len(request.Attachments) > 0 && request.Action != "send" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "attachments are only accepted with send")
		return
	}
	if strings.HasPrefix(request.Action, "project-") {
		result, err := d.doProjectAction(r.Context(), principalFromRequest(r), projectRequest{Action: request.Action, ProjectID: request.ProjectID, Path: request.Path, Title: request.Title})
		writeActionResult(w, result, err)
		return
	}
	if strings.HasPrefix(request.Action, "coordinator-") {
		result, err := d.doCoordinatorAction(r.Context(), principalFromRequest(r), coordinatorRequest{Action: request.Action, SessionID: request.SessionID, SourceSessionID: request.SourceSessionID, ProjectID: request.ProjectID, Name: request.Name, Surface: request.Surface, Cwd: request.Cwd, Path: request.Path, Model: request.Model, PermissionMode: request.PermissionMode, Instructions: request.Instructions, Replace: request.Replace, ChangedAt: request.ChangedAt, ChangedBy: request.ChangedBy, Message: request.Message, Author: request.Author, PersonName: request.PersonName})
		writeActionResult(w, result, err)
		return
	}
	if operation, typed := typedActions[request.Action]; typed {
		result, err := operation(d, r.Context(), principalFromRequest(r), request)
		writeActionResult(w, result, err)
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
	if request.Action == "codex-launch" {
		d.dashboardCodexLaunch(w, r, request)
		return
	}
	if request.Action == "runtime-ensure" {
		d.dashboardRuntimeEnsure(w, r, request)
		return
	}
	result, err := d.doSessionAction(r.Context(), principalFromRequest(r), request)
	writeActionResult(w, result, err)
}
