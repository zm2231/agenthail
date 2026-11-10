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
	return `SELECT ` + sessionColumnsSQL(status)
}

const sessionJoins = ` LEFT JOIN session_runtime sr ON sr.session_id=s.id LEFT JOIN catalog_sessions cs ON cs.session_id=s.id`

func scanSession(scanner rowScanner) (surface.Session, error) {
	var columns sessionScan
	if err := scanner.Scan(columns.targets()...); err != nil {
		return surface.Session{}, err
	}
	return columns.result()
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

func decodeCatalogTurnStop(raw []byte) (*surface.TurnStop, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var stop *surface.TurnStop
	if err := json.Unmarshal(raw, &stop); err != nil {
		return nil, fmt.Errorf("parse catalog turn stop: %w", err)
	}
	return stop, nil
}

func decodeCatalogSettings(raw []byte) (*surface.ObservedSettings, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var settings *surface.ObservedSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("parse catalog settings: %w", err)
	}
	return settings, nil
}

func encodeCatalogSettings(settings *surface.ObservedSettings) ([]byte, error) {
	if settings == nil {
		return []byte{}, nil
	}
	return json.Marshal(settings)
}

func encodeCatalogTurnStop(stop *surface.TurnStop) ([]byte, error) {
	if stop == nil {
		return []byte{}, nil
	}
	return json.Marshal(stop)
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
		WHERE s.surface=? AND s.host_id='' AND NOT `+hiddenSessionRow("s")+` AND (s.id LIKE ? ESCAPE '\' OR s.name LIKE ? ESCAPE '\' OR s.cwd LIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM aliases a WHERE a.session_id=s.id AND a.name LIKE ? ESCAPE '\'))
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
