package registry

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var (
	hostNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`)
	hostIDPattern   = regexp.MustCompile(`^h[0-9a-f]{12}$`)

	ErrHostNotFound = errors.New("host is not paired")
)

// SessionOwnedError reports that a session id already belongs to another host.
// The same conversation id on two hosts is a move or copy, which the move
// protocol owns; a mirror never takes over a row it does not own.
type SessionOwnedError struct {
	SessionID string
	HostID    string
}

func (e SessionOwnedError) Error() string {
	owner := e.HostID
	if owner == "" {
		owner = "this host"
	}
	return fmt.Sprintf("session %s is owned by %s", e.SessionID, owner)
}

type HostIdentity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PairedHost struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Endpoint      string `json:"endpoint"`
	Address       string `json:"address"`
	Token         string `json:"-"`
	State         string `json:"state"`
	LastError     string `json:"lastError,omitempty"`
	LastContactAt string `json:"lastContactAt,omitempty"`
	CreatedAt     string `json:"createdAt"`
	CatalogEpoch  string `json:"-"`
	CatalogSeq    uint64 `json:"-"`
}

func ValidHostName(name string) bool {
	return hostNamePattern.MatchString(name)
}

func ValidHostID(id string) bool {
	return hostIDPattern.MatchString(id)
}

func (r *Registry) HostIdentity() (HostIdentity, error) {
	var identity HostIdentity
	err := r.read.QueryRow(`SELECT host_id,name FROM host_identity WHERE id=1`).Scan(&identity.ID, &identity.Name)
	return identity, err
}

// ensureHostIdentity creates this daemon's host id once, when the registry is
// opened, so reading the identity never writes.
func (r *Registry) ensureHostIdentity() error {
	var exists bool
	if err := r.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM host_identity WHERE id=1)`).Scan(&exists); err != nil || exists {
		return err
	}
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("generate host id: %w", err)
	}
	_, err := r.db.Exec(`INSERT OR IGNORE INTO host_identity(id,host_id,name) VALUES(1,?,?)`, "h"+hex.EncodeToString(raw), defaultHostName())
	return err
}

func defaultHostName() string {
	hostname, _ := os.Hostname()
	hostname, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(hostname)), ".")
	hostname = strings.Trim(generatedAliasCharacters.ReplaceAllString(hostname, "-"), "-._")
	hostname = strings.ReplaceAll(hostname, "_", "-")
	if len(hostname) > 63 {
		hostname = hostname[:63]
	}
	if !ValidHostName(hostname) {
		return "host"
	}
	return hostname
}

func (r *Registry) SetHostName(name string) (HostIdentity, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !ValidHostName(name) {
		return HostIdentity{}, fmt.Errorf("host name must be 1 to 63 lowercase letters, digits, dots or dashes")
	}
	identity, err := r.HostIdentity()
	if err != nil {
		return HostIdentity{}, err
	}
	var taken int
	if err := r.read.QueryRow(`SELECT COUNT(*) FROM hosts WHERE name=?`, name).Scan(&taken); err != nil {
		return HostIdentity{}, err
	}
	if taken > 0 {
		return HostIdentity{}, fmt.Errorf("host name %q is already used by a paired host", name)
	}
	if _, err := r.db.Exec(`UPDATE host_identity SET name=? WHERE id=1`, name); err != nil {
		return HostIdentity{}, err
	}
	identity.Name = name
	return identity, nil
}

func (r *Registry) AddHost(host PairedHost) (PairedHost, error) {
	host.Name = strings.ToLower(strings.TrimSpace(host.Name))
	if !ValidHostID(host.ID) {
		return PairedHost{}, fmt.Errorf("remote host id %q is invalid", host.ID)
	}
	if !ValidHostName(host.Name) {
		return PairedHost{}, fmt.Errorf("host name must be 1 to 63 lowercase letters, digits, dots or dashes")
	}
	if host.Endpoint == "" || host.Address == "" || host.Token == "" {
		return PairedHost{}, fmt.Errorf("host endpoint, address and token are required")
	}
	local, err := r.HostIdentity()
	if err != nil {
		return PairedHost{}, err
	}
	if host.ID == local.ID {
		return PairedHost{}, fmt.Errorf("cannot pair this daemon with itself")
	}
	if host.Name == local.Name {
		return PairedHost{}, fmt.Errorf("host name %q is this daemon's own name; choose another with --name", host.Name)
	}
	if host.State == "" {
		host.State = "connecting"
	}
	host.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	_, err = r.db.Exec(`INSERT INTO hosts(host_id,name,endpoint,address,token,state,created_at) VALUES(?,?,?,?,?,?,?)`, host.ID, host.Name, host.Endpoint, host.Address, host.Token, host.State, host.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "hosts.host_id") || strings.Contains(err.Error(), "PRIMARY KEY") {
			return PairedHost{}, fmt.Errorf("host %s is already paired", host.ID)
		}
		if strings.Contains(err.Error(), "hosts.name") || strings.Contains(err.Error(), "UNIQUE") {
			return PairedHost{}, fmt.Errorf("host name %q is already paired; choose another with --name", host.Name)
		}
		return PairedHost{}, err
	}
	return host, nil
}

const pairedHostColumns = `host_id,name,endpoint,address,token,state,last_error,last_contact_at,created_at,catalog_epoch,catalog_seq`

func scanPairedHost(row interface{ Scan(...any) error }) (PairedHost, error) {
	var host PairedHost
	var seq int64
	err := row.Scan(&host.ID, &host.Name, &host.Endpoint, &host.Address, &host.Token, &host.State, &host.LastError, &host.LastContactAt, &host.CreatedAt, &host.CatalogEpoch, &seq)
	if seq > 0 {
		host.CatalogSeq = uint64(seq)
	}
	return host, err
}

func (r *Registry) ListHosts() ([]PairedHost, error) {
	rows, err := r.read.Query(`SELECT ` + pairedHostColumns + ` FROM hosts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hosts := []PairedHost{}
	for rows.Next() {
		host, err := scanPairedHost(rows)
		if err != nil {
			return nil, err
		}
		hosts = append(hosts, host)
	}
	return hosts, rows.Err()
}

func (r *Registry) Host(ref string) (PairedHost, error) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	host, err := scanPairedHost(r.read.QueryRow(`SELECT `+pairedHostColumns+` FROM hosts WHERE host_id=? OR name=? ORDER BY host_id=? DESC LIMIT 1`, ref, ref, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return PairedHost{}, fmt.Errorf("%w: %s", ErrHostNotFound, ref)
	}
	return host, err
}

func (r *Registry) RemoveHost(hostID string) ([]CatalogEvent, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`DELETE FROM hosts WHERE host_id=?`, hostID)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return nil, fmt.Errorf("%w: %s", ErrHostNotFound, hostID)
	}
	rows, err := tx.Query(`SELECT s.id,cs.projection_fingerprint FROM sessions s JOIN catalog_sessions cs ON cs.session_id=s.id WHERE s.host_id=?`, hostID)
	if err != nil {
		return nil, err
	}
	type removed struct{ id, entity string }
	var cataloged []removed
	for rows.Next() {
		var id, fingerprint string
		if err := rows.Scan(&id, &fingerprint); err != nil {
			rows.Close()
			return nil, err
		}
		cataloged = append(cataloged, removed{id: id, entity: projectionID(fingerprint, id)})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	events := []CatalogEvent{}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, item := range cataloged {
		payload, _ := json.Marshal(map[string]string{"sessionId": item.entity, "reason": "host_removed"})
		event, created, err := appendCatalogEventTx(tx, CatalogEvent{DedupeKey: "session.removed:" + item.entity + ":" + stamp, Type: "session.removed", EntityID: item.entity, Payload: payload})
		if err != nil {
			return nil, err
		}
		if created {
			events = append(events, event)
		}
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE host_id=?`, hostID); err != nil {
		return nil, err
	}
	return events, tx.Commit()
}

func projectionID(fingerprint, fallback string) string {
	var projection struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(fingerprint), &projection) == nil && projection.ID != "" {
		return projection.ID
	}
	return fallback
}

func (r *Registry) RecordHostContact(hostID, state, lastError string, contacted bool) error {
	if contacted {
		_, err := r.db.Exec(`UPDATE hosts SET state=?,last_error=?,last_contact_at=? WHERE host_id=?`, state, lastError, time.Now().UTC().Format(time.RFC3339Nano), hostID)
		return err
	}
	_, err := r.db.Exec(`UPDATE hosts SET state=?,last_error=? WHERE host_id=?`, state, lastError, hostID)
	return err
}

func (r *Registry) RecordHostCatalogCursor(hostID, epoch string, seq uint64) error {
	_, err := r.db.Exec(`UPDATE hosts SET catalog_epoch=?,catalog_seq=? WHERE host_id=?`, epoch, int64(seq), hostID)
	return err
}

func (r *Registry) SessionHost(sessionID string) (string, error) {
	var hostID string
	err := r.read.QueryRow(`SELECT host_id FROM sessions WHERE id=?`, sessionID).Scan(&hostID)
	return hostID, err
}

type HostCatalogRow struct {
	Session           surface.Session
	EntityID          string
	Projection        json.RawMessage
	Project       json.RawMessage
	Checkout          json.RawMessage
	UnavailableReason string
	ObservedAt        time.Time
	// QueueCount is the owning host's own count. The stored projection's
	// queueCount adds the sends this daemon holds for the session.
	QueueCount int
}

// Process and transcript columns stay empty: they describe another machine.
func (r *Registry) ApplyHostCatalogRow(hostID string, row HostCatalogRow) (CatalogEvent, bool, error) {
	if hostID == "" || row.Session.ID == "" || row.EntityID == "" {
		return CatalogEvent{}, false, fmt.Errorf("host catalog row requires host, session and entity ids")
	}
	var projection map[string]any
	if err := json.Unmarshal(row.Projection, &projection); err != nil {
		return CatalogEvent{}, false, fmt.Errorf("host catalog projection must be a JSON object: %w", err)
	}
	if len(row.Project) == 0 {
		row.Project = json.RawMessage(`null`)
	}
	if len(row.Checkout) == 0 {
		row.Checkout = json.RawMessage(`{}`)
	}
	if row.ObservedAt.IsZero() {
		row.ObservedAt = time.Now().UTC()
	}
	if err := r.EnsureCatalogState(); err != nil {
		return CatalogEvent{}, false, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return CatalogEvent{}, false, err
	}
	defer tx.Rollback()
	var paired int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM hosts WHERE host_id=?`, hostID).Scan(&paired); err != nil {
		return CatalogEvent{}, false, err
	}
	if paired == 0 {
		return CatalogEvent{}, false, fmt.Errorf("host %s is not paired", hostID)
	}
	var owner string
	err = tx.QueryRow(`SELECT host_id FROM sessions WHERE id=?`, row.Session.ID).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return CatalogEvent{}, false, err
	}
	if err == nil && owner != hostID {
		return CatalogEvent{}, false, SessionOwnedError{SessionID: row.Session.ID, HostID: owner}
	}
	session := row.Session
	lastActiveMS := int64(0)
	if !session.LastActive.IsZero() {
		lastActiveMS = session.LastActive.UnixMilli()
	}
	parentID, rootID, depth, nickname, role := subagentValues(session)
	if _, err := tx.Exec(`INSERT INTO sessions(id,host_id,surface,name,cwd,status,source,transport,configured_model,last_active_ms,parent_session_id,root_session_id,subagent_depth,agent_nickname,agent_role,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,datetime('now'))
		ON CONFLICT(id) DO UPDATE SET surface=excluded.surface,name=excluded.name,cwd=excluded.cwd,status=excluded.status,source=excluded.source,
		transport=excluded.transport,configured_model=excluded.configured_model,last_active_ms=excluded.last_active_ms,parent_session_id=excluded.parent_session_id,
		root_session_id=excluded.root_session_id,subagent_depth=excluded.subagent_depth,agent_nickname=excluded.agent_nickname,agent_role=excluded.agent_role,updated_at=datetime('now')`,
		session.ID, hostID, string(session.Surface), session.Name, session.Cwd, string(session.Status), session.Source, session.Transport, session.ConfiguredModel, lastActiveMS, parentID, rootID, depth, nickname, role); err != nil {
		return CatalogEvent{}, false, err
	}
	prior, err := readCatalogSessionPrior(tx, session.ID)
	if err != nil {
		return CatalogEvent{}, false, err
	}
	held, err := queueCountTx(tx, session.ID, time.Now())
	if err != nil {
		return CatalogEvent{}, false, err
	}
	projection["queueCount"] = row.QueueCount + held
	encoded, err := json.Marshal(projection)
	if err != nil {
		return CatalogEvent{}, false, err
	}
	fingerprint := string(encoded)
	var priorHostCount int
	if prior.found {
		if err := tx.QueryRow(`SELECT host_queue_count FROM catalog_sessions WHERE session_id=?`, session.ID).Scan(&priorHostCount); err != nil {
			return CatalogEvent{}, false, err
		}
	}
	if prior.found && prior.fingerprint == fingerprint && prior.unavailable == row.UnavailableReason {
		if priorHostCount != row.QueueCount {
			if _, err := tx.Exec(`UPDATE catalog_sessions SET host_queue_count=? WHERE session_id=?`, row.QueueCount, session.ID); err != nil {
				return CatalogEvent{}, false, err
			}
		}
		return CatalogEvent{}, false, tx.Commit()
	}
	generation := prior.generation + 1
	observedAt := row.ObservedAt.UTC()
	if _, err := tx.Exec(`INSERT INTO catalog_sessions(session_id,project,checkout,unavailable_reason,observed_at,projection_fingerprint,projection_generation,discovery_failures,host_queue_count)
		VALUES(?,?,?,?,?,?,?,0,?) ON CONFLICT(session_id) DO UPDATE SET project=excluded.project,checkout=excluded.checkout,
		unavailable_reason=excluded.unavailable_reason,observed_at=excluded.observed_at,misses=0,projection_fingerprint=excluded.projection_fingerprint,
		projection_generation=excluded.projection_generation,discovery_failures=0,host_queue_count=excluded.host_queue_count`,
		session.ID, []byte(row.Project), []byte(row.Checkout), row.UnavailableReason, observedAt.Format(time.RFC3339Nano), fingerprint, generation, row.QueueCount); err != nil {
		return CatalogEvent{}, false, err
	}
	event, created, err := appendHostUpsertTx(tx, row.EntityID, projection, generation, observedAt, row.UnavailableReason)
	if err != nil {
		return CatalogEvent{}, false, err
	}
	return event, created, tx.Commit()
}

func appendHostUpsertTx(tx *sql.Tx, entityID string, projection map[string]any, generation int64, observedAt time.Time, unavailable string) (CatalogEvent, bool, error) {
	projection["freshness"] = CatalogFreshness{Generation: uint64(generation), ObservedAt: observedAt, Stale: unavailable != ""}
	payload, err := json.Marshal(map[string]any{"session": projection})
	if err != nil {
		return CatalogEvent{}, false, err
	}
	return appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("session.upserted:%s:%d", entityID, generation), Type: "session.upserted", EntityID: entityID, Payload: payload})
}

// RefreshHostQueueCounts re-adds this daemon's held sends to every mirrored
// row whose projected queue count no longer matches, in one transaction so a
// concurrent mirror write is never overwritten with an older projection.
func (r *Registry) RefreshHostQueueCounts() (int, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT cs.session_id,cs.projection_fingerprint,cs.projection_generation,cs.host_queue_count,cs.observed_at,cs.unavailable_reason
		FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id WHERE s.host_id<>''`)
	if err != nil {
		return 0, err
	}
	type mirrored struct {
		id, fingerprint, observedAt, unavailable string
		generation                               int64
		hostCount                                int
	}
	var candidates []mirrored
	for rows.Next() {
		var row mirrored
		if err := rows.Scan(&row.id, &row.fingerprint, &row.generation, &row.hostCount, &row.observedAt, &row.unavailable); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, row)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	now := time.Now()
	changed := 0
	for _, row := range candidates {
		held, err := queueCountTx(tx, row.id, now)
		if err != nil {
			return 0, err
		}
		var projection map[string]any
		if err := json.Unmarshal([]byte(row.fingerprint), &projection); err != nil {
			continue
		}
		want := row.hostCount + held
		if current, ok := projection["queueCount"].(float64); ok && int(current) == want {
			continue
		}
		projection["queueCount"] = want
		encoded, err := json.Marshal(projection)
		if err != nil {
			return 0, err
		}
		entityID, _ := projection["id"].(string)
		if entityID == "" {
			continue
		}
		generation := row.generation + 1
		if _, err := tx.Exec(`UPDATE catalog_sessions SET projection_fingerprint=?,projection_generation=? WHERE session_id=?`, string(encoded), generation, row.id); err != nil {
			return 0, err
		}
		observedAt, _ := time.Parse(time.RFC3339Nano, row.observedAt)
		if _, _, err := appendHostUpsertTx(tx, entityID, projection, generation, observedAt, row.unavailable); err != nil {
			return 0, err
		}
		changed++
	}
	return changed, tx.Commit()
}

func (r *Registry) RemoveHostCatalogRow(hostID, sessionID, entityID, reason string) (CatalogEvent, bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return CatalogEvent{}, false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`DELETE FROM catalog_sessions WHERE session_id=? AND session_id IN (SELECT id FROM sessions WHERE host_id=?)`, sessionID, hostID)
	if err != nil {
		return CatalogEvent{}, false, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return CatalogEvent{}, false, tx.Commit()
	}
	payload, _ := json.Marshal(map[string]string{"sessionId": entityID, "reason": reason})
	event, created, err := appendCatalogEventTx(tx, CatalogEvent{DedupeKey: "session.removed:" + entityID + ":" + time.Now().UTC().Format(time.RFC3339Nano), Type: "session.removed", EntityID: entityID, Payload: payload})
	if err != nil {
		return CatalogEvent{}, false, err
	}
	return event, created, tx.Commit()
}

func (r *Registry) HostCatalogRows(hostID string) ([]CatalogSessionState, error) {
	rows, err := r.read.Query(`SELECT s.id,s.surface,s.name,s.cwd,s.status,cs.project,cs.checkout,cs.unavailable_reason,cs.observed_at,cs.projection_fingerprint,cs.host_queue_count
		FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id WHERE s.host_id=? ORDER BY s.id`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CatalogSessionState{}
	for rows.Next() {
		var state CatalogSessionState
		var kind, status, observedAt string
		if err := rows.Scan(&state.Session.ID, &kind, &state.Session.Name, &state.Session.Cwd, &status, &state.Project, &state.Checkout, &state.UnavailableReason, &observedAt, &state.ProjectionFingerprint, &state.HostQueueCount); err != nil {
			return nil, err
		}
		state.Session.Surface = surface.SurfaceKind(kind)
		state.Session.Status = surface.SessionStatus(status)
		state.HostID = hostID
		if parsed, err := time.Parse(time.RFC3339Nano, observedAt); err == nil {
			state.ObservedAt = parsed
		}
		result = append(result, state)
	}
	return result, rows.Err()
}

type HostSessionMatch struct {
	HostID  string
	ID      string
	Surface surface.SurfaceKind
	Name    string
	Alias   string
}

// Exact id or alias only: fuzzy matching needs the host itself.
func (r *Registry) MatchHostSessions(hostID, target string) ([]HostSessionMatch, error) {
	alias := strings.TrimPrefix(target, "@")
	rows, err := r.read.Query(`SELECT s.host_id,s.id,s.surface,s.name,coalesce(json_extract(cs.projection_fingerprint,'$.alias'),'')
		FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id
		WHERE s.host_id<>'' AND (?='' OR s.host_id=?) AND (s.id=? OR (?<>'' AND json_extract(cs.projection_fingerprint,'$.alias')=?))
		ORDER BY s.host_id,s.id`, hostID, hostID, target, alias, alias)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matches := []HostSessionMatch{}
	for rows.Next() {
		var match HostSessionMatch
		var kind string
		if err := rows.Scan(&match.HostID, &match.ID, &kind, &match.Name, &match.Alias); err != nil {
			return nil, err
		}
		match.Surface = surface.SurfaceKind(kind)
		matches = append(matches, match)
	}
	return matches, rows.Err()
}

// HostDelivery is a send to a paired host's session that this daemon holds:
// pending was provably never sent, inflight was sent with an unknown outcome.
// Request is the exact action body, replayed only with the same key so the
// host's receipt store can never apply it twice.
type HostDelivery struct {
	ID        int64
	SessionID string
	Key       string
	Request   []byte
	Status    string
	ExpiresAt int64
}

func (r *Registry) QueueHostDelivery(sessionID, message, key string, request []byte, sent bool) (int64, error) {
	if key == "" || len(request) == 0 {
		return 0, fmt.Errorf("a held host delivery needs its idempotency key and request")
	}
	status, inflightAt, expiresAt, evidence := "pending", int64(0), r.now().Add(queueMessageTTL).UnixMilli(), surface.EvidenceQueued
	if sent {
		status, inflightAt, expiresAt, evidence = "inflight", r.now().UnixMilli(), int64(0), surface.EvidenceUnknown
	}
	result, err := r.db.Exec(`INSERT INTO message_queue(session_id,message,delivery_key,host_request,status,attempts,inflight_at_ms,expires_at_ms,evidence,updated_at)
		SELECT ?,?,?,?,?,1,?,?,?,datetime('now') WHERE EXISTS(SELECT 1 FROM sessions WHERE id=? AND host_id<>'')`,
		sessionID, message, key, request, status, inflightAt, expiresAt, evidence, sessionID)
	if err != nil {
		return 0, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return 0, fmt.Errorf("session %s is not owned by a paired host", sessionID)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: sessionID, QueueID: id, Message: message})
	return id, nil
}

func (r *Registry) HostDeliveries(hostID string) ([]HostDelivery, error) {
	rows, err := r.read.Query(`SELECT q.id,q.session_id,q.delivery_key,q.host_request,q.status,q.expires_at_ms FROM message_queue q JOIN sessions s ON s.id=q.session_id
		WHERE s.host_id=? AND q.host_request<>'' AND (q.status='inflight' OR (q.status='pending' AND (q.expires_at_ms=0 OR q.expires_at_ms>?))) ORDER BY q.id`, hostID, r.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []HostDelivery{}
	for rows.Next() {
		var item HostDelivery
		if err := rows.Scan(&item.ID, &item.SessionID, &item.Key, &item.Request, &item.Status, &item.ExpiresAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// ClaimHostDelivery marks a held delivery inflight before it is replayed and
// returns when it first went inflight, the start of its confirmation window.
// A pending row past its deadline, or one expiry or another drain already
// moved, is not claimed and must not be sent.
func (r *Registry) ClaimHostDelivery(id int64) (time.Time, bool, error) {
	now := r.now().UnixMilli()
	var inflightAt int64
	err := r.db.QueryRow(`UPDATE message_queue SET evidence=?,inflight_at_ms=CASE WHEN status='pending' THEN ? ELSE inflight_at_ms END,status='inflight',expires_at_ms=0,attempts=attempts+1,updated_at=datetime('now')
		WHERE id=? AND host_request<>'' AND (status='inflight' OR (status='pending' AND (expires_at_ms=0 OR expires_at_ms>?))) RETURNING inflight_at_ms`, surface.EvidenceUnknown, now, id, now).Scan(&inflightAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.UnixMilli(inflightAt), true, nil
}

// ReleaseHostDelivery returns a claimed delivery that provably never left
// this daemon to pending with its original deadline.
func (r *Registry) ReleaseHostDelivery(id, expiresAt int64) error {
	_, err := r.db.Exec(`UPDATE message_queue SET status='pending',evidence=?,inflight_at_ms=0,expires_at_ms=?,updated_at=datetime('now') WHERE id=? AND host_request<>'' AND status='inflight'`, surface.EvidenceQueued, expiresAt, id)
	return err
}
