package registry

import (
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

type sessionWriteColumn struct {
	name   string
	update string
	value  func(surface.Session) any
}

func keepStoredUnlessSet(column string) string {
	return `CASE WHEN excluded.` + column + `<>'' THEN excluded.` + column + ` ELSE sessions.` + column + ` END`
}

// The register upsert and the catalog's unchanged check are both built from this list.
var sessionWriteColumns = []sessionWriteColumn{
	{"surface", "excluded.surface", func(s surface.Session) any { return string(s.Surface) }},
	{"name", "excluded.name", func(s surface.Session) any { return s.Name }},
	{"cwd", "excluded.cwd", func(s surface.Session) any { return s.Cwd }},
	{"pid", "excluded.pid", func(s surface.Session) any { return s.PID }},
	{"status", "excluded.status", func(s surface.Session) any { return string(s.Status) }},
	{"ended", "excluded.ended", func(s surface.Session) any { return b2i(sessionEnded(s)) }},
	{"transcript", "excluded.transcript", func(s surface.Session) any { return s.Transcript }},
	{"has_local", "excluded.has_local", func(s surface.Session) any { return b2i(s.HasLocal) }},
	{"source", sessionRouteSourceSQL, func(s surface.Session) any { return s.Source }},
	{"transport", sessionRouteTransportSQL, func(s surface.Session) any { return s.Transport }},
	{"runtime_kind", sessionRouteRuntimeSQL, func(s surface.Session) any { return string(s.RuntimeKind) }},
	{"configured_model", keepStoredUnlessSet("configured_model"), func(s surface.Session) any { return s.ConfiguredModel }},
	{"bridge", "excluded.bridge", func(s surface.Session) any { return s.Bridge }},
	{"forked_from", `CASE WHEN sessions.forked_from<>'' THEN sessions.forked_from ELSE excluded.forked_from END`, func(s surface.Session) any { return s.ForkedFrom }},
	{"last_active_ms", "excluded.last_active_ms", func(s surface.Session) any {
		if s.LastActive.IsZero() {
			return int64(0)
		}
		return s.LastActive.UnixMilli()
	}},
	{"parent_session_id", keepStoredUnlessSet("parent_session_id"), func(s surface.Session) any { parent, _, _, _, _ := subagentValues(s); return parent }},
	{"root_session_id", keepStoredUnlessSet("root_session_id"), func(s surface.Session) any { _, root, _, _, _ := subagentValues(s); return root }},
	{"subagent_depth", `CASE WHEN excluded.parent_session_id<>'' THEN excluded.subagent_depth ELSE sessions.subagent_depth END`, func(s surface.Session) any { _, _, depth, _, _ := subagentValues(s); return depth }},
	{"agent_nickname", keepStoredUnlessSet("agent_nickname"), func(s surface.Session) any { _, _, _, nickname, _ := subagentValues(s); return nickname }},
	{"agent_role", keepStoredUnlessSet("agent_role"), func(s surface.Session) any { _, _, _, _, role := subagentValues(s); return role }},
}

var sessionUpsertSQL, sessionUnchangedSQL = buildSessionWriteSQL()

func buildSessionWriteSQL() (upsert, unchanged string) {
	names := make([]string, 0, len(sessionWriteColumns))
	sets := make([]string, 0, len(sessionWriteColumns))
	same := make([]string, 0, len(sessionWriteColumns))
	bound := make([]string, 0, len(sessionWriteColumns))
	for _, column := range sessionWriteColumns {
		names = append(names, column.name)
		sets = append(sets, column.name+"="+column.update)
		same = append(same, "("+column.update+") IS sessions."+column.name)
		bound = append(bound, "? AS "+column.name)
	}
	upsert = `INSERT INTO sessions (id,` + strings.Join(names, ",") + `,updated_at) VALUES (?` + strings.Repeat(",?", len(names)) + `,datetime('now'))
		ON CONFLICT(id) DO UPDATE SET ` + strings.Join(sets, ",") + `,updated_at=datetime('now')`
	unchanged = `SELECT ` + strings.Join(same, " AND ") + ` FROM sessions, (SELECT ? AS id,` + strings.Join(bound, ",") + `) AS excluded WHERE sessions.id=excluded.id`
	return upsert, unchanged
}

func sessionWriteValues(s surface.Session) []any {
	values := make([]any, 0, 1+len(sessionWriteColumns))
	values = append(values, s.ID)
	for _, column := range sessionWriteColumns {
		values = append(values, column.value(s))
	}
	return values
}
