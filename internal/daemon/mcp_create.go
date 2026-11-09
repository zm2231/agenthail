package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	mcpDepthLimit    = 3
	mcpChildrenLimit = 8
)

type mcpCreateInput struct {
	mcpWrite
	Surface        string `json:"surface" jsonschema:"claude, codex, or notion (a Notion AI thread; for the person's voice operator only)."`
	Cwd            string `json:"cwd,omitempty" jsonschema:"Claude and Codex: absolute folder the session works in; it must be inside the folders you may work in."`
	Message        string `json:"message" jsonschema:"The session's first message."`
	Model          string `json:"model,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty" jsonschema:"Claude: plan, default, acceptEdits, auto or bypassPermissions. Codex: untrusted, on-request or never. It cannot exceed your own ceiling; omitted, the session gets your ceiling's level."`
	Alias          string `json:"alias,omitempty" jsonschema:"A name to reach the session by, without @."`
	Task           bool   `json:"task,omitempty" jsonschema:"Claude and Codex: record this as a task with a result you read with agenthail_result. The reply has its taskId. It settles when the child reports with agenthail_report, or at its last turn or goal, and it holds one of your running-session slots until then."`
	Isolation      string `json:"isolation,omitempty" jsonschema:"worktree: run in a new git worktree and branch of cwd's repository, removed once reported or archived if nothing would be lost."`
	Branch         string `json:"branch,omitempty" jsonschema:"Worktree branch; default ah/<alias>."`
	Base           string `json:"base,omitempty" jsonschema:"Worktree base ref; default cwd's HEAD."`
}

// agenthail_capabilities reports this same refusal, so the two cannot disagree.
func spawnRefusal(caller *mcpCaller, kind surface.SurfaceKind) *mcpRefusal {
	switch {
	case !caller.has("spawn"):
		return refuse("capability_missing", "This session may not start sessions.")
	case kind == surface.KindNotion && !caller.has("control.all"):
		return &mcpRefusal{Code: "exceeds_ceiling", Message: "A Notion thread works in the person's Notion workspace, outside any ceiling; starting one is for the person's voice operator.", Detail: map[string]any{"axis": "surface"}}
	case kind != surface.KindClaude && kind != surface.KindCodex && kind != surface.KindNotion:
		return refuse("invalid", "surface is claude, codex or notion.")
	}
	return nil
}

func (d *Daemon) mcpCreateScope(caller *mcpCaller, kind surface.SurfaceKind, mode, rawCwd string) (childStart, string, error) {
	if refusal := spawnRefusal(caller, kind); refusal != nil {
		return childStart{}, "", refusal
	}
	if !filepath.IsAbs(strings.TrimSpace(rawCwd)) {
		return childStart{}, "", refuse("invalid", "cwd must be an absolute folder.")
	}
	cwd, err := canonicalCatalogPath(strings.TrimSpace(rawCwd))
	if err != nil {
		return childStart{}, "", refuse("invalid", fmt.Sprintf("cwd: %s", err))
	}
	ceiling, err := parseCeiling(caller.ceiling)
	if err != nil {
		return childStart{}, "", err
	}
	start, err := childCeiling(ceiling, kind, strings.TrimSpace(mode), cwd)
	if err != nil {
		return childStart{}, "", err
	}
	ancestors, err := d.Registry.SessionAncestors(caller.session.ID)
	if err != nil {
		return childStart{}, "", err
	}
	if len(ancestors)+1 > mcpDepthLimit {
		return childStart{}, "", &mcpRefusal{Code: "depth_limit", Message: fmt.Sprintf("Sessions nest at most %d deep below the one a person started; hand this work back to your spawner instead.", mcpDepthLimit), Detail: map[string]any{"limit": mcpDepthLimit}}
	}
	return start, cwd, nil
}

func (d *Daemon) mcpChildrenRefusal(caller *mcpCaller) error {
	running, err := d.Registry.RunningChildren(caller.session.ID)
	if err != nil {
		return err
	}
	if running >= mcpChildrenLimit {
		return &mcpRefusal{Code: "children_limit", Message: fmt.Sprintf("You already have %d sessions running; wait for one to finish.", running), Retryable: true, Detail: map[string]any{"limit": mcpChildrenLimit}}
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
		return nil, refusal
	}
	if kind == surface.KindNotion {
		return d.mcpAction(ctx, caller, "agenthail_create", input.mcpWrite, dashboardActionRequest{Action: "notion-create", Message: input.Message, Model: strings.TrimSpace(input.Model), Alias: strings.TrimSpace(input.Alias)})
	}
	isolation := strings.TrimSpace(input.Isolation)
	if isolation != "" && isolation != isolationWorktree {
		return nil, refuse("invalid", "isolation is worktree or omitted.")
	}
	if isolation == "" && (strings.TrimSpace(input.Branch) != "" || strings.TrimSpace(input.Base) != "") {
		return nil, refuse("invalid", "branch and base need isolation worktree.")
	}
	start, cwd, err := d.mcpCreateScope(caller, kind, input.PermissionMode, input.Cwd)
	if err != nil {
		return nil, err
	}
	release, _ := d.spawnLocks.acquire(caller.session.ID, true)
	defer release()
	if err := d.mcpChildrenRefusal(caller); err != nil {
		return nil, err
	}
	return d.mcpAction(ctx, caller, "agenthail_create", input.mcpWrite, dashboardActionRequest{
		Action: "session-create", Surface: string(kind), Cwd: cwd, Message: input.Message, Model: strings.TrimSpace(input.Model),
		PermissionMode: start.permissionMode, Approval: start.approval, Alias: strings.TrimSpace(input.Alias),
		SourceSessionID: caller.session.ID, Ceiling: start.ceiling.encode(), Task: input.Task,
		Isolation: isolation, Worktree: strings.TrimSpace(input.Branch), Base: strings.TrimSpace(input.Base),
	})
}
