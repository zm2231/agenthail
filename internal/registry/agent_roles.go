package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// AgentRoleRecord is the person's definition of one agent role. Paired
// hosts share every record; of two for one name the later wins, the origin
// host breaking a tie. A deleted record stays so its deletion reaches hosts
// that still hold the role.
type AgentRoleRecord struct {
	Name       string          `json:"name"`
	Definition json.RawMessage `json:"definition"`
	Deleted    bool            `json:"deleted,omitempty"`
	ChangedAt  time.Time       `json:"changedAt"`
	OriginHost string          `json:"originHost"`
}

func (a AgentRoleRecord) newerThan(other AgentRoleRecord) bool {
	if !a.ChangedAt.Equal(other.ChangedAt) {
		return a.ChangedAt.After(other.ChangedAt)
	}
	return a.OriginHost > other.OriginHost
}

// AgentRoles reads on the callers' reader, since every caller resolves its
// role and must not wait behind background reads.
func (r *Registry) AgentRoles() ([]AgentRoleRecord, error) {
	rows, err := r.callers.Query(`SELECT name,definition,deleted,changed_at_ms,origin_host FROM agent_roles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return appendRows([]AgentRoleRecord{}, rows, scanAgentRole)
}

// PutAgentRole records this host's change to a role, after any record it
// already holds for the name.
func (r *Registry) PutAgentRole(name string, definition json.RawMessage, deleted bool) (AgentRoleRecord, error) {
	if name == "" {
		return AgentRoleRecord{}, errors.New("a role needs a name")
	}
	if !deleted && !json.Valid(definition) {
		return AgentRoleRecord{}, fmt.Errorf("the definition of role %s is not JSON", name)
	}
	if deleted {
		definition = json.RawMessage(`{}`)
	}
	identity, err := r.HostIdentity()
	if err != nil {
		return AgentRoleRecord{}, fmt.Errorf("read this host's identity: %w", err)
	}
	record := AgentRoleRecord{Name: name, Definition: definition, Deleted: deleted, ChangedAt: r.now().Truncate(time.Millisecond), OriginHost: identity.ID}
	tx, err := r.db.Begin()
	if err != nil {
		return AgentRoleRecord{}, err
	}
	defer tx.Rollback()
	if current, found, err := agentRoleTx(tx, name); err != nil {
		return AgentRoleRecord{}, err
	} else if found && !record.ChangedAt.After(current.ChangedAt) {
		record.ChangedAt = current.ChangedAt.Add(time.Millisecond)
	}
	if err := putAgentRoleTx(tx, record); err != nil {
		return AgentRoleRecord{}, err
	}
	return record, tx.Commit()
}

// AdoptAgentRoles keeps each record newer than the one held for its name.
func (r *Registry) AdoptAgentRoles(records []AgentRoleRecord) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed := false
	for _, record := range records {
		if record.Name == "" || record.OriginHost == "" || (!record.Deleted && !json.Valid(record.Definition)) {
			continue
		}
		current, found, err := agentRoleTx(tx, record.Name)
		if err != nil {
			return false, err
		}
		if found && !record.newerThan(current) {
			continue
		}
		if err := putAgentRoleTx(tx, record); err != nil {
			return false, err
		}
		changed = true
	}
	if !changed {
		return false, nil
	}
	return true, tx.Commit()
}

func scanAgentRole(row rowScanner) (AgentRoleRecord, error) {
	var record AgentRoleRecord
	var definition string
	var at int64
	err := row.Scan(&record.Name, &definition, &record.Deleted, &at, &record.OriginHost)
	record.Definition, record.ChangedAt = json.RawMessage(definition), time.UnixMilli(at)
	return record, err
}

func agentRoleTx(tx *sql.Tx, name string) (AgentRoleRecord, bool, error) {
	record, err := scanAgentRole(tx.QueryRow(`SELECT name,definition,deleted,changed_at_ms,origin_host FROM agent_roles WHERE name=?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentRoleRecord{}, false, nil
	}
	return record, err == nil, err
}

func putAgentRoleTx(tx *sql.Tx, record AgentRoleRecord) error {
	if _, err := tx.Exec(`INSERT INTO agent_roles(name,definition,deleted,changed_at_ms,origin_host) VALUES(?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET definition=excluded.definition, deleted=excluded.deleted, changed_at_ms=excluded.changed_at_ms, origin_host=excluded.origin_host`,
		record.Name, string(record.Definition), record.Deleted, record.ChangedAt.UnixMilli(), record.OriginHost); err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, _, err = appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("agent.role:%s:%d:%s", record.Name, record.ChangedAt.UnixMilli(), record.OriginHost), Type: "agent.role", EntityID: record.Name, Payload: payload})
	return err
}
