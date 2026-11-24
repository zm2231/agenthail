package daemon

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/surface"
)

// forwardedScope checks an agent's action that a paired host sent here for a
// session in the agent's own project. The sending host admitted it from its
// mirror; this host knows the session's project, whether it is archived and
// its permission rank, so it decides again. It also knows whether the caller
// started or leads the session, so it picks which of the caller's two
// decisions applies and returns it.
func (d *Daemon) forwardedScope(session surface.Session, request dashboardActionRequest) (string, error) {
	if request.ScopeProjectID == "" || surface.IsAgentIdentity(&session) {
		return "", nil
	}
	if err := d.forwardedLimits(session, request); err != nil {
		return "", err
	}
	decision := request.ScopeControl
	if decision == "" {
		return "", nil
	}
	if d.oversees(request.SourceSessionID, session) {
		decision = request.ScopeOverseen
	}
	if decision == agentroles.Ask {
		return "", scopeFailure("not_queued", fmt.Sprintf("the caller's role leaves this action on %s to the person", session.ID))
	}
	return decision, nil
}

func (d *Daemon) forwardedLimits(session surface.Session, request dashboardActionRequest) error {
	if project := d.resolveProject(session); project != request.ScopeProjectID {
		return scopeFailure("different_project", fmt.Sprintf("%s is not in the caller's project %s; across hosts an agent reaches only sessions in its own project", session.ID, request.ScopeProjectID))
	}
	if request.Action != "session-unarchive" {
		archived, err := d.Registry.SessionArchived(session.ID)
		if err != nil {
			return err
		}
		if archived {
			return scopeFailure("archived", fmt.Sprintf("%s is archived", session.ID))
		}
	}
	if request.ScopeRank == "" {
		return nil
	}
	raw, err := d.Registry.SessionCeiling(session.ID)
	if err != nil {
		return err
	}
	target, err := parseCeiling(raw)
	if err != nil {
		return err
	}
	if !target.within(sessionCeiling{Permission: request.ScopeRank}) {
		return scopeFailure("exceeds_ceiling", fmt.Sprintf("%s may do more than the caller may (%s), so the caller cannot control it", session.ID, request.ScopeRank))
	}
	if request.Action == "permission-mode" {
		rank, ok := claudePermissionRank[strings.TrimSpace(request.PermissionMode)]
		if !ok || !(sessionCeiling{Permission: rank}).within(sessionCeiling{Permission: target.Permission}) {
			return scopeFailure("exceeds_ceiling", fmt.Sprintf("%s is above %s's ceiling of %s", request.PermissionMode, session.ID, target.Permission))
		}
	}
	return nil
}

func scopeFailure(rule, message string) error {
	return &actionError{Status: http.StatusForbidden, Code: "out_of_scope", Message: message, Extra: map[string]any{"detail": map[string]any{"rule": rule}}}
}
