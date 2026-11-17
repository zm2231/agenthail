package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/targets"
)

// mcpSessionOpenRequests reads the open requests of the session ref names,
// from its provider on this host or from the paired host that runs it, so a
// lead reads a worker's prompts wherever the worker runs.
func (d *Daemon) mcpSessionOpenRequests(ctx context.Context, caller *mcpCaller, ref string) (string, openRequests, error) {
	session, err := d.mcpReadableSession(caller, ref)
	if err == nil {
		open, err := d.openRequests(ctx, session)
		return session.ID, open, err
	}
	var refusal *mcpRefusal
	if !errors.As(err, &refusal) || refusal.Code != "not_found" && refusal.Code != "out_of_scope" {
		return "", openRequests{}, err
	}
	_, remote, resolveErr := d.mcpTargets(caller).Resolve(ctx, strings.TrimSpace(ref))
	if resolveErr != nil {
		return "", openRequests{}, mcpTargetRefusal(resolveErr)
	}
	if remote == nil {
		return "", openRequests{}, err
	}
	if err := mcpRemoteReadable(caller, ref, *remote); err != nil {
		return "", openRequests{}, err
	}
	open, err := d.hostOpenRequests(ctx, remote.ID)
	return remote.ID, open, err
}

// mcpRemoteReadable admits reading a paired host's session exactly as
// agenthail_sessions lists it (mcpVisibleHostSessions): any with read.all, or
// one the owning host places in the caller's project.
func mcpRemoteReadable(caller *mcpCaller, ref string, remote targets.Remote) error {
	if !remote.Exact {
		return refuse("invalid", fmt.Sprintf("%s does not name one session exactly; it matched %s. Address it by @alias or session id.", ref, remote.Label()))
	}
	if remote.Archived {
		return scopeRefusal(remote.ID, "archived", fmt.Sprintf("%s on host %s is archived.", remote.Label(), remote.Host.Name))
	}
	if caller.has("read.all") {
		return nil
	}
	if caller.has("read.project") && caller.project != nil && remote.Project == caller.project.ID {
		return nil
	}
	return scopeRefusal(remote.ID, "not_visible", fmt.Sprintf("%s runs on host %s and is not in your project.", remote.Label(), remote.Host.Name))
}

// hostOpenRequests asks the paired host that runs a session for its open
// requests, each under the id of its ask there.
func (d *Daemon) hostOpenRequests(ctx context.Context, qualifiedID string) (openRequests, error) {
	hostID, rawID, _ := hosts.Split(qualifiedID)
	host, paired := d.pairedHost(hostID)
	if !paired {
		return openRequests{}, refuse("not_found", fmt.Sprintf("Host %s is no longer paired.", hostID))
	}
	readCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	response, err := host.client.Do(readCtx, http.MethodGet, "/api/v1/requests", url.Values{"sessionId": {rawID}}, nil, nil)
	if err != nil {
		return openRequests{}, &mcpRefusal{Code: "refused", Message: fmt.Sprintf("Host %s did not answer: %s", host.record.Name, err), Retryable: true}
	}
	body, err := hosts.ReadResponse(response)
	if err != nil {
		return openRequests{}, &mcpRefusal{Code: "refused", Message: fmt.Sprintf("Host %s did not answer: %s", host.record.Name, err), Retryable: true}
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &failure)
		refusal, _ := mcpCodeFor(response.StatusCode, failure.Error.Code)
		refusal.Message = fmt.Sprintf("Host %s: %s", host.record.Name, failure.Error.Message)
		return openRequests{}, &refusal
	}
	var listed struct {
		PermissionMode string            `json:"permissionMode"`
		Requests       []openRequestView `json:"requests"`
		ProviderError  string            `json:"providerError"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		return openRequests{}, &mcpRefusal{Code: "refused", Message: fmt.Sprintf("Host %s answered with requests this host cannot read: %s", host.record.Name, err), Retryable: true}
	}
	return openRequests{requests: listed.Requests, permissionMode: listed.PermissionMode, providerError: listed.ProviderError}, nil
}
