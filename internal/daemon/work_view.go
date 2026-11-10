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
	return &workView{Path: record.WorktreePath, Branch: record.Branch, Base: record.BaseRef, State: record.State, SettledReason: record.SettledReason, Cleanup: record.Cleanup, CleanupReason: record.CleanupReason, KeptPaths: record.KeptPaths}
}
