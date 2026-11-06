package daemon

import "github.com/zm2231/agenthail/internal/registry"

type workView struct {
	Path          string   `json:"path"`
	Branch        string   `json:"branch"`
	Base          string   `json:"base"`
	State         string   `json:"state"`
	SettledReason string   `json:"settledReason,omitempty"`
	Cleanup       string   `json:"cleanup,omitempty"`
	CleanupReason string   `json:"cleanupReason,omitempty"`
	KeptPaths     []string `json:"keptPaths,omitempty"`
}

func newWorkView(record registry.WorkRecord) *workView {
	return &workView{Path: record.WorktreePath, Branch: record.Branch, Base: record.BaseRef, State: record.State, SettledReason: record.SettledReason, Cleanup: record.Cleanup, CleanupReason: record.CleanupReason, KeptPaths: record.KeptPaths}
}
