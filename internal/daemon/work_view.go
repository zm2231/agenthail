package daemon

import (
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/worktree"
)

type workView struct {
	Path          string    `json:"path"`
	Branch        string    `json:"branch"`
	Base          string    `json:"base"`
	State         string    `json:"state"`
	SettledReason string    `json:"settledReason,omitempty"`
	Cleanup       string    `json:"cleanup,omitempty"`
	CleanupReason string    `json:"cleanupReason,omitempty"`
	KeptPaths     []string  `json:"keptPaths,omitempty"`
	Hint          *workHint `json:"hint,omitempty"`
}

// workHint is why kept work was not removed and, when git can show what is
// in the way, the command that shows it.
type workHint struct {
	Reason  string `json:"reason"`
	Command string `json:"command,omitempty"`
}

type moveView struct {
	State    string `json:"state"`
	Host     string `json:"host"`
	HostName string `json:"hostName,omitempty"`
	Root     string `json:"root,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

func moveRow(move *registry.MoveShown) *moveView {
	if move == nil {
		return nil
	}
	return &moveView{State: move.State, Host: move.PeerHost, HostName: move.HostName, Root: move.Root, Detail: move.Detail}
}

func workRow(record *registry.WorkRecord) *workView {
	if record == nil {
		return nil
	}
	return newWorkView(*record)
}

func newWorkView(record registry.WorkRecord) *workView {
	return &workView{Path: record.WorktreePath, Branch: record.Branch, Base: record.BaseRef, State: record.State, SettledReason: record.SettledReason, Cleanup: record.Cleanup, CleanupReason: record.CleanupReason, KeptPaths: record.KeptPaths, Hint: newWorkHint(record)}
}

func newWorkHint(record registry.WorkRecord) *workHint {
	if record.Cleanup != registry.WorkCleanupKept {
		return nil
	}
	git := "git -C " + quoteShell(record.WorktreePath) + " "
	switch reason := record.CleanupReason; reason {
	case worktree.KeptDirty:
		return &workHint{Reason: "It has changes that are not committed.", Command: git + "status"}
	case worktree.KeptUnpushed:
		return &workHint{Reason: "Its branch has commits that are on no remote and not merged.", Command: git + "log --oneline " + quoteShell(record.BaseRef) + "..HEAD"}
	case worktree.KeptIgnored:
		return &workHint{Reason: "It holds ignored files outside build and package folders.", Command: git + "status --ignored"}
	case worktree.KeptIgnoredMany:
		return &workHint{Reason: "It holds more ignored files outside build and package folders than can be listed here.", Command: git + "status --ignored"}
	case registry.WorkKeptRunning:
		return &workHint{Reason: "Its session is still working; it is removed once the turn ends."}
	default:
		return &workHint{Reason: "Removing it " + strings.TrimSpace(reason) + "."}
	}
}

func quoteShell(value string) string {
	if value != "" && strings.Trim(value, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+@:") == "" {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
