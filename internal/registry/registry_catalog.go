package registry

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

func (r *Registry) GetSession(id string) (surface, name, cwd string, err error) {
	err = r.read.QueryRow(`SELECT surface, name, cwd FROM sessions WHERE id = ?`, id).Scan(&surface, &name, &cwd)
	return
}

func sessionSelect(status string) string {
	return `SELECT s.id,s.surface,s.name,s.cwd,s.pid,` + status + `,s.transcript,s.has_local,s.source,s.transport,s.runtime_kind,s.configured_model,s.bridge,s.forked_from,s.last_active_ms,COALESCE(sr.runtime_launcher,''),sr.runtime_location,COALESCE(sr.runtime_focusable,0),COALESCE(cs.waiting_on,X''),COALESCE(cs.background_tasks,X''),COALESCE(cs.usage_limit,X''),` + sessionSubagentSelect
}

const sessionJoins = ` LEFT JOIN session_runtime sr ON sr.session_id=s.id LEFT JOIN catalog_sessions cs ON cs.session_id=s.id`

func scanSession(scanner rowScanner) (surface.Session, error) {
	var session surface.Session
	var kind, status string
	var hasLocal int
	var lastActiveMS int64
	var launcher string
	var location, waitingOn, background, usageLimit []byte
	var focusable int
	var subagent subagentColumns
	if err := scanner.Scan(append([]any{&session.ID, &kind, &session.Name, &session.Cwd, &session.PID, &status, &session.Transcript, &hasLocal, &session.Source, &session.Transport, &session.RuntimeKind, &session.ConfiguredModel, &session.Bridge, &session.ForkedFrom, &lastActiveMS, &launcher, &location, &focusable, &waitingOn, &background, &usageLimit}, subagent.targets()...)...); err != nil {
		return surface.Session{}, err
	}
	session.Surface = surface.SurfaceKind(kind)
	session.Status = surface.SessionStatus(status)
	session.HasLocal = hasLocal != 0
	if lastActiveMS > 0 {
		session.LastActive = time.UnixMilli(lastActiveMS)
	}
	subagent.apply(&session)
	runtime, err := runtimeFromColumns(launcher, location, focusable)
	if err != nil {
		return surface.Session{}, err
	}
	session.Runtime = runtime
	if session.WaitingOn, err = decodeCatalogWaiting(waitingOn); err != nil {
		return surface.Session{}, err
	}
	if session.BackgroundTasks, err = decodeCatalogBackground(background); err != nil {
		return surface.Session{}, err
	}
	if session.UsageLimit, err = decodeCatalogUsageLimit(usageLimit); err != nil {
		return surface.Session{}, err
	}
	return session, nil
}

func decodeCatalogWaiting(raw []byte) (*surface.Waiting, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var waiting *surface.Waiting
	if err := json.Unmarshal(raw, &waiting); err != nil {
		return nil, fmt.Errorf("parse catalog waiting state: %w", err)
	}
	return waiting, nil
}

func decodeCatalogBackground(raw []byte) (*surface.BackgroundTasks, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var background *surface.BackgroundTasks
	if err := json.Unmarshal(raw, &background); err != nil {
		return nil, fmt.Errorf("parse catalog background tasks: %w", err)
	}
	return background, nil
}

func encodeCatalogBackground(background *surface.BackgroundTasks) ([]byte, error) {
	if background == nil {
		return []byte{}, nil
	}
	return json.Marshal(background)
}

func decodeCatalogUsageLimit(raw []byte) (*surface.UsageLimit, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var limit *surface.UsageLimit
	if err := json.Unmarshal(raw, &limit); err != nil {
		return nil, fmt.Errorf("parse catalog usage limit: %w", err)
	}
	return limit, nil
}

func encodeCatalogUsageLimit(limit *surface.UsageLimit) ([]byte, error) {
	if limit == nil {
		return []byte{}, nil
	}
	return json.Marshal(limit)
}

func (r *Registry) Session(id string) (*surface.Session, error) {
	session, err := scanSession(r.read.QueryRow(sessionSelect(`s.status`)+` FROM sessions s`+sessionJoins+` WHERE s.id = ?`, id))
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *Registry) SearchSessions(kind surface.SurfaceKind, query string, limit int) ([]surface.Session, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []surface.Session{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	pattern := "%" + escapeLike(query) + "%"
	rows, err := r.read.Query(sessionSelect(`s.status`)+`
		FROM sessions s`+sessionJoins+`
		WHERE s.surface=? AND s.host_id='' AND (s.id LIKE ? ESCAPE '\' OR s.name LIKE ? ESCAPE '\' OR s.cwd LIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM aliases a WHERE a.session_id=s.id AND a.name LIKE ? ESCAPE '\'))
		ORDER BY s.last_active_ms DESC, s.updated_at DESC, s.id LIMIT ?`, string(kind), pattern, pattern, pattern, pattern, limit)
	if err != nil {
		return nil, err
	}
	return appendRows(make([]surface.Session, 0), rows, scanSession)
}

func (r *Registry) ListSessions(limit int) ([]surface.Session, error) {
	query := sessionSelect(`CASE WHEN cs.session_id IS NOT NULL THEN s.status ELSE ? END`) + ` FROM sessions s` + sessionJoins + ` WHERE s.host_id='' ORDER BY s.last_active_ms DESC, s.updated_at DESC, s.id`
	args := []any{string(surface.StatusOffline)}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.read.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return appendRows(make([]surface.Session, 0), rows, scanSession)
}

func (r *Registry) SessionUpdatedBefore(id string, before time.Time) (bool, error) {
	var stale bool
	err := r.read.QueryRow(`SELECT updated_at < datetime(?,'unixepoch') FROM sessions WHERE id=?`, before.Unix(), id).Scan(&stale)
	return stale, err
}
