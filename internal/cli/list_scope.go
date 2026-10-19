package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type listProject struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	RootPath    string `json:"rootPath"`
	Kind        string `json:"kind"`
	Archived    bool   `json:"archived,omitempty"`
}

type listCatalogView struct {
	Project   *listProject            `json:"project,omitempty"`
	Subagents *surface.SubagentRollup `json:"subagents,omitempty"`
	Current   bool                    `json:"current"`
	Recent    bool                    `json:"recent"`
	Archived  bool                    `json:"archived,omitempty"`
}

func catalogListView(record registry.CatalogSessionState) listCatalogView {
	var view listCatalogView
	_ = json.Unmarshal([]byte(record.ProjectionFingerprint), &view)
	return view
}

func listScopeFromArgs(args []string) (string, error) {
	scope := ""
	for _, candidate := range []string{"recent", "all", "archived"} {
		if !hasFlag(args, "--"+candidate) {
			continue
		}
		if scope != "" {
			return "", fmt.Errorf("choose one of --recent, --all or --archived")
		}
		scope = candidate
	}
	if scope == "" {
		scope = "current"
	}
	return scope, nil
}

func listProjectMatches(project *listProject, selector string) bool {
	return project != nil && (project.ID == selector || strings.EqualFold(project.DisplayName, selector))
}

// filterListScope keeps the session families with a member in scope and, when
// selector is set, in that project. A subagent belongs to its root's family
// when the root is listed.
func filterListScope(sessions []surface.Session, views map[string]listCatalogView, scope, selector string) []surface.Session {
	listed := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		listed[session.ID] = true
	}
	family := func(session surface.Session) string {
		if session.Subagent != nil && session.Subagent.RootID != "" && listed[session.Subagent.RootID] {
			return session.Subagent.RootID
		}
		return session.ID
	}
	matched := map[string]bool{}
	for _, session := range sessions {
		view := views[session.ID]
		if selector != "" && !listProjectMatches(view.Project, selector) {
			continue
		}
		if daemon.ScopeMatches(scope, daemon.SessionWorking(session.Status, view.Subagents), view.Current, view.Recent, view.Archived) {
			matched[family(session)] = true
		}
	}
	kept := sessions[:0]
	for _, session := range sessions {
		if matched[family(session)] {
			kept = append(kept, session)
		}
	}
	return kept
}

func listProjectColumn(view listCatalogView) string {
	if view.Project == nil {
		return "-"
	}
	return view.Project.DisplayName
}
