package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

func (r *Registry) CanonicalSession(ref string) (id, hostID string, found bool, err error) {
	err = r.read.QueryRow(`SELECT id,host_id FROM sessions WHERE id=?`, ref).Scan(&id, &hostID)
	if err == nil {
		return id, hostID, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", false, err
	}
	if _, err := uuid.Parse(ref); err != nil {
		return "", "", false, nil
	}
	rows, err := r.read.Query(`SELECT id,surface,host_id FROM sessions`)
	if err != nil {
		return "", "", false, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		if err := rows.Scan(&id, &kind, &hostID); err != nil {
			return "", "", false, err
		}
		if strings.EqualFold(surface.PeerUUID(surface.SurfaceKind(kind), id), ref) {
			return id, hostID, true, nil
		}
	}
	return "", "", false, rows.Err()
}

func (r *Registry) ResolveTarget(target string) (string, error) {
	if path, ok := strings.CutPrefix(target, "@"); ok {
		if strings.Contains(path, "/") {
			return r.resolveSubagentPath(path)
		}
		return r.ExactTarget("", path)
	}
	return r.resolveTarget(target, "")
}

func (r *Registry) ResolveSurfaceTarget(kind surface.SurfaceKind, target string) (string, error) {
	if name, ok := strings.CutPrefix(target, "@"); ok {
		return r.ExactTarget(kind, name)
	}
	return r.resolveTarget(target, string(kind))
}

func (r *Registry) resolveTarget(target, kind string) (string, error) {
	if sid, err := r.ExactTarget(surface.SurfaceKind(kind), target); err == nil {
		return sid, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if ids, err := r.idPrefixMatches(target, kind, 2); err != nil {
		return "", err
	} else if len(ids) == 1 {
		return ids[0], nil
	} else if len(ids) > 1 {
		return "", fmt.Errorf("ambiguous session id prefix %q matches %s", target, strings.Join(ids, ", "))
	}
	ids, err := r.nameOrCwdMatches(target, kind, 2)
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", sql.ErrNoRows
	}
	if len(ids) > 1 {
		return "", fmt.Errorf("ambiguous session target %q matches %s", target, strings.Join(ids, ", "))
	}
	return ids[0], nil
}

func (r *Registry) NamesSession(session surface.Session, selector string) bool {
	if path, ok := strings.CutPrefix(selector, "@"); ok {
		if parent, _, isPath := strings.Cut(path, "/"); isPath {
			_, err := r.ExactTarget("", parent)
			return err == nil
		}
	}
	if surface.IdentifiedBy(session, strings.TrimPrefix(selector, "@")) {
		return true
	}
	sid, err := r.ExactTarget(session.Surface, strings.TrimPrefix(selector, "@"))
	return err == nil && sid == session.ID
}

func (r *Registry) FragmentMatches(kind surface.SurfaceKind, target string, limit int) ([]string, error) {
	if target == "" {
		return nil, nil
	}
	prefixed, err := r.idPrefixMatches(target, string(kind), limit)
	if err != nil {
		return nil, err
	}
	named, err := r.nameOrCwdMatches(target, string(kind), limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	seen := map[string]bool{}
	for _, id := range append(prefixed, named...) {
		if !seen[id] && len(ids) < limit {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (r *Registry) idPrefixMatches(target, kind string, limit int) ([]string, error) {
	return r.matchingIDs(`SELECT id FROM sessions WHERE host_id='' AND id LIKE ? ESCAPE '\' AND (? = '' OR surface = ?) ORDER BY updated_at DESC, id LIMIT ?`, escapeLike(target)+"%", kind, kind, limit)
}

func (r *Registry) nameOrCwdMatches(target, kind string, limit int) ([]string, error) {
	contains := "%" + escapeLike(target) + "%"
	return r.matchingIDs(`SELECT id FROM sessions WHERE host_id='' AND parent_session_id='' AND (name LIKE ? ESCAPE '\' OR cwd LIKE ? ESCAPE '\') AND (? = '' OR surface = ?) ORDER BY updated_at DESC, id LIMIT ?`, contains, contains, kind, kind, limit)
}

func (r *Registry) localSessionSurface(id string) (string, bool) {
	var kind string
	err := r.read.QueryRow(`SELECT surface FROM sessions WHERE id = ? AND host_id=''`, id).Scan(&kind)
	return kind, err == nil
}

func (r *Registry) ExactTarget(kind surface.SurfaceKind, target string) (string, error) {
	if sid, err := r.LookupAlias(strings.TrimPrefix(target, "@")); err == nil {
		if aliased, local := r.localSessionSurface(sid); local && (kind == "" || aliased == string(kind)) {
			return sid, nil
		}
	}
	var exact string
	err := r.read.QueryRow(`SELECT id FROM sessions WHERE id = ? AND host_id='' AND (? = '' OR surface = ?)`, target, string(kind), string(kind)).Scan(&exact)
	if err == nil || !errors.Is(err, sql.ErrNoRows) || target == "" || (kind != "" && kind != surface.KindClaude) {
		return exact, err
	}
	err = r.read.QueryRow(`SELECT id FROM sessions WHERE surface=? AND host_id='' AND transcript LIKE ? ESCAPE '\' ORDER BY updated_at DESC, rowid DESC LIMIT 1`, string(surface.KindClaude), "%/"+escapeLike(target)+".jsonl").Scan(&exact)
	return exact, err
}

func (r *Registry) resolveSubagentPath(path string) (string, error) {
	segments := strings.Split(path, "/")
	for _, segment := range segments {
		if strings.TrimSpace(segment) == "" {
			return "", fmt.Errorf("invalid subagent target %q: use @<parent>/<nickname>", "@"+path)
		}
	}
	current, err := r.ResolveTarget(segments[0])
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("subagent target %q: no session matched %q", "@"+path, segments[0])
	}
	if err != nil {
		return "", fmt.Errorf("subagent target %q: %w", "@"+path, err)
	}
	for _, segment := range segments[1:] {
		rows, err := r.read.Query(`SELECT id,agent_nickname FROM sessions WHERE parent_session_id=? ORDER BY id`, current)
		if err != nil {
			return "", err
		}
		var nicknameMatches, idMatches, prefixMatches []string
		for rows.Next() {
			var id, nickname string
			if err := rows.Scan(&id, &nickname); err != nil {
				rows.Close()
				return "", err
			}
			switch {
			case id == segment:
				idMatches = append(idMatches, id)
			case nickname != "" && strings.EqualFold(nickname, segment):
				nicknameMatches = append(nicknameMatches, id)
			case strings.HasPrefix(id, segment):
				prefixMatches = append(prefixMatches, id)
			}
		}
		if err := rows.Close(); err != nil {
			return "", err
		}
		var matches []string
		switch {
		case len(idMatches) > 0:
			matches = idMatches
		case len(nicknameMatches) > 0:
			matches = nicknameMatches
		default:
			matches = prefixMatches
		}
		if len(matches) == 0 {
			return "", fmt.Errorf("subagent target %q: %s has no subagent %q", "@"+path, current, segment)
		}
		if len(matches) > 1 {
			return "", fmt.Errorf("ambiguous subagent target %q: %q matches %s; use a subagent ID", "@"+path, segment, strings.Join(matches, ", "))
		}
		current = matches[0]
	}
	return current, nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

type NamedSession struct {
	ID    string
	Alias string
}

func (r *Registry) SessionsNamed(kind surface.SurfaceKind, name string) ([]NamedSession, error) {
	rows, err := r.read.Query(`SELECT s.id, COALESCE((SELECT a.name FROM aliases a WHERE a.session_id = s.id ORDER BY a.created_at LIMIT 1),'') FROM sessions s WHERE s.host_id='' AND s.parent_session_id='' AND s.surface = ? AND s.name = ? ORDER BY s.updated_at DESC, s.id`, string(kind), name)
	if err != nil {
		return nil, err
	}
	return appendRows(nil, rows, func(row rowScanner) (NamedSession, error) {
		var session NamedSession
		err := row.Scan(&session.ID, &session.Alias)
		return session, err
	})
}

func (r *Registry) matchingIDs(query string, args ...any) ([]string, error) {
	return queryRows(r.read, nil, scanString, query, args...)
}
