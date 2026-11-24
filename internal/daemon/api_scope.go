package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zm2231/agenthail/internal/agentroles"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) registerScopeAPI(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc(daemonapi.ScopePath, d.apiV1Guard(dashboard, "control", d.apiScopeHandler))
}

func (d *Daemon) apiScopeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
		return
	}
	if !localReach(r) {
		writeAPIError(w, http.StatusForbidden, "remote_refused", "Scope is answered only for this machine's CLI.")
		return
	}
	var request daemonapi.ScopeRequest
	if err := decodeAPIV1JSON(w, r, &request, false); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	caller, err := d.scopeCaller(request.SessionID)
	if err != nil {
		writeScopeRefusal(w, mcpRefusalFor(err))
		return
	}
	if request.Action == "create" {
		created, err := d.agentCreate(r.Context(), caller, request.Create)
		if err != nil {
			d.refuseScope(w, caller, request.Action, err)
			return
		}
		created.write(w)
		return
	}
	reply, err := d.agentScope(r, caller, request)
	if err != nil {
		d.refuseScope(w, caller, request.Action, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, reply)
}

func (d *Daemon) refuseScope(w http.ResponseWriter, caller *mcpCaller, action string, err error) {
	refusal := mcpRefusalFor(err)
	d.recordMCPScopeRefusal(caller, "cli "+action, refusal)
	writeScopeRefusal(w, refusal)
}

func (d *Daemon) scopeCaller(sessionID string) (*mcpCaller, error) {
	record, err := d.Registry.Caller(strings.TrimSpace(sessionID))
	if err != nil {
		return nil, refuse("not_found", "Agenthail does not know the calling session "+sessionID+".")
	}
	return d.agentCaller(record)
}

// The check and the start share the spawn lock, as agenthail_create's do, so concurrent starts cannot pass the children limit together.
func (d *Daemon) agentCreate(ctx context.Context, caller *mcpCaller, payload json.RawMessage) (actionReply, error) {
	var asked dashboardActionRequest
	if err := json.Unmarshal(payload, &asked); err != nil {
		return actionReply{}, refuse("invalid", "create: "+err.Error())
	}
	if host := strings.TrimSpace(asked.Host); host != "" && !d.isLocalHostRef(host) {
		return d.agentCreateOnHost(ctx, caller, host, asked)
	}
	configRoot, err := d.agentConfigRoot(caller, asked.ConfigRoot)
	if err != nil {
		return actionReply{}, err
	}
	request := dashboardActionRequest{
		ConfigRoot:  configRoot,
		TurnOptions: asked.TurnOptions, LaunchSettings: asked.LaunchSettings,
		Surface: asked.Surface, Cwd: asked.Cwd, Message: asked.Message, Model: asked.Model, Alias: asked.Alias, Name: asked.Name, Agent: asked.Agent,
		PermissionMode: asked.PermissionMode, Approval: asked.Approval, Isolation: asked.Isolation, Worktree: asked.Worktree, Base: asked.Base,
	}
	kind := surface.SurfaceKind(strings.TrimSpace(request.Surface))
	if kind != surface.KindClaude && kind != surface.KindCodex {
		return actionReply{}, refuse("invalid", "surface is claude or codex.")
	}
	mode := request.PermissionMode
	if kind == surface.KindCodex {
		mode = request.Approval
	}
	start, cwd, notice, err := d.mcpCreateScope(caller, kind, mode, request.Cwd)
	if err != nil {
		return actionReply{}, err
	}
	release, _ := d.spawnLocks.acquire(caller.session.ID, true)
	defer release()
	if err := d.mcpChildrenRefusal(caller); err != nil {
		return actionReply{}, err
	}
	request.Action, request.Cwd, request.PermissionMode, request.Approval = "session-create", cwd, start.permissionMode, start.approval
	request.Ceiling, request.SourceSessionID, request.Author = start.ceiling.encode(), caller.session.ID, ""
	body, err := json.Marshal(request)
	if err != nil {
		return actionReply{}, err
	}
	reply := d.performAction(ctx, sessionPrincipal(caller.session.ID), actionFormatAPIv1, body, "cli-create-"+uuid.NewString())
	if notice != "" && reply.Status >= 200 && reply.Status < 300 {
		d.notifyAgentAction(ctx, caller.session.ID, notice)
	}
	return reply, nil
}

func (d *Daemon) agentConfigRoot(caller *mcpCaller, choice string) (string, error) {
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root, err := surface.ResolveClaudeStartRoot(home, choice)
	if err != nil {
		return "", refuse("invalid", err.Error())
	}
	if caller.has("control.all") || caller.has("control.project") {
		return root, nil
	}
	if own := surface.ClaudeSessionConfigRoot(d.claudeConfigRoots(), caller.session.Transcript); own != nil && own.Root == root {
		return root, nil
	}
	return "", scopeRefusal("new:claude", "config_root", "A session you start runs on your own Claude account; only your project's coordinator places sessions on another root.")
}

func (d *Daemon) agentScope(r *http.Request, caller *mcpCaller, request daemonapi.ScopeRequest) (daemonapi.ScopeReply, error) {
	mode := strings.TrimSpace(request.Mode)
	switch request.Action {
	case "send", "control":
		if request.Payload != nil {
			return d.controlPayloadScope(r.Context(), caller, request)
		}
		if strings.TrimSpace(request.Target) == caller.session.ID {
			return daemonapi.ScopeReply{TargetID: caller.session.ID}, nil
		}
		reach := reachSession
		if request.Action == "send" {
			reach = reachQueued
			if mode == "now" {
				reach = reachSteer
			}
		}
		target, notice, err := d.mcpReachTarget(r.Context(), caller, request.Target, reach)
		if err != nil {
			return daemonapi.ScopeReply{}, err
		}
		reply := daemonapi.ScopeReply{TargetID: target.ID, Notice: d.holdAgentAction(caller, notice)}
		action := ""
		if request.Action == "send" {
			action = "send"
		}
		err = d.scopeReply(caller, &reply, target.ID, action, reach)
		return reply, err
	case "answer":
		return d.answerScope(r.Context(), caller, request.Target, mode)
	case "lead-send":
		project, notice, refusal := d.mcpLeadScope(caller, request.Project, mode)
		if refusal != nil {
			return daemonapi.ScopeReply{}, refusal
		}
		return daemonapi.ScopeReply{ProjectID: project.ID, Notice: d.holdAgentNotice(caller, notice)}, nil
	case "channel-send":
		if target, err := broadcast.Resolve(d.Registry, d.localChannelRef(request.Channel)); err == nil && target.ProjectID != "" {
			if refusal := mcpProjectPostScope(caller, target); refusal != nil {
				return daemonapi.ScopeReply{}, refusal
			}
		}
		return daemonapi.ScopeReply{}, nil
	case "ledger":
		if !caller.has("ledger.write") {
			return daemonapi.ScopeReply{}, refuse("capability_missing", "Writing the ledger needs ledger.write.")
		}
		key := strings.TrimSpace(request.Project)
		own, err := projectresolve.LedgerKey(d.Registry, caller.project)
		if err != nil {
			return daemonapi.ScopeReply{}, err
		}
		if key == "" || key != own {
			granted, err := d.ledgerGranted(caller, key)
			if err != nil {
				return daemonapi.ScopeReply{}, err
			}
			if !granted {
				return daemonapi.ScopeReply{}, scopeRefusal("project:"+key, "different_project", "Your role reaches only your own project's ledger; run the command in your own project's folder, or the person widens your role with agenthail roles ledgers.")
			}
		}
		if request.Mode != string(ledger.WorkTakeover) {
			return daemonapi.ScopeReply{ProjectID: key}, nil
		}
		notice, err := d.takeoverScope(caller, strings.TrimSpace(request.Target))
		if err != nil {
			return daemonapi.ScopeReply{}, err
		}
		return daemonapi.ScopeReply{ProjectID: key, Notice: d.holdAgentNotice(caller, notice)}, nil
	case "notice":
		if what, found := d.agentNotices.take(strings.TrimSpace(request.Notice), caller.session.ID, time.Now()); found && notifies(request.ControlDecision) {
			d.notifyAgentAction(r.Context(), caller.session.ID, what.told(request.NotSteered, strings.TrimSpace(request.Ran)))
		}
		return daemonapi.ScopeReply{}, nil
	case "operator":
		if request.Payload == nil {
			return daemonapi.ScopeReply{}, refuse("invalid", "operator needs the payload the command sends.")
		}
		notice, err := d.personActionScope(r.Context(), caller, *request.Payload)
		var refusal *mcpRefusal
		if err == nil || !request.AskPerson || !errors.As(err, &refusal) || refusal.Detail["approvable"] != true {
			return daemonapi.ScopeReply{Notice: notice}, err
		}
		ask, err := d.askPersonToAllow(r.Context(), caller, *request.Payload, request.Reason)
		if err != nil {
			return daemonapi.ScopeReply{}, err
		}
		return daemonapi.ScopeReply{AskID: ask.ID}, nil
	}
	return daemonapi.ScopeReply{}, refuse("invalid", "action is send, control, lead-send, channel-send, create, ledger, operator or notice.")
}

func writeScopeRefusal(w http.ResponseWriter, refusal *mcpRefusal) {
	status := http.StatusForbidden
	switch refusal.Code {
	case "invalid":
		status = http.StatusBadRequest
	case "not_found":
		status = http.StatusNotFound
	case "unavailable":
		status = http.StatusServiceUnavailable
	case "refused":
		if refusal.Retryable {
			status = http.StatusInternalServerError
		}
	}
	extra := map[string]any{"retryable": refusal.Retryable, "scope": true}
	if refusal.Detail != nil {
		extra["detail"] = refusal.Detail
	}
	writeAPIErrorWith(w, status, refusal.Code, refusal.Message, extra)
}

// answerScope admits the CLI's answer to a session's request: on this host
// as any control of the session, and on a paired host to a caller that may
// answer requests, as agenthail_answer does; that host then answers only a
// request routed to the caller.
func (d *Daemon) answerScope(ctx context.Context, caller *mcpCaller, ref, decision string) (daemonapi.ScopeReply, error) {
	_, remote, err := d.mcpTargets(caller).Resolve(ctx, strings.TrimSpace(ref))
	if err != nil {
		return daemonapi.ScopeReply{}, mcpTargetRefusal(err)
	}
	if remote == nil {
		target, err := d.reachTarget(ctx, caller, ref, reachSession)
		if err != nil {
			return daemonapi.ScopeReply{}, err
		}
		return daemonapi.ScopeReply{TargetID: target.ID}, nil
	}
	if !remote.Exact {
		return daemonapi.ScopeReply{}, refuse("invalid", fmt.Sprintf("%s does not name one session exactly; it matched %s. Address it by @alias or session id.", ref, remote.Label()))
	}
	if !caller.has("requests.answer") {
		return daemonapi.ScopeReply{}, refuse("capability_missing", "This session may not answer requests.")
	}
	if decision == "always" && caller.role.Decision("answer-for-person") == agentroles.Ask {
		return daemonapi.ScopeReply{}, (&mcpRefusal{Code: "exceeds_ceiling", Message: "Always allow adds a lasting rule that can raise the session's permission mode; allow this one request instead.", Detail: map[string]any{"axis": "permission", "action": "answer-for-person"}}).about(remote.ID)
	}
	return daemonapi.ScopeReply{TargetID: remote.ID}, nil
}
