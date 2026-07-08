package registry

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

type Registry struct {
	db *sql.DB
}

func Open(path string) (*Registry, error) {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".agenthail", "registry.db")
	}
	os.MkdirAll(filepath.Dir(path), 0700)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	r := &Registry{db: db}
	if err := r.migrate(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) Close() error { return r.db.Close() }

func (r *Registry) migrate() error {
	_, err := r.db.Exec(schema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY, surface TEXT NOT NULL, name TEXT NOT NULL DEFAULT '',
	cwd TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'unknown', transcript TEXT NOT NULL DEFAULT '',
	has_local INTEGER NOT NULL DEFAULT 0,
	registered_at TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS aliases (
	name TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS channels (
	id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS channel_members (
	channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	PRIMARY KEY (channel_id, session_id)
);
CREATE TABLE IF NOT EXISTS routes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	from_session TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	to_session TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	channel_id TEXT REFERENCES channels(id) ON DELETE SET NULL,
	pattern TEXT NOT NULL DEFAULT '.*',
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS steer_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	message TEXT NOT NULL, queued_at TEXT NOT NULL DEFAULT (datetime('now')),
	delivered INTEGER NOT NULL DEFAULT 0
);
`

func (r *Registry) RegisterSession(s surface.Session) error {
	_, err := r.db.Exec(
		`INSERT INTO sessions (id,surface,name,cwd,pid,status,transcript,has_local,updated_at)
		 VALUES (?,?,?,?,?,?,?, ?,datetime('now'))
		 ON CONFLICT(id) DO UPDATE SET surface=excluded.surface,name=excluded.name,cwd=excluded.cwd,
		   pid=excluded.pid,status=excluded.status,transcript=excluded.transcript,
		   has_local=excluded.has_local,updated_at=datetime('now')`,
		s.ID, string(s.Surface), s.Name, s.Cwd, s.PID, string(s.Status), s.Transcript, b2i(s.HasLocal))
	return err
}

func (r *Registry) LookupAlias(name string) (string, error) {
	var sid string
	err := r.db.QueryRow(`SELECT session_id FROM aliases WHERE name = ?`, name).Scan(&sid)
	return sid, err
}

func (r *Registry) SetAlias(name, sessionID string) error {
	_, err := r.db.Exec(`INSERT INTO aliases (name,session_id) VALUES (?,?) ON CONFLICT(name) DO UPDATE SET session_id=excluded.session_id`, name, sessionID)
	return err
}

func (r *Registry) ResolveTarget(target string) (string, error) {
	if sid, err := r.LookupAlias(target); err == nil {
		return sid, nil
	}
	var sid string
	if err := r.db.QueryRow(`SELECT id FROM sessions WHERE id = ? OR id LIKE ?`, target, target+"%").Scan(&sid); err == nil {
		return sid, nil
	}
	err := r.db.QueryRow(`SELECT id FROM sessions WHERE name LIKE ? OR cwd LIKE ? LIMIT 1`, "%"+target+"%", "%"+target+"%").Scan(&sid)
	return sid, err
}

func (r *Registry) CreateChannel(name string) (string, error) {
	id := fmt.Sprintf("ch_%d", time.Now().UnixNano())
	_, err := r.db.Exec(`INSERT INTO channels (id,name) VALUES (?,?)`, id, name)
	return id, err
}

func (r *Registry) AddToChannel(channelName, sessionID string) error {
	_, err := r.db.Exec(`INSERT OR IGNORE INTO channel_members (channel_id,session_id) SELECT id,? FROM channels WHERE name = ?`, sessionID, channelName)
	return err
}

func (r *Registry) AddRoute(from, to, pattern string) (int64, error) {
	res, err := r.db.Exec(`INSERT INTO routes (from_session,to_session,pattern) VALUES (?,?,?)`, from, to, pattern)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *Registry) QueueSteer(sessionID, message string) error {
	_, err := r.db.Exec(`INSERT INTO steer_queue (session_id,message) VALUES (?,?)`, sessionID, message)
	return err
}

func (r *Registry) GetSteerQueue(sessionID string) (ids []int64, msgs []string, err error) {
	rows, err := r.db.Query(`SELECT id,message FROM steer_queue WHERE session_id=? AND delivered=0 ORDER BY queued_at`, sessionID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var msg string
		if err := rows.Scan(&id, &msg); err == nil {
			ids = append(ids, id)
			msgs = append(msgs, msg)
		}
	}
	return ids, msgs, rows.Err()
}

func (r *Registry) MarkSteerDelivered(id int64) error {
	_, err := r.db.Exec(`UPDATE steer_queue SET delivered=1 WHERE id=?`, id)
	return err
}

func (r *Registry) RemoveRoute(id int64) error {
	_, err := r.db.Exec(`DELETE FROM routes WHERE id=?`, id)
	return err
}

type RouteRow struct {
	ID          int64
	FromSession string
	ToSession   string
	Pattern     string
}

func (r *Registry) ListRoutes() ([]RouteRow, error) {
	rows, err := r.db.Query(`SELECT id, from_session, to_session, pattern FROM routes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RouteRow
	for rows.Next() {
		var r RouteRow
		if err := rows.Scan(&r.ID, &r.FromSession, &r.ToSession, &r.Pattern); err == nil {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

type AliasRow struct {
	Name      string
	SessionID string
}

func (r *Registry) ListAliases() ([]AliasRow, error) {
	rows, err := r.db.Query(`SELECT name, session_id FROM aliases ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AliasRow
	for rows.Next() {
		var a AliasRow
		if err := rows.Scan(&a.Name, &a.SessionID); err == nil {
			out = append(out, a)
		}
	}
	return out, rows.Err()
}

type ChannelRow struct {
	Name        string
	MemberCount int
	Members     []string
}

func (r *Registry) ListChannels() ([]ChannelRow, error) {
	rows, err := r.db.Query(`
		SELECT c.name, COALESCE(count(cm.session_id),0) as n
		FROM channels c
		LEFT JOIN channel_members cm ON cm.channel_id = c.id
		GROUP BY c.id
		ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelRow
	for rows.Next() {
		var ch ChannelRow
		if err := rows.Scan(&ch.Name, &ch.MemberCount); err == nil {
			out = append(out, ch)
		}
	}
	for i := range out {
		members, _ := r.ChannelMembers(out[i].Name)
		out[i].Members = members
	}
	return out, nil
}

func (r *Registry) ChannelMembers(channelName string) ([]string, error) {
	rows, err := r.db.Query(`
		SELECT cm.session_id FROM channel_members cm
		JOIN channels c ON c.id = cm.channel_id
		WHERE c.name = ?`, channelName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil {
			out = append(out, s)
		}
	}
	return out, rows.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
