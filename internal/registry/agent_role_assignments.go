package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	AssignSession  = "session"
	AssignIdentity = "identity"
	AssignAlias    = "alias"
)

// AgentRoleAssignment gives the sessions it names a role in place of the one
// they hold by what they are. Paired hosts share every record the way they
// share roles: the later change wins, and a removal stays as a record.
type AgentRoleAssignment struct {
	Kind       string    `json:"kind"`
	Target     string    `json:"target"`
	Role       string    `json:"role"`
	Deleted    bool      `json:"deleted,omitempty"`
	ChangedAt  time.Time `json:"changedAt"`
	OriginHost string    `json:"originHost"`
}

func (a AgentRoleAssignment) newerThan(other AgentRoleAssignment) bool {
	if !a.ChangedAt.Equal(other.ChangedAt) {
		return a.ChangedAt.After(other.ChangedAt)
	}
	return a.OriginHost > other.OriginHost
}

func validAssignmentKind(kind string) bool {
	return kind == AssignSession || kind == AssignIdentity || kind == AssignAlias
}

// AgentRoleAssignments reads on the callers' reader, as AgentRoles does.
func (r *Registry) AgentRoleAssignments() ([]AgentRoleAssignment, error) {
	rows, err := r.callers.Query(`SELECT kind,target,role,deleted,changed_at_ms,origin_host FROM agent_role_assignments ORDER BY kind,target`)
	if err != nil {
		return nil, err
	}
	return appendRows([]AgentRoleAssignment{}, rows, scanAgentRoleAssignment)
}

func (r *Registry) PutAgentRoleAssignment(kind, target, role string, deleted bool) (AgentRoleAssignment, error) {
	if !validAssignmentKind(kind) || target == "" {
		return AgentRoleAssignment{}, fmt.Errorf("an assignment names a session, an identity or an alias")
	}
	if deleted {
		role = ""
	} else if role == "" {
		return AgentRoleAssignment{}, errors.New("an assignment needs a role")
	}
	identity, err := r.HostIdentity()
	if err != nil {
		return AgentRoleAssignment{}, fmt.Errorf("read this host's identity: %w", err)
	}
	record := AgentRoleAssignment{Kind: kind, Target: target, Role: role, Deleted: deleted, ChangedAt: r.now().Truncate(time.Millisecond), OriginHost: identity.ID}
	tx, err := r.db.Begin()
	if err != nil {
		return AgentRoleAssignment{}, err
	}
	defer tx.Rollback()
	if current, found, err := agentRoleAssignmentTx(tx, kind, target); err != nil {
		return AgentRoleAssignment{}, err
	} else if found && !record.ChangedAt.After(current.ChangedAt) {
		record.ChangedAt = current.ChangedAt.Add(time.Millisecond)
	}
	if err := putAgentRoleAssignmentTx(tx, record); err != nil {
		return AgentRoleAssignment{}, err
	}
	return record, tx.Commit()
}

func (r *Registry) AdoptAgentRoleAssignments(records []AgentRoleAssignment) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed := false
	for _, record := range records {
		if !validAssignmentKind(record.Kind) || record.Target == "" || record.OriginHost == "" || (!record.Deleted && record.Role == "") {
			continue
		}
		current, found, err := agentRoleAssignmentTx(tx, record.Kind, record.Target)
		if err != nil {
			return false, err
		}
		if found && !record.newerThan(current) {
			continue
		}
		if err := putAgentRoleAssignmentTx(tx, record); err != nil {
			return false, err
		}
		changed = true
	}
	if !changed {
		return false, nil
	}
	return true, tx.Commit()
}

// ForkChain lists nearest first, so a branch takes the assignment of the
// conversation it came from until it is given its own.
func (r *Registry) ForkChain(sessionID string) ([]string, error) {
	rows, err := r.callers.Query(`WITH RECURSIVE chain(id, depth) AS (
		SELECT ?, 0
		UNION
		SELECT s.forked_from, c.depth+1 FROM sessions s JOIN chain c ON s.id=c.id WHERE s.forked_from<>'' AND c.depth<?
	) SELECT id FROM chain ORDER BY depth`, sessionID, maxForkDepth)
	if err != nil {
		return nil, err
	}
	return appendRows([]string{}, rows, func(row rowScanner) (string, error) {
		var id string
		return id, row.Scan(&id)
	})
}

func scanAgentRoleAssignment(row rowScanner) (AgentRoleAssignment, error) {
	var record AgentRoleAssignment
	var at int64
	err := row.Scan(&record.Kind, &record.Target, &record.Role, &record.Deleted, &at, &record.OriginHost)
	record.ChangedAt = time.UnixMilli(at)
	return record, err
}

func agentRoleAssignmentTx(tx *sql.Tx, kind, target string) (AgentRoleAssignment, bool, error) {
	record, err := scanAgentRoleAssignment(tx.QueryRow(`SELECT kind,target,role,deleted,changed_at_ms,origin_host FROM agent_role_assignments WHERE kind=? AND target=?`, kind, target))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentRoleAssignment{}, false, nil
	}
	return record, err == nil, err
}

func putAgentRoleAssignmentTx(tx *sql.Tx, record AgentRoleAssignment) error {
	if _, err := tx.Exec(`INSERT INTO agent_role_assignments(kind,target,role,deleted,changed_at_ms,origin_host) VALUES(?,?,?,?,?,?)
		ON CONFLICT(kind,target) DO UPDATE SET role=excluded.role, deleted=excluded.deleted, changed_at_ms=excluded.changed_at_ms, origin_host=excluded.origin_host`,
		record.Kind, record.Target, record.Role, record.Deleted, record.ChangedAt.UnixMilli(), record.OriginHost); err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, _, err = appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("agent.role.assignment:%s:%s:%d:%s", record.Kind, record.Target, record.ChangedAt.UnixMilli(), record.OriginHost), Type: "agent.role.assignment", EntityID: record.Kind + ":" + record.Target, Payload: payload})
	return err
}
