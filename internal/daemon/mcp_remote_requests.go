package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/targets"
)

// mcpSessionOpenRequests reads the open requests of the session ref names,
// from its provider on this host or from the paired host that runs it, so a
// lead reads a worker's prompts wherever the worker runs.
func (d *Daemon) mcpSessionOpenRequests(ctx context.Context, caller *mcpCaller, ref string) (string, openRequests, error) {
	session, remote, err := d.mcpReadable(ctx, caller, ref)
	if err != nil {
		return "", openRequests{}, err
	}
	if remote != nil {
		open, err := d.hostOpenRequests(ctx, remote.ID, caller.session.ID)
		return remote.ID, open, err
	}
	open, err := d.readOpenRequests(ctx, session, true, caller.session.ID)
	return session.ID, open, err
}

// mcpReadable resolves a session the caller may read: one here, as
// mcpReadableSession admits it, or one a paired host runs, by its
// host-qualified id, alias or bare id.
func (d *Daemon) mcpReadable(ctx context.Context, caller *mcpCaller, ref string) (*surface.Session, *targets.Remote, error) {
	session, err := d.mcpReadableSession(caller, ref)
	if err == nil {
		return session, nil, nil
	}
	var refusal *mcpRefusal
	if !errors.As(err, &refusal) || refusal.Code != "not_found" && refusal.Code != "out_of_scope" {
		return nil, nil, err
	}
	_, remote, resolveErr := d.mcpTargets(caller).Resolve(ctx, strings.TrimSpace(ref))
	if resolveErr != nil {
		return nil, nil, mcpTargetRefusal(resolveErr)
	}
	if remote == nil {
		return nil, nil, err
	}
	if err := d.mcpRemoteReadable(caller, ref, *remote); err != nil {
		return nil, nil, err
	}
	return nil, remote, nil
}

// mcpRemoteReadable admits reading a paired host's session exactly as
// agenthail_sessions lists it (mcpVisibleHostSessions): any with read.all,
// one the owning host places in the caller's project, or one it records the
// caller as having started.
func (d *Daemon) mcpRemoteReadable(caller *mcpCaller, ref string, remote targets.Remote) error {
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
	if caller.has("control.descendants") {
		tie, err := d.remoteStarterTie(caller, remote)
		if err != nil || tie == tieCallerStarted {
			return err
		}
	}
	return scopeRefusal(remote.ID, "not_visible", fmt.Sprintf("%s runs on host %s, is not in your project and was not started by you.", remote.Label(), remote.Host.Name))
}

// hostOpenRequests asks the paired host that runs a session for its open
// requests, each under the id of its ask there, with the asks it routes to
// reader, a session of this host.
func (d *Daemon) hostOpenRequests(ctx context.Context, qualifiedID, reader string) (openRequests, error) {
	var listed struct {
		PermissionMode string            `json:"permissionMode"`
		Requests       []openRequestView `json:"requests"`
		ProviderError  string            `json:"providerError"`
	}
	if err := d.hostGet(ctx, qualifiedID, "/api/v1/requests", "sessionId", url.Values{"reader": {reader}}, &listed); err != nil {
		return openRequests{}, err
	}
	return openRequests{requests: listed.Requests, permissionMode: listed.PermissionMode, providerError: listed.ProviderError}, nil
}
