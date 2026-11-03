package daemon

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

type sessionCeiling struct {
	Permission string   `json:"permission,omitempty"`
	Roots      []string `json:"roots,omitempty"`
}

var permissionRanks = []string{"plan", "ask", "edits", "auto", "full"}

var claudePermissionRank = map[string]string{"plan": "plan", "default": "ask", "manual": "ask", "dontAsk": "ask", "acceptEdits": "edits", "auto": "auto", "bypassPermissions": "full"}

var codexApprovalRank = map[string]string{"untrusted": "ask", "on-request": "edits", "never": "full"}

var claudeModeForRank = map[string]string{"plan": "plan", "ask": "default", "edits": "acceptEdits", "auto": "auto", "full": "bypassPermissions"}

var codexApprovalForRank = map[string]string{"ask": "untrusted", "edits": "on-request", "auto": "on-request", "full": "never"}

func rankOf(permission string) int { return slices.Index(permissionRanks, permission) }

func parseCeiling(raw json.RawMessage) (sessionCeiling, error) {
	var ceiling sessionCeiling
	if len(raw) == 0 {
		return ceiling, nil
	}
	if err := json.Unmarshal(raw, &ceiling); err != nil {
		return ceiling, fmt.Errorf("decode ceiling: %w", err)
	}
	return ceiling, nil
}

func (c sessionCeiling) encode() json.RawMessage {
	encoded, _ := json.Marshal(c)
	return encoded
}

func underRoot(path string, roots []string) bool {
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// An empty axis is unbounded, so a bounded limit never contains an unbounded ceiling.
func (c sessionCeiling) within(limit sessionCeiling) bool {
	if limit.Permission != "" && (c.Permission == "" || rankOf(c.Permission) > rankOf(limit.Permission)) {
		return false
	}
	if len(limit.Roots) == 0 {
		return true
	}
	if len(c.Roots) == 0 {
		return false
	}
	for _, root := range c.Roots {
		if !underRoot(root, limit.Roots) {
			return false
		}
	}
	return true
}

type childStart struct {
	permissionMode string
	approval       string
	ceiling        sessionCeiling
}

func childCeiling(caller sessionCeiling, kind surface.SurfaceKind, mode, cwd string) (childStart, error) {
	ranks, modes := claudePermissionRank, claudeModeForRank
	if kind == surface.KindCodex {
		ranks, modes = codexApprovalRank, codexApprovalForRank
	}
	rank := caller.Permission
	if mode != "" {
		requested, ok := ranks[mode]
		if !ok {
			return childStart{}, refuse("invalid", fmt.Sprintf("Unknown %s permission mode %q.", kind, mode))
		}
		rank = requested
	} else if rank != "" {
		mode = modes[rank]
		if mode == "" {
			return childStart{}, &mcpRefusal{Code: "exceeds_ceiling", Message: fmt.Sprintf("Your ceiling is %s, and %s has no mode that low.", rank, kind), Detail: map[string]any{"axis": "permission", "limit": rank}}
		}
	}
	if caller.Permission != "" && rankOf(rank) > rankOf(caller.Permission) {
		return childStart{}, &mcpRefusal{Code: "exceeds_ceiling", Message: fmt.Sprintf("%s is above your ceiling of %s.", mode, caller.Permission), Detail: map[string]any{"axis": "permission", "limit": caller.Permission}}
	}
	if len(caller.Roots) > 0 && !underRoot(cwd, caller.Roots) {
		return childStart{}, &mcpRefusal{Code: "exceeds_ceiling", Message: "That folder is outside the folders you may work in.", Detail: map[string]any{"axis": "roots", "limit": caller.Roots}}
	}
	start := childStart{ceiling: sessionCeiling{Permission: rank, Roots: caller.Roots}}
	if len(start.ceiling.Roots) == 0 {
		start.ceiling.Roots = []string{cwd}
	}
	if kind == surface.KindCodex {
		start.approval = mode
	} else {
		start.permissionMode = mode
	}
	return start, nil
}
