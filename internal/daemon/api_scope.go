package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/surface"
)

const ScopePath = "/api/v1/scope"

type ScopeRequest struct {
	SessionID      string          `json:"sessionId"`
	Action         string          `json:"action"`
	Target         string          `json:"target,omitempty"`
	Project        string          `json:"project,omitempty"`
	Mode           string          `json:"mode,omitempty"`
	Channel        string          `json:"channel,omitempty"`
	Surface        string          `json:"surface,omitempty"`
	PermissionMode string          `json:"permissionMode,omitempty"`
	Cwd            string          `json:"cwd,omitempty"`
	Payload        *PersonAction   `json:"payload,omitempty"`
	AskPerson      bool            `json:"askPerson,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Create         json.RawMessage `json:"create,omitempty"`
	Notice         string          `json:"notice,omitempty"`
}

type ScopeReply struct {
	TargetID       string `json:"targetId,omitempty"`
	ProjectID      string `json:"projectId,omitempty"`
	ScopeProjectID string `json:"scopeProjectId,omitempty"`
	AskID          string `json:"askId,omitempty"`
	Notice         string `json:"notice,omitempty"`
}

func (d *Daemon) registerScopeAPI(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc(ScopePath, d.apiV1Guard(dashboard, "control", d.apiScopeHandler))
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
	var request ScopeRequest
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
	start, cwd, err := d.mcpCreateScope(caller, kind, mode, request.Cwd)
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
	return d.performAction(ctx, sessionPrincipal(caller.session.ID), actionFormatAPIv1, body, "cli-create-"+uuid.NewString()), nil
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
	if caller.has("control.all") || d.mcpIsProjectLead(caller, caller.session.ID) {
		return root, nil
	}
	if own := surface.ClaudeSessionConfigRoot(d.claudeConfigRoots(), caller.session.Transcript); own != nil && own.Root == root {
		return root, nil
	}
	return "", scopeRefusal("new:claude", "config_root", "A session you start runs on your own Claude account; only your project's coordinator places sessions on another root.")
}

func (d *Daemon) agentScope(r *http.Request, caller *mcpCaller, request ScopeRequest) (ScopeReply, error) {
	mode := strings.TrimSpace(request.Mode)
	switch request.Action {
	case "send", "control":
		if request.Payload != nil {
			return d.controlPayloadScope(r.Context(), caller, request)
		}
		if strings.TrimSpace(request.Target) == caller.session.ID {
			return ScopeReply{TargetID: caller.session.ID}, nil
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
			return ScopeReply{}, err
		}
		reply := ScopeReply{TargetID: target.ID, Notice: d.holdAgentNotice(caller, notice)}
		if request.Action == "send" {
			reply.ScopeProjectID, err = d.remoteSendScope(caller, target)
		}
		return reply, err
	case "answer":
		return d.answerScope(r.Context(), caller, request.Target, mode)
	case "lead-send":
		project, notice, refusal := d.mcpLeadScope(caller, request.Project, mode)
		if refusal != nil {
			return ScopeReply{}, refusal
		}
		return ScopeReply{ProjectID: project.ID, Notice: d.holdAgentNotice(caller, notice)}, nil
	case "channel-send":
		if target, err := broadcast.Resolve(d.Registry, d.localChannelRef(request.Channel)); err == nil && target.ProjectID != "" {
			if refusal := mcpProjectPostScope(caller, target); refusal != nil {
				return ScopeReply{}, refusal
			}
		}
		return ScopeReply{}, nil
	case "ledger":
		if caller.has("control.all") {
			return ScopeReply{}, nil
		}
		if !caller.has("ledger.write") {
			return ScopeReply{}, refuse("capability_missing", "Writing the ledger needs ledger.write.")
		}
		own, err := LedgerKey(d.Registry, caller.project)
		if err != nil {
			return ScopeReply{}, err
		}
		if own == "" || own != strings.TrimSpace(request.Project) {
			return ScopeReply{}, scopeRefusal("project:"+strings.TrimSpace(request.Project), "different_project", "You may write only your own project's ledger; run the command in your own project's folder, or ask that project's coordinator.")
		}
		return ScopeReply{ProjectID: own}, nil
	case "notice":
		if what, found := d.agentNotices.take(strings.TrimSpace(request.Notice), caller.session.ID, time.Now()); found {
			d.notifyAgentAction(r.Context(), caller.session.ID, what)
		}
		return ScopeReply{}, nil
	case "operator":
		if request.Payload == nil {
			return ScopeReply{}, refuse("invalid", "operator needs the payload the command sends.")
		}
		notice, err := d.personActionScope(r.Context(), caller, *request.Payload)
		var refusal *mcpRefusal
		if err == nil || !request.AskPerson || !errors.As(err, &refusal) || refusal.Detail["approvable"] != true {
			return ScopeReply{Notice: notice}, err
		}
		ask, err := d.askPersonToAllow(r.Context(), caller, *request.Payload, request.Reason)
		if err != nil {
			return ScopeReply{}, err
		}
		return ScopeReply{AskID: ask.ID}, nil
	}
	return ScopeReply{}, refuse("invalid", "action is send, control, lead-send, channel-send, create, ledger, operator or notice.")
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
func (d *Daemon) answerScope(ctx context.Context, caller *mcpCaller, ref, decision string) (ScopeReply, error) {
	_, remote, err := d.mcpTargets(caller).Resolve(ctx, strings.TrimSpace(ref))
	if err != nil {
		return ScopeReply{}, mcpTargetRefusal(err)
	}
	if remote == nil {
		target, err := d.mcpControlTarget(ctx, caller, ref)
		if err != nil {
			return ScopeReply{}, err
		}
		return ScopeReply{TargetID: target.ID}, nil
	}
	if !remote.Exact {
		return ScopeReply{}, refuse("invalid", fmt.Sprintf("%s does not name one session exactly; it matched %s. Address it by @alias or session id.", ref, remote.Label()))
	}
	if !caller.has("requests.answer") {
		return ScopeReply{}, refuse("capability_missing", "This session may not answer requests.")
	}
	if decision == "always" && !caller.has("control.all") {
		return ScopeReply{}, (&mcpRefusal{Code: "exceeds_ceiling", Message: "Always allow adds a lasting rule that can raise the session's permission mode; allow this one request instead.", Detail: map[string]any{"axis": "permission"}}).about(remote.ID)
	}
	return ScopeReply{TargetID: remote.ID}, nil
}
