package registry

import (
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

// recordRuntimeKinds is schema step 16. Every session row without a runtime
// kind gets the runtime that carries it, derived from the attachments it was
// registered with, in one transaction with the column: before this step each
// surface had exactly one runtime, and a Codex thread's runtime followed its
// transport. It runs on every open because a registry stamped by a newer build
// can still lack the column.
func (r *Registry) recordRuntimeKinds() error {
	found, err := r.columnExists("sessions", "runtime_kind")
	if err != nil {
		return err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !found {
		if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN runtime_kind TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`UPDATE sessions SET runtime_kind=CASE surface
		WHEN '%s' THEN '%s'
		WHEN '%s' THEN CASE WHEN transport='managed' THEN '%s' ELSE '%s' END
		WHEN '%s' THEN '%s'
		WHEN 'agenthail' THEN '%s'
		ELSE '' END
		WHERE runtime_kind=''`,
		surface.KindClaude, surface.RuntimeClaudeCLI,
		surface.KindCodex, surface.RuntimeCodexAppServer, surface.RuntimeCodexDesktop,
		surface.KindNotion, surface.RuntimeNotionWeb,
		surface.RuntimeOperator)); err != nil {
		return err
	}
	return tx.Commit()
}
