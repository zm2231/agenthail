package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
)

// MovedCoordinator is the coordinator role a moving session holds. Since is
// when the session became the coordinator, which the move keeps.
type MovedCoordinator struct {
	ProjectID      string    `json:"projectId"`
	Name           string    `json:"name"`
	Surface        string    `json:"surface"`
	Model          string    `json:"model,omitempty"`
	PermissionMode string    `json:"permissionMode,omitempty"`
	Instructions   string    `json:"instructions,omitempty"`
	ChangedBy      string    `json:"changedBy,omitempty"`
	Since          time.Time `json:"since"`
}

func movedCoordinator(q catalogQuerier, conversation string) (*MovedCoordinator, error) {
	var lead MovedCoordinator
	var since sql.NullInt64
	err := q.QueryRow(`SELECT c.project_id,c.name,c.surface,c.model,c.permission_mode,c.instructions,c.changed_by,
		(SELECT MAX(h.at_ms) FROM coordinator_history h WHERE h.project_id=c.project_id AND h.session_id=a.session_id AND h.action IN ('set','replaced','moved'))
		FROM coordinators c JOIN aliases a ON a.name=c.name WHERE c.host_id='' AND a.session_id=?`, conversation).
		Scan(&lead.ProjectID, &lead.Name, &lead.Surface, &lead.Model, &lead.PermissionMode, &lead.Instructions, &lead.ChangedBy, &since)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if since.Valid {
		lead.Since = time.UnixMilli(since.Int64).UTC()
	}
	return &lead, nil
}

func sameCoordinatorCarry(a, b *MovedCoordinator) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// coordinatorMoveRefusal names why a coordinator cannot move: only a
// repository's project is the same project on another host.
func coordinatorMoveRefusal(q catalogQuerier, conversation string) (string, error) {
	lead, err := movedCoordinator(q, conversation)
	if err != nil || lead == nil {
		return "", err
	}
	if !strings.HasPrefix(lead.ProjectID, hosts.RepositoryPrefix) {
		return fmt.Sprintf("it coordinates %s, a project that is not a Git repository and so not the same project on another host", lead.ProjectID), nil
	}
	return "", nil
}

func (r *Registry) CheckMovedCoordinator(conversation string, lead *MovedCoordinator, landing string) error {
	if lead == nil {
		return nil
	}
	if lead.ProjectID == "" || lead.Name == "" || lead.Surface == "" {
		return fmt.Errorf("a moved coordinator needs its project, name and agent")
	}
	if landing != lead.ProjectID {
		return fmt.Errorf("the session coordinates %s, but lands in %s here", lead.ProjectID, landingProject(landing))
	}
	var name string
	err := r.read.QueryRow(`SELECT name FROM coordinators WHERE project_id=? AND host_id=''`, lead.ProjectID).Scan(&name)
	if err == nil {
		return fmt.Errorf("%s already has coordinator @%s here", lead.ProjectID, name)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var project string
	err = r.read.QueryRow(`SELECT project_id FROM coordinators WHERE name=? AND project_id!=?`, lead.Name, lead.ProjectID).Scan(&project)
	if err == nil {
		return fmt.Errorf("@%s already coordinates %s", lead.Name, project)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var holder string
	err = r.read.QueryRow(`SELECT session_id FROM aliases WHERE name=?`, lead.Name).Scan(&holder)
	if err == nil && holder != conversation {
		return fmt.Errorf("@%s already names session %s here", lead.Name, holder)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func landingProject(project string) string {
	if project == "" {
		return "no project"
	}
	return project
}

type MovedCoordinatorPlace struct {
	ProjectRoot string
	Cwd         string
}

func insertMovedCoordinatorTx(tx *sql.Tx, conversation, alias, from string, lead *MovedCoordinator, place MovedCoordinatorPlace, now time.Time) error {
	if lead == nil {
		return nil
	}
	if alias != lead.Name {
		return fmt.Errorf("coordinator @%s moves only with its name", lead.Name)
	}
	var holder string
	if err := tx.QueryRow(`SELECT session_id FROM aliases WHERE name=?`, lead.Name).Scan(&holder); err != nil || holder != conversation {
		return fmt.Errorf("@%s does not name %s here, so it cannot coordinate %s: %v", lead.Name, conversation, lead.ProjectID, err)
	}
	if _, err := tx.Exec(`INSERT INTO coordinators (project_id,host_id,project_root,name,surface,cwd,model,permission_mode,instructions,changed_by,changed_at_ms,moved_from) VALUES (?,'',?,?,?,?,?,?,?,?,?,?)`,
		lead.ProjectID, place.ProjectRoot, lead.Name, lead.Surface, place.Cwd, lead.Model, lead.PermissionMode, lead.Instructions, lead.ChangedBy, now.UnixMilli(), from); err != nil {
		return err
	}
	since := lead.Since
	if since.IsZero() {
		since = now
	}
	if err := recordCoordinatorHistoryTx(tx, lead.ProjectID, "moved", lead.Name, conversation, lead.Name, conversation, lead.ChangedBy, since); err != nil {
		return err
	}
	_, err := publishCoordinatorTx(tx, lead.ProjectID)
	return err
}

func dropMovedCoordinatorTx(tx *sql.Tx, conversation string, now time.Time) error {
	lead, err := movedCoordinator(tx, conversation)
	if err != nil || lead == nil {
		return err
	}
	for _, statement := range []string{`DELETE FROM coordinators WHERE project_id=? AND host_id=''`, `DELETE FROM coordinator_replaced WHERE project_id=?`} {
		if _, err := tx.Exec(statement, lead.ProjectID); err != nil {
			return err
		}
	}
	if err := recordCoordinatorHistoryTx(tx, lead.ProjectID, "moved", lead.Name, "", lead.Name, conversation, "", now); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"projectId": lead.ProjectID})
	_, _, err = appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("coordinator.removed:%s:%d", lead.ProjectID, now.UnixNano()), Type: "coordinator.removed", EntityID: lead.ProjectID, Payload: payload})
	return err
}

func (r *Registry) MovingOut(session string) (bool, error) {
	var moving bool
	err := r.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_moves WHERE conversation=? AND role='out' AND state IN ('preparing','sent'))`, session).Scan(&moving)
	return moving, err
}

func coordinatorChangeWhileMovingTx(tx *sql.Tx, projectID string) error {
	var session string
	err := tx.QueryRow(`SELECT a.session_id FROM coordinators c JOIN aliases a ON a.name=c.name WHERE c.project_id=? AND c.host_id=''`, projectID).Scan(&session)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	moving, err := movingOutTx(tx, session)
	if err != nil {
		return err
	}
	if moving {
		return fmt.Errorf("the coordinator's session %s is moving to another host; change the coordinator once the move has finished", session)
	}
	return nil
}

// oneCoordinatorPerConversation keeps one row for a coordinator that is
// moving between hosts, whose role both hosts list until the source lets it
// go. The row kept is the one whose host runs the conversation as this host
// sees it, else the source's. Rows of different conversations stay a
// conflict.
func (r *Registry) oneCoordinatorPerConversation(specs []CoordinatorSpec) ([]CoordinatorSpec, error) {
	if len(specs) < 2 {
		return specs, nil
	}
	byProject := map[string][]int{}
	for index, spec := range specs {
		byProject[spec.ProjectID] = append(byProject[spec.ProjectID], index)
	}
	identity, err := r.HostIdentity()
	if err != nil {
		return nil, err
	}
	drop := map[int]bool{}
	for _, indexes := range byProject {
		if len(indexes) < 2 {
			continue
		}
		group := make([]CoordinatorSpec, 0, len(indexes))
		for _, index := range indexes {
			group = append(group, specs[index])
		}
		keep, err := r.movingCoordinator(group, identity.ID)
		if err != nil {
			return nil, err
		}
		if keep < 0 {
			continue
		}
		for position, index := range indexes {
			if position != keep {
				drop[index] = true
			}
		}
	}
	kept := make([]CoordinatorSpec, 0, len(specs)-len(drop))
	for index, spec := range specs {
		if !drop[index] {
			kept = append(kept, spec)
		}
	}
	return kept, nil
}

func (r *Registry) movingCoordinator(group []CoordinatorSpec, self string) (int, error) {
	conversation := ""
	for _, spec := range group {
		raw := coordinatorConversation(spec)
		if raw == "" || conversation != "" && raw != conversation {
			return -1, nil
		}
		conversation = raw
	}
	owner, err := r.SessionHost(conversation)
	switch {
	case err == nil:
		running := -1
		for index, spec := range group {
			if spec.HostID == owner {
				if running >= 0 {
					return -1, nil
				}
				running = index
			}
		}
		if running >= 0 {
			return running, nil
		}
	case !errors.Is(err, sql.ErrNoRows):
		return -1, err
	}
	source := -1
	for index, spec := range group {
		host := spec.HostID
		if host == "" {
			host = self
		}
		for _, other := range group {
			if other.MovedFrom != host {
				continue
			}
			if source < 0 || spec.ChangedAt.Before(group[source].ChangedAt) {
				source = index
			}
		}
	}
	return source, nil
}

func coordinatorConversation(spec CoordinatorSpec) string {
	host, target, ok := hosts.Split(spec.SessionID)
	if !ok {
		return spec.SessionID
	}
	if host != spec.HostID {
		return ""
	}
	return target
}

func (r *Registry) CoordinatorMovedTo(name string) (string, error) {
	var action, conversation string
	err := r.read.QueryRow(`SELECT action,previous_session_id FROM coordinator_history WHERE name=? ORDER BY at_ms DESC, id DESC LIMIT 1`, name).Scan(&action, &conversation)
	if errors.Is(err, sql.ErrNoRows) || err == nil && action != "moved" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return r.MovedTo(conversation)
}
