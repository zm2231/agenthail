package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var agentIdentityName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type AgentIdentity struct {
	Name      string    `json:"name"`
	SessionID string    `json:"sessionId"`
	CreatedAt time.Time `json:"createdAt"`
	Unread    int       `json:"unread"`
	Total     int       `json:"total"`
}

type InboxMessage struct {
	ID         int64     `json:"id"`
	Identity   string    `json:"identity"`
	UUID       string    `json:"messageId,omitempty"`
	SenderID   string    `json:"senderId,omitempty"`
	SenderName string    `json:"sender,omitempty"`
	Message    string    `json:"message"`
	ReceivedAt time.Time `json:"receivedAt"`
	ReadAt     time.Time `json:"readAt,omitzero"`
}

var ErrNoAgentIdentity = errors.New("no such agent identity")

type AgentIdentityNameError struct{ Name string }

func (e AgentIdentityNameError) Error() string {
	return fmt.Sprintf("identity name %q must be 1 to 64 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit", e.Name)
}

type AgentIdentityTakenError struct {
	Name  string
	Owner string
}

func (e AgentIdentityTakenError) Error() string {
	return fmt.Sprintf("@%s already names session %s; choose another identity name", e.Name, e.Owner)
}

func ValidAgentIdentityName(name string) error {
	if !agentIdentityName.MatchString(name) {
		return AgentIdentityNameError{Name: name}
	}
	return nil
}

// AddAgentIdentity records the identity's session and binds its alias, refusing
// a name any alias, coordinator, reservation or session id already holds.
func (r *Registry) AddAgentIdentity(name string) (AgentIdentity, error) {
	if err := ValidAgentIdentityName(name); err != nil {
		return AgentIdentity{}, err
	}
	if strings.EqualFold(name, LeadAlias) {
		return AgentIdentity{}, ErrReservedAlias
	}
	now := r.now()
	host, err := r.HostIdentity()
	if err != nil {
		return AgentIdentity{}, fmt.Errorf("read this host's id: %w", err)
	}
	id := surface.AgentIdentityID(host.ID, name)
	tx, err := r.db.Begin()
	if err != nil {
		return AgentIdentity{}, err
	}
	defer tx.Rollback()
	var existing string
	switch err := tx.QueryRow(`SELECT identity_id FROM agent_identities WHERE name=?`, name).Scan(&existing); {
	case err == nil:
		return AgentIdentity{}, fmt.Errorf("identity @%s already exists", name)
	case !errors.Is(err, sql.ErrNoRows):
		return AgentIdentity{}, err
	}
	var owner string
	switch err := tx.QueryRow(`SELECT session_id FROM aliases WHERE name=?`, name).Scan(&owner); {
	case err == nil:
		return AgentIdentity{}, AgentIdentityTakenError{Name: name, Owner: owner}
	case !errors.Is(err, sql.ErrNoRows):
		return AgentIdentity{}, err
	}
	switch err := tx.QueryRow(`SELECT id FROM sessions WHERE id=?`, name).Scan(&owner); {
	case err == nil:
		return AgentIdentity{}, AgentIdentityTakenError{Name: name, Owner: owner}
	case !errors.Is(err, sql.ErrNoRows):
		return AgentIdentity{}, err
	}
	if coordinated, err := coordinatorNamedTx(tx, name); err != nil {
		return AgentIdentity{}, err
	} else if coordinated {
		return AgentIdentity{}, CoordinatorAliasError{Name: name}
	}
	if err := aliasReservedTx(tx, name, "", now); err != nil {
		return AgentIdentity{}, err
	}
	var ownerHost string
	switch err := tx.QueryRow(`SELECT host_id FROM sessions WHERE id=?`, id).Scan(&ownerHost); {
	case err == nil && ownerHost != "":
		return AgentIdentity{}, SessionOwnedError{SessionID: id, HostID: ownerHost}
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return AgentIdentity{}, err
	}
	session := surface.Session{ID: id, Surface: surface.KindAgent, RuntimeKind: surface.RuntimeAgentInbox, Name: name, Status: surface.StatusIdle, LastActive: now}
	if err := registerSessionRowTx(tx, session); err != nil {
		return AgentIdentity{}, err
	}
	if _, err := tx.Exec(`UPDATE sessions SET ended=0, status=? WHERE id=?`, string(surface.StatusIdle), id); err != nil {
		return AgentIdentity{}, err
	}
	if err := bindAliasTx(tx, name, id); err != nil {
		return AgentIdentity{}, err
	}
	if _, err := tx.Exec(`INSERT INTO agent_identities(identity_id,name,created_at_ms) VALUES(?,?,?)`, id, name, now.UnixMilli()); err != nil {
		return AgentIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentIdentity{}, err
	}
	return AgentIdentity{Name: name, SessionID: id, CreatedAt: now}, nil
}

// RemoveAgentIdentity drops the identity, its inbox and its alias. Its session
// row stays for discovery to retire, so paired hosts drop their mirror of it.
func (r *Registry) RemoveAgentIdentity(name string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id, err := agentIdentityIDTx(tx, name)
	if err != nil {
		return err
	}
	var owner string
	switch err := tx.QueryRow(`SELECT session_id FROM aliases WHERE name=?`, name).Scan(&owner); {
	case err == nil && owner != id:
		return fmt.Errorf("@%s now names session %s, not the identity; run 'agenthail identify rm %s' first if that session should lose the name", name, owner, name)
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}
	if _, err := tx.Exec(`DELETE FROM aliases WHERE session_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM agent_identities WHERE identity_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sessions SET ended=1, status=?, updated_at=datetime('now') WHERE id=?`, string(surface.StatusOffline), id); err != nil {
		return err
	}
	return tx.Commit()
}

func agentIdentityIDTx(q catalogQuerier, name string) (string, error) {
	var id string
	err := q.QueryRow(`SELECT identity_id FROM agent_identities WHERE name=?`, strings.TrimPrefix(name, "@")).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: @%s (create it with: agenthail identity add %s)", ErrNoAgentIdentity, strings.TrimPrefix(name, "@"), strings.TrimPrefix(name, "@"))
	}
	return id, err
}

const agentIdentitySelect = `SELECT i.name,i.identity_id,i.created_at_ms,
	(SELECT COUNT(*) FROM agent_inbox m WHERE m.identity_id=i.identity_id AND m.read_at_ms=0),
	(SELECT COUNT(*) FROM agent_inbox m WHERE m.identity_id=i.identity_id)
	FROM agent_identities i`

func scanAgentIdentity(row rowScanner) (AgentIdentity, error) {
	var identity AgentIdentity
	var created int64
	if err := row.Scan(&identity.Name, &identity.SessionID, &created, &identity.Unread, &identity.Total); err != nil {
		return AgentIdentity{}, err
	}
	identity.CreatedAt = time.UnixMilli(created).UTC()
	return identity, nil
}

func (r *Registry) AgentIdentities() ([]AgentIdentity, error) {
	return queryRows(r.read, []AgentIdentity{}, scanAgentIdentity, agentIdentitySelect+` ORDER BY i.name`)
}

// AgentIdentityOf is the identity a session id carries.
func (r *Registry) AgentIdentityOf(sessionID string) (AgentIdentity, error) {
	identity, err := scanAgentIdentity(r.read.QueryRow(agentIdentitySelect+` WHERE i.identity_id=?`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentIdentity{}, fmt.Errorf("%w: session %s", ErrNoAgentIdentity, sessionID)
	}
	return identity, err
}

func (r *Registry) AgentIdentityNamed(name string) (AgentIdentity, error) {
	id, err := agentIdentityIDTx(r.read, name)
	if err != nil {
		return AgentIdentity{}, err
	}
	return r.AgentIdentityOf(id)
}

// StoreInboxMessage keeps a message for the identity; a message id already
// stored is kept once and reports false.
func (r *Registry) StoreInboxMessage(message InboxMessage) (InboxMessage, bool, error) {
	if message.ReceivedAt.IsZero() {
		message.ReceivedAt = r.now()
	}
	tx, err := r.db.Begin()
	if err != nil {
		return InboxMessage{}, false, err
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRow(`SELECT name FROM agent_identities WHERE identity_id=?`, message.Identity).Scan(&name); errors.Is(err, sql.ErrNoRows) {
		return InboxMessage{}, false, fmt.Errorf("%w: session %s", ErrNoAgentIdentity, message.Identity)
	} else if err != nil {
		return InboxMessage{}, false, err
	}
	result, err := tx.Exec(`INSERT INTO agent_inbox(identity_id,message_uuid,sender_id,sender_name,message,received_at_ms) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
		message.Identity, message.UUID, message.SenderID, message.SenderName, message.Message, message.ReceivedAt.UnixMilli())
	if err != nil {
		return InboxMessage{}, false, err
	}
	stored, err := result.RowsAffected()
	if err != nil {
		return InboxMessage{}, false, err
	}
	if stored == 0 {
		existing, err := scanInboxMessage(tx.QueryRow(inboxSelect+` WHERE m.identity_id=? AND m.message_uuid=?`, message.Identity, message.UUID))
		if err != nil {
			return InboxMessage{}, false, err
		}
		return existing, false, tx.Commit()
	}
	if message.ID, err = result.LastInsertId(); err != nil {
		return InboxMessage{}, false, err
	}
	if _, err := tx.Exec(`UPDATE sessions SET last_active_ms=MAX(last_active_ms,?), updated_at=datetime('now') WHERE id=?`, message.ReceivedAt.UnixMilli(), message.Identity); err != nil {
		return InboxMessage{}, false, err
	}
	message.Identity = name
	return message, true, tx.Commit()
}

// AckInbox marks read the identity's messages up to and including through.
func (r *Registry) AckInbox(name string, through int64) (int, error) {
	id, err := agentIdentityIDTx(r.read, name)
	if err != nil {
		return 0, err
	}
	result, err := r.db.Exec(`UPDATE agent_inbox SET read_at_ms=? WHERE identity_id=? AND id<=? AND read_at_ms=0`, r.now().UnixMilli(), id, through)
	if err != nil {
		return 0, err
	}
	acked, err := result.RowsAffected()
	return int(acked), err
}

type InboxQuery struct {
	All   bool
	Peek  bool
	Limit int
}

const inboxSelect = `SELECT m.id,i.name,m.message_uuid,m.sender_id,m.sender_name,m.message,m.received_at_ms,m.read_at_ms FROM agent_inbox m JOIN agent_identities i ON i.identity_id=m.identity_id`

func scanInboxMessage(row rowScanner) (InboxMessage, error) {
	var message InboxMessage
	var received, read int64
	if err := row.Scan(&message.ID, &message.Identity, &message.UUID, &message.SenderID, &message.SenderName, &message.Message, &received, &read); err != nil {
		return InboxMessage{}, err
	}
	message.ReceivedAt = time.UnixMilli(received).UTC()
	if read > 0 {
		message.ReadAt = time.UnixMilli(read).UTC()
	}
	return message, nil
}

// ReadInbox returns the identity's messages oldest first: unread ones, or
// every one with All. Unless Peek, the unread ones it returns are marked read
// in the same transaction, so two readers never both take a message.
func (r *Registry) ReadInbox(name string, query InboxQuery) ([]InboxMessage, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	id, err := agentIdentityIDTx(tx, name)
	if err != nil {
		return nil, err
	}
	statement := inboxSelect + ` WHERE m.identity_id=?`
	if !query.All {
		statement += ` AND m.read_at_ms=0`
	}
	statement += ` ORDER BY m.id`
	args := []any{id}
	if query.Limit > 0 {
		statement += ` LIMIT ?`
		args = append(args, query.Limit)
	}
	messages, err := queryRows(tx, []InboxMessage{}, scanInboxMessage, statement, args...)
	if err != nil {
		return nil, err
	}
	if !query.Peek {
		now := r.now()
		for index := range messages {
			if !messages[index].ReadAt.IsZero() {
				continue
			}
			if _, err := tx.Exec(`UPDATE agent_inbox SET read_at_ms=? WHERE id=? AND read_at_ms=0`, now.UnixMilli(), messages[index].ID); err != nil {
				return nil, err
			}
			messages[index].ReadAt = time.UnixMilli(now.UnixMilli()).UTC()
		}
	}
	return messages, tx.Commit()
}

// UnreadInbox counts the identity's unread messages.
func (r *Registry) UnreadInbox(name string) (int, error) {
	id, err := agentIdentityIDTx(r.read, name)
	if err != nil {
		return 0, err
	}
	var count int
	return count, r.read.QueryRow(`SELECT COUNT(*) FROM agent_inbox WHERE identity_id=? AND read_at_ms=0`, id).Scan(&count)
}

// PruneInbox deletes the identity's read messages received before cutoff.
func (r *Registry) PruneInbox(name string, cutoff time.Time) (int64, error) {
	id, err := agentIdentityIDTx(r.read, name)
	if err != nil {
		return 0, err
	}
	result, err := r.db.Exec(`DELETE FROM agent_inbox WHERE identity_id=? AND read_at_ms>0 AND received_at_ms<?`, id, cutoff.UnixMilli())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// AgentIdentitySessions are the identities' sessions, for the inbox adapter's
// listing.
func (r *Registry) AgentIdentitySessions() ([]surface.Session, error) {
	rows, err := r.read.Query(sessionSelect(`s.status`) + ` FROM sessions s` + sessionJoins + ` JOIN agent_identities i ON i.identity_id=s.id ORDER BY i.name`)
	if err != nil {
		return nil, err
	}
	return appendRows(make([]surface.Session, 0), rows, scanSession)
}

func (r *Registry) InboxMessageByID(name string, messageID int64) (InboxMessage, error) {
	id, err := agentIdentityIDTx(r.read, name)
	if err != nil {
		return InboxMessage{}, err
	}
	message, err := scanInboxMessage(r.read.QueryRow(inboxSelect+` WHERE m.identity_id=? AND m.id=?`, id, messageID))
	if errors.Is(err, sql.ErrNoRows) {
		return InboxMessage{}, fmt.Errorf("@%s has no message %d", name, messageID)
	}
	return message, err
}
