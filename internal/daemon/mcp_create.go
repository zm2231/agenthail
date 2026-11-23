package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/agentroles"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/surface"
)

type mcpCreateInput struct {
	mcpWrite
	Surface        string `json:"surface" jsonschema:"claude, codex, or notion (a Notion AI thread, if your role may start one)."`
	Cwd            string `json:"cwd,omitempty" jsonschema:"Claude and Codex: absolute folder the session works in; it must be inside the folders you may work in."`
	Message        string `json:"message" jsonschema:"The session's first message."`
	Model          string `json:"model,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty" jsonschema:"Claude: plan, default, acceptEdits, auto or bypassPermissions. Codex: untrusted, on-request or never. It cannot exceed your own ceiling; omitted, the session gets your ceiling's level."`
	Alias          string `json:"alias,omitempty" jsonschema:"A name to reach the session by, without @."`
	Task           bool   `json:"task,omitempty" jsonschema:"Claude and Codex: record this as a task with a result you read with agenthail_result. The reply has its taskId. It settles when the child reports with agenthail_report, or at its last turn or goal, and it holds one of your running-session slots until then."`
	Isolation      string `json:"isolation,omitempty" jsonschema:"worktree: run in a new git worktree and branch of cwd's repository, removed once reported or archived if nothing would be lost."`
	Branch         string `json:"branch,omitempty" jsonschema:"Worktree branch; default ah/<alias>."`
	Base           string `json:"base,omitempty" jsonschema:"Worktree base ref; default cwd's HEAD."`
	Host           string `json:"host,omitempty" jsonschema:"Claude and Codex: a paired computer to start it on, by name; omitted, this one. cwd is a folder there."`
}

// agenthail_capabilities reports this same refusal, so the two cannot disagree.
func spawnRefusal(caller *mcpCaller, kind surface.SurfaceKind) *mcpRefusal {
	switch {
	case !caller.has("spawn"):
		return refuse("capability_missing", "This session may not start sessions.")
	case kind == surface.KindNotion && caller.role.Decision("notion-create") == agentroles.Ask:
		return &mcpRefusal{Code: "exceeds_ceiling", Message: "A Notion thread works in the person's Notion workspace, outside any ceiling; your role leaves starting one to the person.", Detail: map[string]any{"axis": "surface", "action": "notion-create"}}
	case kind != surface.KindClaude && kind != surface.KindCodex && kind != surface.KindNotion:
		return refuse("invalid", "surface is claude, codex or notion.")
	}
	return nil
}

func (d *Daemon) mcpCreateScope(caller *mcpCaller, kind surface.SurfaceKind, mode, rawCwd string) (childStart, string, string, error) {
	if refusal := spawnRefusal(caller, kind); refusal != nil {
		return childStart{}, "", "", refusal.about("new:" + string(kind))
	}
	if !filepath.IsAbs(strings.TrimSpace(rawCwd)) {
		return childStart{}, "", "", refuse("invalid", "cwd must be an absolute folder.")
	}
	cwd, err := projectresolve.CanonicalCatalogPath(strings.TrimSpace(rawCwd))
	if err != nil {
		return childStart{}, "", "", refuse("invalid", fmt.Sprintf("cwd: %s", err))
	}
	ceiling, err := parseCeiling(caller.ceiling)
	if err != nil {
		return childStart{}, "", "", err
	}
	about := "new:" + string(kind) + ":" + cwd
	start, err := childCeiling(ceiling, kind, strings.TrimSpace(mode), cwd)
	if refusal, ok := err.(*mcpRefusal); ok {
		return childStart{}, "", "", refusal.about(about)
	}
	if err != nil {
		return childStart{}, "", "", err
	}
	notice, err := fullModeNotice(caller, start, kind, "", cwd, about)
	if err != nil {
		return childStart{}, "", "", err
	}
	if err := d.depthRefusal(caller); err != nil {
		return childStart{}, "", "", err
	}
	return start, cwd, notice, nil
}

// A session the person started outside Agenthail has no ceiling, so a child it starts in the highest mode is the person's to hear about.
func fullModeNotice(caller *mcpCaller, start childStart, kind surface.SurfaceKind, host, cwd, about string) (string, error) {
	if !caller.outside || start.ceiling.Permission != "full" {
		return "", nil
	}
	mode := start.permissionMode
	if kind == surface.KindCodex {
		mode = start.approval
	}
	where := cwd
	if host != "" {
		where = cwd + " on " + host
	}
	switch caller.role.Decision("session-create-full") {
	case agentroles.Ask:
		return "", scopeRefusal(about, "full_mode", fmt.Sprintf("Starting a session in %s mode is for the person here; start it in a lower mode, or ask the person to start it.", mode))
	case agentroles.Notify:
		return fmt.Sprintf("Started a %s session in %s mode in %s", kind, mode, where), nil
	}
	return "", nil
}

func (d *Daemon) depthRefusal(caller *mcpCaller) error {
	ancestors, err := d.Registry.SessionAncestors(caller.session.ID)
	if err != nil {
		return err
	}
	if limit := caller.role.DepthLimit(); len(ancestors)+1 > limit {
		return &mcpRefusal{Code: "depth_limit", Message: fmt.Sprintf("Sessions nest at most %d deep below the one a person started; hand this work back to your spawner instead.", limit), Detail: map[string]any{"limit": limit}}
	}
	return nil
}

func (d *Daemon) mcpChildrenRefusal(caller *mcpCaller) error {
	running, err := d.Registry.RunningChildren(caller.session.ID)
	if err != nil {
		return err
	}
	if limit := caller.role.RunningLimit(); len(running) >= limit {
		return &mcpRefusal{Code: "children_limit", Message: fmt.Sprintf("You already have %d sessions running; wait for one to finish.", len(running)), Retryable: true, Detail: map[string]any{"limit": limit}}
	}
	return nil
}

func (d *Daemon) mcpCreate(ctx context.Context, caller *mcpCaller, input mcpCreateInput) (any, error) {
	kind := surface.SurfaceKind(strings.TrimSpace(input.Surface))
	if !caller.has("spawn") {
		return nil, spawnRefusal(caller, kind)
	}
	if strings.TrimSpace(input.Message) == "" {
		return nil, refuse("invalid", "message is required.")
	}
	if kind == surface.KindNotion && input.Task {
		return nil, refuse("invalid", "A Notion thread cannot be a task.")
	}
	if refusal := spawnRefusal(caller, kind); refusal != nil {
		return nil, refusal.about("new:" + string(kind))
	}
	if kind == surface.KindNotion {
		result, err := d.mcpAction(ctx, caller, "agenthail_create", input.mcpWrite, dashboardActionRequest{Action: "notion-create", Message: input.Message, Model: strings.TrimSpace(input.Model), Alias: strings.TrimSpace(input.Alias)})
		if caller.role.Decision("notion-create") == agentroles.Notify {
			d.noticeAfter(ctx, caller, "Started a Notion thread", err)
		}
		return result, err
	}
	isolation := strings.TrimSpace(input.Isolation)
	if isolation != "" && isolation != isolationWorktree {
		return nil, refuse("invalid", "isolation is worktree or omitted.")
	}
	if isolation == "" && (strings.TrimSpace(input.Branch) != "" || strings.TrimSpace(input.Base) != "") {
		return nil, refuse("invalid", "branch and base need isolation worktree.")
	}
	if host := strings.TrimSpace(input.Host); host != "" && !d.isLocalHostRef(host) {
		if input.Task {
			return nil, refuse("invalid", "A session on another computer cannot be a task.")
		}
		start, cwd, notices, err := d.remoteCreateScope(caller, host, kind, input.PermissionMode, input.Cwd)
		if err != nil {
			return nil, err
		}
		result, err := d.mcpAction(ctx, caller, "agenthail_create", input.mcpWrite, dashboardActionRequest{
			Action: "session-create", Host: host, Surface: string(kind), Cwd: cwd, Message: input.Message, Model: strings.TrimSpace(input.Model),
			PermissionMode: start.permissionMode, Approval: start.approval, Alias: strings.TrimSpace(input.Alias), Ceiling: start.ceiling.encode(),
			Isolation: isolation, Worktree: strings.TrimSpace(input.Branch), Base: strings.TrimSpace(input.Base),
		})
		for _, notice := range notices {
			d.noticeAfter(ctx, caller, notice, err)
		}
		return result, err
	}
	start, cwd, notice, err := d.mcpCreateScope(caller, kind, input.PermissionMode, input.Cwd)
	if err != nil {
		return nil, err
	}
	release, _ := d.spawnLocks.acquire(caller.session.ID, true)
	defer release()
	if err := d.mcpChildrenRefusal(caller); err != nil {
		return nil, err
	}
	result, err := d.mcpAction(ctx, caller, "agenthail_create", input.mcpWrite, dashboardActionRequest{
		Action: "session-create", Surface: string(kind), Cwd: cwd, Message: input.Message, Model: strings.TrimSpace(input.Model),
		PermissionMode: start.permissionMode, Approval: start.approval, Alias: strings.TrimSpace(input.Alias),
		SourceSessionID: caller.session.ID, Ceiling: start.ceiling.encode(), Task: input.Task,
		Isolation: isolation, Worktree: strings.TrimSpace(input.Branch), Base: strings.TrimSpace(input.Base),
	})
	d.noticeAfter(ctx, caller, notice, err)
	return result, err
}

// remoteCreateScope bounds a session an agent starts on a paired computer:
// the caller's permission ceiling holds there, and the folder it starts in
// is its only root, since the caller's folders are on this computer.
func (d *Daemon) remoteCreateScope(caller *mcpCaller, host string, kind surface.SurfaceKind, mode, rawCwd string) (childStart, string, []string, error) {
	about := "new:" + string(kind) + "@" + host
	if refusal := spawnRefusal(caller, kind); refusal != nil {
		return childStart{}, "", nil, refusal.about(about)
	}
	if kind != surface.KindClaude && kind != surface.KindCodex {
		return childStart{}, "", nil, refuse("invalid", "surface is claude or codex.")
	}
	if _, paired := d.pairedHost(host); !paired {
		return childStart{}, "", nil, refuse("invalid", fmt.Sprintf("No paired computer is named %q.", host))
	}
	decision := caller.role.Decision("session-create-remote")
	if decision == agentroles.Ask {
		return childStart{}, "", nil, scopeRefusal(about, "other_host", "Starting a session on another computer is for the person here; ask your coordinator or the person to start it.")
	}
	cwd := strings.TrimSpace(rawCwd)
	if !strings.HasPrefix(cwd, "/") {
		return childStart{}, "", nil, refuse("invalid", "cwd must be an absolute folder on that computer.")
	}
	cwd = path.Clean(cwd)
	ceiling, err := parseCeiling(caller.ceiling)
	if err != nil {
		return childStart{}, "", nil, err
	}
	ceiling.Roots = nil
	start, err := childCeiling(ceiling, kind, strings.TrimSpace(mode), cwd)
	if refusal, ok := err.(*mcpRefusal); ok {
		return childStart{}, "", nil, refusal.about(about)
	}
	if err != nil {
		return childStart{}, "", nil, err
	}
	full, err := fullModeNotice(caller, start, kind, host, cwd, about)
	if err != nil {
		return childStart{}, "", nil, err
	}
	if err := d.depthRefusal(caller); err != nil {
		return childStart{}, "", nil, err
	}
	var notices []string
	if decision == agentroles.Notify {
		notices = append(notices, remoteCreateWords(kind, host, cwd))
	}
	if full != "" {
		notices = append(notices, full)
	}
	return start, cwd, notices, nil
}

func remoteCreateWords(kind surface.SurfaceKind, host, cwd string) string {
	return fmt.Sprintf("Started a %s session on %s in %s", kind, host, cwd)
}

func (d *Daemon) agentCreateOnHost(ctx context.Context, caller *mcpCaller, host string, asked dashboardActionRequest) (actionReply, error) {
	if strings.TrimSpace(asked.ConfigRoot) != "" {
		return actionReply{}, refuse("invalid", "A Claude account root names a folder on this computer; start the session on that computer without one.")
	}
	if asked.Launcher != "" {
		return actionReply{}, refuse("invalid", "A session on another computer starts in the background, not in a terminal.")
	}
	if asked.Task {
		return actionReply{}, refuse("invalid", "A session on another computer cannot be a task.")
	}
	kind := surface.SurfaceKind(strings.TrimSpace(asked.Surface))
	mode := asked.PermissionMode
	if kind == surface.KindCodex {
		mode = asked.Approval
	}
	start, cwd, notices, err := d.remoteCreateScope(caller, host, kind, mode, asked.Cwd)
	if err != nil {
		return actionReply{}, err
	}
	request := dashboardActionRequest{
		Action: "session-create", Host: host, TurnOptions: asked.TurnOptions, LaunchSettings: asked.LaunchSettings,
		Surface: string(kind), Cwd: cwd, Message: asked.Message, Model: asked.Model, Alias: asked.Alias, Name: asked.Name, Agent: asked.Agent,
		PermissionMode: start.permissionMode, Approval: start.approval, Isolation: asked.Isolation, Worktree: asked.Worktree, Base: asked.Base,
		Ceiling: start.ceiling.encode(),
	}
	body, err := json.Marshal(request)
	if err != nil {
		return actionReply{}, err
	}
	reply := d.performAction(ctx, sessionPrincipal(caller.session.ID), actionFormatAPIv1, body, "cli-create-"+uuid.NewString())
	if reply.Status >= 200 && reply.Status < 300 {
		for _, notice := range notices {
			d.notifyAgentAction(ctx, caller.session.ID, notice)
		}
	}
	return reply, nil
}
