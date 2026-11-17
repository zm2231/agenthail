package registry

import "github.com/zm2231/agenthail/internal/surface"

// The register upsert and the catalog's unchanged check must agree on these,
// or a row the upsert leaves alone is rewritten on every pass.
const (
	sessionKeepsRouteSQL = `(sessions.surface='codex' AND (
		(sessions.transport='managed' AND NOT (excluded.source='vscode' AND excluded.transport='desktop'))
		OR (sessions.transport='desktop' AND excluded.source='agenthail' AND excluded.transport='managed')))`
	// A process cannot change runtime, so a write that calls the Claude
	// process the host runs a terminal one is reading a listing taken before
	// the host listed it. A later process given the same pid has another
	// identity; one that could not be read is taken as the same process.
	sessionKeepsHostedSQL = `(sessions.runtime_kind='` + string(surface.RuntimeClaudeHost) + `' AND excluded.runtime_kind='` + string(surface.RuntimeClaudeCLI) + `'
		AND excluded.pid>0 AND excluded.pid=sessions.pid
		AND (excluded.pid_identity='' OR sessions.pid_identity='' OR excluded.pid_identity=sessions.pid_identity))`
	sessionRouteSourceSQL    = `CASE WHEN ` + sessionKeepsRouteSQL + ` THEN sessions.source WHEN excluded.source<>'' THEN excluded.source ELSE sessions.source END`
	sessionRouteTransportSQL = `CASE WHEN ` + sessionKeepsRouteSQL + ` THEN sessions.transport WHEN excluded.transport<>'' THEN excluded.transport ELSE sessions.transport END`
	sessionRouteRuntimeSQL   = `CASE WHEN ` + sessionKeepsRouteSQL + ` OR ` + sessionKeepsHostedSQL + ` THEN sessions.runtime_kind ELSE excluded.runtime_kind END`
)
