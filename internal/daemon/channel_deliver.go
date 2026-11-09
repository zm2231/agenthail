package daemon

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) doChannelDeliver(ctx context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	if _, fromHost := requestingHost(ctx); !fromHost {
		return actionResult{}, actionFailed(http.StatusForbidden, "host_only", "channel-deliver is sent only by a paired host; send to a project with channel-send")
	}
	if strings.TrimSpace(request.ProjectID) == "" || strings.TrimSpace(request.Message) == "" {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", "projectId and message are required")
	}
	if err := channelDeliverScope(request); err != nil {
		return actionResult{}, err
	}
	operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	source, label, err := d.channelSender(request)
	if err != nil {
		return actionResult{}, err
	}
	target, listed, err := d.channelDeliverTarget(request.ProjectID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	if !listed {
		return d.channelReply(broadcast.Result{Target: target}, nil)
	}
	result, err := d.channelBroadcaster(request.PersonName).Send(operationCtx, target, request.Message, label, source)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	return d.channelReply(result, nil)
}

// A session of the sending host reaches only its own project here, as a
// direct send across hosts does; the person, the CLI and Agenthail reach any.
func channelDeliverScope(request dashboardActionRequest) error {
	if request.SourceSessionID == "" || request.SourceSessionID == surface.SystemSessionID {
		return nil
	}
	if request.SourceProjectID == "" {
		return &actionError{Status: http.StatusForbidden, Code: "out_of_scope", Message: "the sending host did not say which project the sender is in; a session may send across hosts only to its own project", Extra: map[string]any{"detail": map[string]any{"rule": "project_unconfirmed"}}}
	}
	if request.SourceProjectID != request.ProjectID {
		return &actionError{Status: http.StatusForbidden, Code: "out_of_scope", Message: fmt.Sprintf("the sender is in project %s, not %s; across hosts a session may send only to its own project", request.SourceProjectID, request.ProjectID), Extra: map[string]any{"detail": map[string]any{"rule": "different_project"}}}
	}
	return nil
}

func (d *Daemon) channelDeliverTarget(projectID string) (broadcast.Target, bool, error) {
	projects, err := broadcast.Projects(d.Registry)
	if err != nil {
		return broadcast.Target{}, false, err
	}
	for _, project := range projects {
		if project.ID == projectID {
			return broadcast.Target{Name: project.ID, ProjectID: project.ID, Project: project.DisplayName}, true, nil
		}
	}
	return broadcast.Target{Name: projectID, ProjectID: projectID}, false, nil
}
