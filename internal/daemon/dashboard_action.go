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
	"session-create":   (*Daemon).doSessionCreate,
	"notion-create":    (*Daemon).doNotionCreate,
	"codex-launch":     (*Daemon).doCodexLaunch,
	"runtime-ensure":   (*Daemon).doRuntimeEnsure,
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

func (d *Daemon) dispatchAction(ctx context.Context, p principal, request dashboardActionRequest) (actionResult, error) {
	if len(request.Attachments) > 0 && request.Action != "send" {
		return actionResult{}, invalidRequest("attachments are only accepted with send")
	}
	if strings.HasPrefix(request.Action, "project-") {
		return d.doProjectAction(ctx, p, projectRequest{Action: request.Action, ProjectID: request.ProjectID, Path: request.Path, Title: request.Title})
	}
	if strings.HasPrefix(request.Action, "coordinator-") {
		return d.doCoordinatorAction(ctx, p, coordinatorRequest{Action: request.Action, SessionID: request.SessionID, SourceSessionID: request.SourceSessionID, ProjectID: request.ProjectID, Project: request.Project, Name: request.Name, Surface: request.Surface, Cwd: request.Cwd, Path: request.Path, Model: request.Model, PermissionMode: request.PermissionMode, Instructions: request.Instructions, Replace: request.Replace, ChangedAt: request.ChangedAt, ChangedBy: request.ChangedBy, Message: request.Message, Author: request.Author, PersonName: request.PersonName})
	}
	if operation, typed := typedActions[request.Action]; typed {
		return operation(d, ctx, p, request)
	}
	return d.doSessionAction(ctx, p, request)
}
