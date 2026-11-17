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

func (c sessionWriteColumn) guarded() string {
	if c.name == "runtime_kind" {
		return sessionRouteRuntimeGuardSQL
	}
	return c.update
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
	{"pid_identity", `CASE WHEN excluded.pid_identity='' AND excluded.pid=sessions.pid THEN sessions.pid_identity ELSE excluded.pid_identity END`, func(s surface.Session) any { return s.PIDIdentity }},
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

var sessionUpsertSQL, sessionUnchangedSQL, sessionExcludedSQL = buildSessionWriteSQL()

func buildSessionWriteSQL() (upsert, unchanged, excluded string) {
	names := make([]string, 0, len(sessionWriteColumns))
	sets := make([]string, 0, len(sessionWriteColumns))
	bound := make([]string, 0, len(sessionWriteColumns))
	for _, column := range sessionWriteColumns {
		names = append(names, column.name)
		sets = append(sets, column.name+"="+column.update)
		bound = append(bound, "? AS "+column.name)
	}
	upsert = `INSERT INTO sessions (id,` + strings.Join(names, ",") + `,updated_at) VALUES (?` + strings.Repeat(",?", len(names)) + `,datetime('now'))
		ON CONFLICT(id) DO UPDATE SET ` + strings.Join(sets, ",") + `,updated_at=datetime('now')`
	excluded = `(SELECT ? AS id,` + strings.Join(bound, ",") + `) AS excluded`
	// A listing that calls a hosted process a terminal one is never
	// unchanged, so its write reaches the transaction that refuses it.
	unchanged = `SELECT ` + sessionColumnsSameSQL(sessionWriteColumns) + ` AND NOT ` + sessionKeepsHostedSQL + ` FROM sessions, ` + excluded + ` WHERE sessions.id=excluded.id`
	return upsert, unchanged, excluded
}

func sessionColumnsSameSQL(columns []sessionWriteColumn) string {
	compared := make([]string, 0, len(columns)+1)
	for _, column := range columns {
		compared = append(compared, "("+column.update+") IS sessions."+column.name)
	}
	if len(compared) == 0 {
		return "1"
	}
	return strings.Join(compared, " AND ")
}

// ownedSessionUpdateSQL writes owns from the first bound session, bound as
// next, and only while each guard holds what registering the second, the row
// the writer read, would leave it, or already what this write leaves it.
func ownedSessionUpdateSQL(owns, guards []sessionWriteColumn) string {
	next := func(update string) string { return strings.ReplaceAll(update, "excluded.", "next.") }
	sets := make([]string, 0, len(owns)+1)
	for _, column := range owns {
		sets = append(sets, column.name+"="+next(column.update))
	}
	sets = append(sets, `updated_at=datetime('now')`)
	held := make([]string, 0, len(guards)+1)
	for _, column := range guards {
		held = append(held, ownedSessionGuardSQL(column))
	}
	if len(held) == 0 {
		held = append(held, "1")
	}
	return `UPDATE sessions SET ` + strings.Join(sets, ",") + ` FROM ` + strings.Replace(sessionExcludedSQL, ") AS excluded", ") AS next", 1) + ` WHERE sessions.id=next.id
		AND EXISTS (SELECT 1 FROM ` + sessionExcludedSQL + ` WHERE sessions.id=excluded.id AND ` + strings.Join(held, " AND ") + `)`
}

func ownedSessionGuardSQL(column sessionWriteColumn) string {
	return "((" + column.guarded() + ") IS sessions." + column.name + " OR (" + strings.ReplaceAll(column.guarded(), "excluded.", "next.") + ") IS sessions." + column.name + ")"
}

func sessionWriteValues(s surface.Session) []any {
	values := make([]any, 0, 1+len(sessionWriteColumns))
	values = append(values, s.ID)
	for _, column := range sessionWriteColumns {
		values = append(values, column.value(s))
	}
	return values
}
