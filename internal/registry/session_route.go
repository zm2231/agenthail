package registry

// The register upsert and the catalog's unchanged check must agree on these,
// or a row the upsert leaves alone is rewritten on every pass.
const (
	sessionKeepsRouteSQL = `(sessions.surface='codex' AND (
		(sessions.transport='managed' AND NOT (excluded.source='vscode' AND excluded.transport='desktop'))
		OR (sessions.transport='desktop' AND excluded.source='agenthail' AND excluded.transport='managed')))`
	sessionRouteSourceSQL    = `CASE WHEN ` + sessionKeepsRouteSQL + ` THEN sessions.source WHEN excluded.source<>'' THEN excluded.source ELSE sessions.source END`
	sessionRouteTransportSQL = `CASE WHEN ` + sessionKeepsRouteSQL + ` THEN sessions.transport WHEN excluded.transport<>'' THEN excluded.transport ELSE sessions.transport END`
	sessionRouteRuntimeSQL   = `CASE WHEN ` + sessionKeepsRouteSQL + ` THEN sessions.runtime_kind ELSE excluded.runtime_kind END`
)
