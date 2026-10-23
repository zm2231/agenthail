package registry

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

var ErrNoCoordinator = errors.New("this project has no coordinator")

// ErrCoordinatorChanged refuses a clear whose caller saw an older version of
// the coordinator than the one stored.
var ErrCoordinatorChanged = errors.New("the coordinator changed since it was read")

// LeadAlias is not a session's name: "@lead" addresses the coordinator of the
// sender's project.
const LeadAlias = "lead"

var ErrReservedAlias = fmt.Errorf("@%s is reserved for the coordinator of the sender's project", LeadAlias)

// CoordinatorSpec is a project's coordinator. A row with an empty HostID
// belongs to this host, which starts its conversation again from the spec
// when it cannot be resumed; its session is whichever one its name points at,
// so the address follows rekeys and restarts. A row with a HostID is a
// read-only mirror of a paired host's coordinator.
type CoordinatorSpec struct {
	ProjectID      string    `json:"projectId"`
	HostID         string    `json:"hostId,omitempty"`
	ProjectRoot    string    `json:"projectRoot"`
	Name           string    `json:"name"`
	Surface        string    `json:"surface"`
	Cwd            string    `json:"cwd,omitempty"`
	Model          string    `json:"model,omitempty"`
	PermissionMode string    `json:"permissionMode,omitempty"`
	Instructions   string    `json:"instructions,omitempty"`
	SessionID      string    `json:"sessionId,omitempty"`
	ChangedBy      string    `json:"changedBy,omitempty"`
	ChangedAt      time.Time `json:"changedAt"`
}

// CoordinatorEntry is what a host publishes about its own coordinators to
// its paired hosts.
type CoordinatorEntry struct {
	ProjectID   string    `json:"projectId"`
	ProjectRoot string    `json:"projectRoot"`
	Name        string    `json:"name"`
	Surface     string    `json:"surface"`
	SessionID   string    `json:"sessionId,omitempty"`
	ChangedBy   string    `json:"changedBy,omitempty"`
	ChangedAt   time.Time `json:"changedAt"`
}

func (spec CoordinatorSpec) Entry() CoordinatorEntry {
	return CoordinatorEntry{ProjectID: spec.ProjectID, ProjectRoot: spec.ProjectRoot, Name: spec.Name, Surface: spec.Surface, SessionID: spec.SessionID, ChangedBy: spec.ChangedBy, ChangedAt: spec.ChangedAt}
}

// CoordinatorChange is who changes a coordinator, and how. SessionID names
// the conversation that becomes the coordinator, "" for one the first message
// starts. Without Replace a project that already has a coordinator on any
// host is refused. OverPeers records it while paired hosts' coordinators of
// the project remain, for a replace that asks their owners to clear them
// next.
type CoordinatorChange struct {
	SessionID string
	Replace   bool
	OverPeers bool
	ChangedBy string
}

// CoordinatorExistsError refuses to take over a project that already has a
// coordinator. Existing lists every host's coordinator of the project.
type CoordinatorExistsError struct {
	Existing []CoordinatorSpec
}

func (e CoordinatorExistsError) Error() string {
	names := make([]string, 0, len(e.Existing))
	for _, spec := range e.Existing {
		names = append(names, "@"+spec.Name)
	}
	return fmt.Sprintf("the project already has coordinator %s; replace it to take over", strings.Join(names, " and "))
}

// CoordinatorAliasError refuses to rename, unname or reuse a name that is
// this host's coordinator's: the coordinator is whichever session the name
// points at.
type CoordinatorAliasError struct {
	Name string
}

func (e CoordinatorAliasError) Error() string {
	return fmt.Sprintf("@%s is a project coordinator's name; change it with 'agenthail coordinator set --replace' or 'agenthail coordinator clear'", e.Name)
}

func coordinatorNamedTx(tx *sql.Tx, name string) (bool, error) {
	var found bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM coordinators WHERE host_id='' AND name=?)`, name).Scan(&found)
	return found, err
}

type CoordinatorNameTakenError struct {
	Name        string
	HostID      string
	ProjectRoot string
}

func (e CoordinatorNameTakenError) Error() string {
	return fmt.Sprintf("@%s already coordinates %s", e.Name, e.ProjectRoot)
}

// A restart holds this lock so concurrent senders in any Agenthail process
// start at most one conversation for the project. A project id can hold any
// character, so the file is named by its hash.
func (r *Registry) CoordinatorLockPath(projectID string) string {
	sum := sha256.Sum256([]byte(projectID))
	return filepath.Join(filepath.Dir(r.path), "coordinator-"+hex.EncodeToString(sum[:12])+".lock")
}

const coordinatorColumns = `c.project_id,c.host_id,c.project_root,c.name,c.surface,c.cwd,c.model,c.permission_mode,c.instructions,
	CASE WHEN c.host_id='' THEN COALESCE((SELECT a.session_id FROM aliases a WHERE a.name=c.name),'') ELSE c.session_id END,
	c.changed_by,c.changed_at_ms`

func scanCoordinator(row interface{ Scan(...any) error }) (CoordinatorSpec, error) {
	var spec CoordinatorSpec
	var changedAt int64
	if err := row.Scan(&spec.ProjectID, &spec.HostID, &spec.ProjectRoot, &spec.Name, &spec.Surface, &spec.Cwd, &spec.Model, &spec.PermissionMode, &spec.Instructions, &spec.SessionID, &spec.ChangedBy, &changedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return spec, ErrNoCoordinator
		}
		return spec, err
	}
	spec.ChangedAt = time.UnixMilli(changedAt).UTC()
	return spec, nil
}

func (r *Registry) queryCoordinators(q interface {
	Query(query string, args ...any) (*sql.Rows, error)
}, where string, args ...any) ([]CoordinatorSpec, error) {
	rows, err := q.Query(`SELECT `+coordinatorColumns+` FROM coordinators c WHERE `+where+` ORDER BY c.name,c.host_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	specs := []CoordinatorSpec{}
	for rows.Next() {
		spec, err := scanCoordinator(rows)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, rows.Err()
}

// Coordinator returns this host's coordinator of a project.
func (r *Registry) Coordinator(projectID string) (CoordinatorSpec, error) {
	return scanCoordinator(r.read.QueryRow(`SELECT `+coordinatorColumns+` FROM coordinators c WHERE c.project_id=? AND c.host_id=''`, projectID))
}

// CoordinatorNamed returns this host's coordinator with that name.
func (r *Registry) CoordinatorNamed(name string) (CoordinatorSpec, error) {
	return scanCoordinator(r.read.QueryRow(`SELECT `+coordinatorColumns+` FROM coordinators c WHERE c.name=? AND c.host_id=''`, name))
}

// Coordinators lists every coordinator this host knows, its own and its
// paired hosts'.
func (r *Registry) Coordinators() ([]CoordinatorSpec, error) {
	return r.queryCoordinators(r.read, `1`)
}

func (r *Registry) LocalCoordinators() ([]CoordinatorSpec, error) {
	return r.queryCoordinators(r.read, `c.host_id=''`)
}

// ProjectCoordinators lists every host's coordinator of a project. More
// than one is a conflict that a replace resolves.
func (r *Registry) ProjectCoordinators(projectID string) ([]CoordinatorSpec, error) {
	return r.queryCoordinators(r.read, `c.project_id=?`, projectID)
}

// CoordinatorsNamed lists every host's coordinator with that name.
func (r *Registry) CoordinatorsNamed(name string) ([]CoordinatorSpec, error) {
	return r.queryCoordinators(r.read, `c.name=?`, name)
}

func (r *Registry) checkCoordinatorTx(tx *sql.Tx, spec CoordinatorSpec, change CoordinatorChange, peersCleared bool) (*CoordinatorSpec, error) {
	if spec.ProjectID == "" || spec.ProjectRoot == "" || spec.Name == "" || spec.Surface == "" || spec.Cwd == "" {
		return nil, fmt.Errorf("a coordinator needs a project, a name, an agent and a working directory")
	}
	if strings.EqualFold(spec.Name, LeadAlias) {
		return nil, ErrReservedAlias
	}
	existing, err := r.queryCoordinators(tx, `c.project_id=?`, spec.ProjectID)
	if err != nil {
		return nil, err
	}
	var previous *CoordinatorSpec
	for index := range existing {
		if !change.Replace || (existing[index].HostID != "" && !peersCleared) {
			return nil, CoordinatorExistsError{Existing: existing}
		}
		if existing[index].HostID == "" {
			previous = &existing[index]
		}
	}
	var takenHost, takenRoot string
	err = tx.QueryRow(`SELECT host_id,project_root FROM coordinators WHERE name=? AND NOT (project_id=? AND (host_id='' OR ?))`, spec.Name, spec.ProjectID, peersCleared).Scan(&takenHost, &takenRoot)
	if err == nil {
		return nil, CoordinatorNameTakenError{Name: spec.Name, HostID: takenHost, ProjectRoot: takenRoot}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var owner string
	err = tx.QueryRow(`SELECT session_id FROM aliases WHERE name=?`, spec.Name).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	freed := previous != nil && previous.Name == spec.Name
	if owner != "" && owner != change.SessionID && !freed {
		return nil, AliasTakenError{Name: spec.Name, Owner: owner}
	}
	if !freed {
		if err := aliasReservedTx(tx, spec.Name, "", r.now()); err != nil {
			return nil, err
		}
	}
	return previous, nil
}

// SetCoordinator makes spec this host's coordinator of its project. It
// refuses a project that has a coordinator on any host unless change.Replace
// is set, and a paired host's coordinator unless change.OverPeers is set too. A name any host's other coordinator uses, or another session's
// name, is refused.
func (r *Registry) SetCoordinator(spec CoordinatorSpec, change CoordinatorChange) (CoordinatorSpec, error) {
	stored, _, err := r.ReplaceCoordinator(spec, change)
	return stored, err
}

// CoordinatorUndo is what a coordinator change replaced here: the previous
// coordinator row, the names it moved, and the replaced conversations it
// forgot. WithdrawCoordinator puts them back.
type CoordinatorUndo struct {
	stored       CoordinatorSpec
	previous     *CoordinatorSpec
	aliases      map[string]string
	replacedRows map[string]string
}

// ReplaceCoordinator is SetCoordinator that also returns what the change
// replaced, for a replace that may have to withdraw it.
func (r *Registry) ReplaceCoordinator(spec CoordinatorSpec, change CoordinatorChange) (CoordinatorSpec, CoordinatorUndo, error) {
	undo := CoordinatorUndo{aliases: map[string]string{}, replacedRows: map[string]string{}}
	tx, err := r.db.Begin()
	if err != nil {
		return spec, undo, err
	}
	defer tx.Rollback()
	previous, err := r.checkCoordinatorTx(tx, spec, change, change.Replace && change.OverPeers)
	if err != nil {
		return spec, undo, err
	}
	if previous != nil {
		row, err := scanCoordinator(tx.QueryRow(`SELECT `+coordinatorColumns+` FROM coordinators c WHERE c.project_id=? AND c.host_id=''`, spec.ProjectID))
		if err != nil {
			return spec, undo, err
		}
		undo.previous = &row
		if row.SessionID != "" {
			undo.aliases[row.Name] = row.SessionID
		}
	}
	if change.SessionID != "" {
		var prior string
		err := tx.QueryRow(`SELECT name FROM aliases WHERE session_id=?`, change.SessionID).Scan(&prior)
		if err == nil {
			undo.aliases[prior] = change.SessionID
		} else if !errors.Is(err, sql.ErrNoRows) {
			return spec, undo, err
		}
	}
	rows, err := tx.Query(`SELECT session_id,project_id FROM coordinator_replaced WHERE project_id=? OR session_id=?`, spec.ProjectID, change.SessionID)
	if err != nil {
		return spec, undo, err
	}
	for rows.Next() {
		var sessionID, projectID string
		if err := rows.Scan(&sessionID, &projectID); err != nil {
			rows.Close()
			return spec, undo, err
		}
		undo.replacedRows[sessionID] = projectID
	}
	if err := rows.Close(); err != nil {
		return spec, undo, err
	}
	if previous != nil && previous.Name == spec.Name && previous.SessionID != change.SessionID {
		if _, err := tx.Exec(`DELETE FROM aliases WHERE name=?`, spec.Name); err != nil {
			return spec, undo, err
		}
	}
	if change.SessionID != "" {
		if err := bindAliasTx(tx, spec.Name, change.SessionID); err != nil {
			return spec, undo, err
		}
	}
	if previous != nil && previous.SessionID != change.SessionID {
		if _, err := tx.Exec(`DELETE FROM coordinator_replaced WHERE project_id=?`, spec.ProjectID); err != nil {
			return spec, undo, err
		}
	}
	if change.SessionID != "" {
		if _, err := tx.Exec(`DELETE FROM coordinator_replaced WHERE session_id=?`, change.SessionID); err != nil {
			return spec, undo, err
		}
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(`INSERT INTO coordinators (project_id,host_id,project_root,name,surface,cwd,model,permission_mode,instructions,changed_by,changed_at_ms) VALUES (?,'',?,?,?,?,?,?,?,?,?)
		ON CONFLICT(project_id,host_id) DO UPDATE SET project_root=excluded.project_root,name=excluded.name,surface=excluded.surface,cwd=excluded.cwd,model=excluded.model,permission_mode=excluded.permission_mode,instructions=excluded.instructions,changed_by=excluded.changed_by,changed_at_ms=excluded.changed_at_ms`,
		spec.ProjectID, spec.ProjectRoot, spec.Name, spec.Surface, spec.Cwd, spec.Model, spec.PermissionMode, spec.Instructions, change.ChangedBy, now.UnixMilli()); err != nil {
		return spec, undo, err
	}
	action, previousName, previousSession := "set", "", ""
	if previous != nil {
		action, previousName, previousSession = "replaced", previous.Name, previous.SessionID
	}
	if err := recordCoordinatorHistoryTx(tx, spec.ProjectID, action, spec.Name, change.SessionID, previousName, previousSession, change.ChangedBy, now); err != nil {
		return spec, undo, err
	}
	stored, err := publishCoordinatorTx(tx, spec.ProjectID)
	if err != nil {
		return spec, undo, err
	}
	undo.stored = stored
	return stored, undo, tx.Commit()
}

// WithdrawCoordinator undoes a ReplaceCoordinator in one transaction: it
// removes the coordinator that change recorded and its name, and restores the
// previous coordinator, names and replaced conversations exactly. If anything
// it would restore changed since, it changes nothing and returns an error, so
// the recorded coordinator stays rather than the project losing both.
func (r *Registry) WithdrawCoordinator(undo CoordinatorUndo, changedBy string) error {
	stored := undo.stored
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := scanCoordinator(tx.QueryRow(`SELECT `+coordinatorColumns+` FROM coordinators c WHERE c.project_id=? AND c.host_id=''`, stored.ProjectID))
	if err != nil {
		return err
	}
	if current.ChangedAt.UnixMilli() != stored.ChangedAt.UnixMilli() || current.Name != stored.Name || current.SessionID != stored.SessionID {
		return ErrCoordinatorChanged
	}
	if _, err := tx.Exec(`DELETE FROM coordinators WHERE project_id=? AND host_id=''`, stored.ProjectID); err != nil {
		return err
	}
	if stored.SessionID != "" {
		if _, err := tx.Exec(`DELETE FROM aliases WHERE name=? AND session_id=?`, stored.Name, stored.SessionID); err != nil {
			return err
		}
	}
	for name, sessionID := range undo.aliases {
		var owner string
		err := tx.QueryRow(`SELECT session_id FROM aliases WHERE name=?`, name).Scan(&owner)
		if err == nil && owner != sessionID {
			return fmt.Errorf("@%s now names session %s: %w", name, owner, ErrCoordinatorChanged)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var other string
		err = tx.QueryRow(`SELECT name FROM aliases WHERE session_id=? AND name<>?`, sessionID, name).Scan(&other)
		if err == nil {
			return fmt.Errorf("session %s is now @%s: %w", sessionID, other, ErrCoordinatorChanged)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO aliases (name,session_id) VALUES (?,?)`, name, sessionID); err != nil {
			return err
		}
	}
	for sessionID, projectID := range undo.replacedRows {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO coordinator_replaced (session_id,project_id) SELECT ?,? WHERE EXISTS (SELECT 1 FROM sessions WHERE id=?)`, sessionID, projectID, sessionID); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	if err := recordCoordinatorHistoryTx(tx, stored.ProjectID, "cleared", stored.Name, "", stored.Name, stored.SessionID, changedBy, now); err != nil {
		return err
	}
	if previous := undo.previous; previous != nil {
		if _, err := tx.Exec(`INSERT INTO coordinators (project_id,host_id,project_root,name,surface,cwd,model,permission_mode,instructions,changed_by,changed_at_ms) VALUES (?,'',?,?,?,?,?,?,?,?,?)`,
			previous.ProjectID, previous.ProjectRoot, previous.Name, previous.Surface, previous.Cwd, previous.Model, previous.PermissionMode, previous.Instructions, previous.ChangedBy, previous.ChangedAt.UnixMilli()); err != nil {
			return err
		}
		if err := recordCoordinatorHistoryTx(tx, stored.ProjectID, "set", previous.Name, previous.SessionID, stored.Name, stored.SessionID, changedBy, now); err != nil {
			return err
		}
		if _, err := publishCoordinatorTx(tx, stored.ProjectID); err != nil {
			return err
		}
		return tx.Commit()
	}
	payload, _ := json.Marshal(map[string]string{"projectId": stored.ProjectID})
	if _, _, err := appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("coordinator.removed:%s:%d", stored.ProjectID, now.UnixNano()), Type: "coordinator.removed", EntityID: stored.ProjectID, Payload: payload}); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearCoordinator removes this host's coordinator of a project. Its
// conversation and name stay. A non-zero expected changed time refuses a
// coordinator that changed since the caller read it.
func (r *Registry) ClearCoordinator(projectID, changedBy string, expected time.Time) (CoordinatorSpec, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return CoordinatorSpec{}, err
	}
	defer tx.Rollback()
	previous, err := scanCoordinator(tx.QueryRow(`SELECT `+coordinatorColumns+` FROM coordinators c WHERE c.project_id=? AND c.host_id=''`, projectID))
	if err != nil {
		return previous, err
	}
	if !expected.IsZero() && previous.ChangedAt.UnixMilli() != expected.UnixMilli() {
		return previous, ErrCoordinatorChanged
	}
	for _, statement := range []string{`DELETE FROM coordinators WHERE project_id=? AND host_id=''`, `DELETE FROM coordinator_replaced WHERE project_id=?`} {
		if _, err := tx.Exec(statement, projectID); err != nil {
			return previous, err
		}
	}
	if err := recordCoordinatorHistoryTx(tx, projectID, "cleared", previous.Name, "", previous.Name, previous.SessionID, changedBy, time.Now().UTC()); err != nil {
		return previous, err
	}
	payload, _ := json.Marshal(map[string]string{"projectId": projectID})
	if _, _, err := appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("coordinator.removed:%s:%d", projectID, time.Now().UnixNano()), Type: "coordinator.removed", EntityID: projectID, Payload: payload}); err != nil {
		return previous, err
	}
	return previous, tx.Commit()
}

// CoordinatorHistoryEntry is one set, replace or clear of a coordinator.
type CoordinatorHistoryEntry struct {
	ProjectID         string    `json:"projectId"`
	Action            string    `json:"action"`
	Name              string    `json:"name"`
	SessionID         string    `json:"sessionId,omitempty"`
	PreviousName      string    `json:"previousName,omitempty"`
	PreviousSessionID string    `json:"previousSessionId,omitempty"`
	ChangedBy         string    `json:"changedBy,omitempty"`
	At                time.Time `json:"at"`
}

func (r *Registry) CoordinatorHistory(projectID string) ([]CoordinatorHistoryEntry, error) {
	rows, err := r.read.Query(`SELECT project_id,action,name,session_id,previous_name,previous_session_id,changed_by,at_ms FROM coordinator_history WHERE project_id=? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []CoordinatorHistoryEntry{}
	for rows.Next() {
		var entry CoordinatorHistoryEntry
		var at int64
		if err := rows.Scan(&entry.ProjectID, &entry.Action, &entry.Name, &entry.SessionID, &entry.PreviousName, &entry.PreviousSessionID, &entry.ChangedBy, &at); err != nil {
			return nil, err
		}
		entry.At = time.UnixMilli(at).UTC()
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func recordCoordinatorHistoryTx(tx *sql.Tx, projectID, action, name, sessionID, previousName, previousSession, changedBy string, at time.Time) error {
	_, err := tx.Exec(`INSERT INTO coordinator_history (project_id,action,name,session_id,previous_name,previous_session_id,changed_by,at_ms) VALUES (?,?,?,?,?,?,?,?)`,
		projectID, action, name, sessionID, previousName, previousSession, changedBy, at.UnixMilli())
	return err
}

// publishCoordinatorTx appends this host's current coordinator of a project
// to the catalog, for its paired hosts' mirrors.
func publishCoordinatorTx(tx *sql.Tx, projectID string) (CoordinatorSpec, error) {
	spec, err := scanCoordinator(tx.QueryRow(`SELECT `+coordinatorColumns+` FROM coordinators c WHERE c.project_id=? AND c.host_id=''`, projectID))
	if err != nil {
		return spec, err
	}
	payload, err := json.Marshal(spec.Entry())
	if err != nil {
		return spec, err
	}
	_, _, err = appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("coordinator.changed:%s:%d:%s", projectID, spec.ChangedAt.UnixMilli(), spec.SessionID), Type: "coordinator.changed", EntityID: projectID, Payload: payload})
	return spec, err
}

// publishSessionCoordinatorTx republishes the coordinator whose name points
// at sessionID, after that session's id changed.
func publishSessionCoordinatorTx(tx *sql.Tx, sessionID string) error {
	var projectID string
	err := tx.QueryRow(`SELECT c.project_id FROM coordinators c JOIN aliases a ON a.name=c.name WHERE c.host_id='' AND a.session_id=?`, sessionID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = publishCoordinatorTx(tx, projectID)
	return err
}

// ReplaceHostCoordinators makes entries the whole mirror of a paired host's
// coordinators. Each entry's project id is already as this host names it.
func (r *Registry) ReplaceHostCoordinators(hostID string, entries []CoordinatorEntry) error {
	if hostID == "" {
		return fmt.Errorf("a mirrored coordinator needs its host")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM coordinators WHERE host_id=?`, hostID); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := putHostCoordinatorTx(tx, hostID, entry); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Registry) PutHostCoordinator(hostID string, entry CoordinatorEntry) error {
	if hostID == "" {
		return fmt.Errorf("a mirrored coordinator needs its host")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM coordinators WHERE host_id=? AND (project_id=? OR name=?)`, hostID, entry.ProjectID, entry.Name); err != nil {
		return err
	}
	if err := putHostCoordinatorTx(tx, hostID, entry); err != nil {
		return err
	}
	return tx.Commit()
}

func putHostCoordinatorTx(tx *sql.Tx, hostID string, entry CoordinatorEntry) error {
	if entry.ProjectID == "" || entry.Name == "" {
		return fmt.Errorf("a mirrored coordinator needs a project and a name")
	}
	_, err := tx.Exec(`INSERT INTO coordinators (project_id,host_id,project_root,name,surface,session_id,changed_by,changed_at_ms) VALUES (?,?,?,?,?,?,?,?)`,
		entry.ProjectID, hostID, entry.ProjectRoot, entry.Name, entry.Surface, entry.SessionID, entry.ChangedBy, entry.ChangedAt.UnixMilli())
	return err
}

func (r *Registry) RemoveHostCoordinator(hostID, projectID string) error {
	if hostID == "" {
		return fmt.Errorf("a mirrored coordinator needs its host")
	}
	_, err := r.db.Exec(`DELETE FROM coordinators WHERE host_id=? AND project_id=?`, hostID, projectID)
	return err
}

// CoordinatorOfSession returns the spec of the project sessionID coordinates,
// as its current conversation or one a restart replaced, which senders such as
// a notification reply may still address. Being a coordinator now outranks
// having been one.
func (r *Registry) CoordinatorOfSession(sessionID string) (CoordinatorSpec, bool, error) {
	if sessionID == "" {
		return CoordinatorSpec{}, false, nil
	}
	for _, owner := range []string{
		`SELECT ` + coordinatorColumns + ` FROM coordinators c WHERE c.host_id='' AND c.name IN (SELECT name FROM aliases WHERE session_id=?)`,
		`SELECT ` + coordinatorColumns + ` FROM coordinators c WHERE c.host_id='' AND c.project_id IN (SELECT project_id FROM coordinator_replaced WHERE session_id=?)`,
	} {
		spec, err := scanCoordinator(r.read.QueryRow(owner, sessionID))
		if errors.Is(err, ErrNoCoordinator) {
			continue
		}
		return spec, err == nil, err
	}
	return CoordinatorSpec{}, false, nil
}

func (r *Registry) CoordinatorSessionID(spec CoordinatorSpec) (string, error) {
	id, err := r.LookupAlias(spec.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

var ErrCoordinatorMoved = errors.New("the coordinator was moved to another conversation")

// HandOverCoordinator points the coordinator's name at next. It also moves
// what the previous conversation can no longer use: messages still pending
// for it, its channel memberships and its relay routes, and remembers it was
// replaced.
func (r *Registry) HandOverCoordinator(spec CoordinatorSpec, previous, next string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	err = tx.QueryRow(`SELECT session_id FROM aliases WHERE name=?`, spec.Name).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if owner != previous {
		return ErrCoordinatorMoved
	}
	if _, err := tx.Exec(`DELETE FROM aliases WHERE name=? OR session_id=?`, spec.Name, next); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM coordinator_replaced WHERE session_id=?`, next); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO aliases (name,session_id) VALUES (?,?)`, spec.Name, next); err != nil {
		return err
	}
	if previous != "" {
		for _, statement := range append([]string{
			`UPDATE delivery_intents SET target_session_id=?2,updated_at=datetime('now') WHERE queue_id IN (SELECT id FROM message_queue WHERE session_id=?1 AND status='pending')`,
			`UPDATE message_queue SET session_id=?2,updated_at=datetime('now') WHERE session_id=?1 AND status='pending'`,
			`INSERT OR IGNORE INTO channel_members (channel_id,session_id) SELECT channel_id,?2 FROM channel_members WHERE session_id=?1`,
			`DELETE FROM channel_members WHERE session_id=?1`,
		}, routeMoves...) {
			if _, err := tx.Exec(statement, previous, next); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO coordinator_replaced (session_id,project_id) VALUES (?,?)`, previous, spec.ProjectID); err != nil {
			return err
		}
	}
	if _, err := publishCoordinatorTx(tx, spec.ProjectID); err != nil && !errors.Is(err, ErrNoCoordinator) {
		return err
	}
	return tx.Commit()
}

// MoveClaimedMessage returns a claimed queue item to the queue for sessionID,
// with its delivery intent, for a message whose target was replaced while it
// was in flight.
func (r *Registry) MoveClaimedMessage(id int64, sessionID string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE message_queue SET session_id=?,status='pending',attempts=MAX(attempts-1,0),available_at_ms=0,inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, sessionID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not inflight", id)
	}
	if _, err := tx.Exec(`UPDATE delivery_intents SET target_session_id=?,updated_at=datetime('now') WHERE queue_id=?`, sessionID, id); err != nil {
		return err
	}
	return tx.Commit()
}
