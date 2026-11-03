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
	Surface        string `json:"surface" jsonschema:"claude or codex."`
	Cwd            string `json:"cwd" jsonschema:"Absolute folder the session works in; it must be inside the folders you may work in."`
	Message        string `json:"message" jsonschema:"The session's first message."`
	Model          string `json:"model,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty" jsonschema:"Claude: plan, default, acceptEdits, auto or bypassPermissions. Codex: untrusted, on-request or never. It cannot exceed your own ceiling; omitted, the session gets your ceiling's level."`
	Alias          string `json:"alias,omitempty" jsonschema:"A name to reach the session by, without @."`
}

func (d *Daemon) mcpCreate(ctx context.Context, caller *mcpCaller, input mcpCreateInput) (any, error) {
	if !caller.has("spawn") {
		return nil, refuse("capability_missing", "This session may not start sessions.")
	}
	kind := surface.SurfaceKind(strings.TrimSpace(input.Surface))
	if kind != surface.KindClaude && kind != surface.KindCodex {
		return nil, refuse("invalid", "surface is claude or codex.")
	}
	if strings.TrimSpace(input.Message) == "" {
		return nil, refuse("invalid", "message is required.")
	}
	if !filepath.IsAbs(strings.TrimSpace(input.Cwd)) {
		return nil, refuse("invalid", "cwd must be an absolute folder.")
	}
	cwd, err := canonicalCatalogPath(strings.TrimSpace(input.Cwd))
	if err != nil {
		return nil, refuse("invalid", fmt.Sprintf("cwd: %s", err))
	}
	ceiling, err := parseCeiling(caller.ceiling)
	if err != nil {
		return nil, err
	}
	start, err := childCeiling(ceiling, kind, strings.TrimSpace(input.PermissionMode), cwd)
	if err != nil {
		return nil, err
	}
	ancestors, err := d.Registry.SessionAncestors(caller.session.ID)
	if err != nil {
		return nil, err
	}
	if len(ancestors)+1 > mcpDepthLimit {
		return nil, &mcpRefusal{Code: "depth_limit", Message: fmt.Sprintf("Sessions nest at most %d deep below the one a person started; hand this work back to your spawner instead.", mcpDepthLimit), Detail: map[string]any{"limit": mcpDepthLimit}}
	}
	release, _ := d.spawnLocks.acquire(caller.session.ID, true)
	defer release()
	running, err := d.Registry.RunningChildren(caller.session.ID)
	if err != nil {
		return nil, err
	}
	if running >= mcpChildrenLimit {
		return nil, &mcpRefusal{Code: "children_limit", Message: fmt.Sprintf("You already have %d sessions running; wait for one to finish.", running), Retryable: true, Detail: map[string]any{"limit": mcpChildrenLimit}}
	}
	return d.mcpAction(ctx, caller, "agenthail_create", input.mcpWrite, dashboardActionRequest{
		Action: "session-create", Surface: string(kind), Cwd: cwd, Message: input.Message, Model: strings.TrimSpace(input.Model),
		PermissionMode: start.permissionMode, Approval: start.approval, Alias: strings.TrimSpace(input.Alias),
		SourceSessionID: caller.session.ID, Ceiling: start.ceiling.encode(),
	})
}
