package registry

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentTurnEndsLookUpEachSessionsLatestTurnInsteadOfScanningEveryTurn(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for s := 0; s < 200; s++ {
		id := fmt.Sprintf("session-%d", s)
		statements := [][]any{
			{`INSERT INTO sessions (id,surface) VALUES (?,'claude')`, id},
			{`INSERT INTO session_runtime (session_id,completed_turn_id) VALUES (?,?)`, id, fmt.Sprintf("turn-%d-2", s)},
		}
		for n := 0; n < 3; n++ {
			statements = append(statements, []any{`INSERT INTO turn_ends (session_id,started_at_ms,ended_at_ms,completed_turn_id) VALUES (?,?,?,?)`, id, 1000 + n*10, 1005 + n*10, fmt.Sprintf("turn-%d-%d", s, n)})
		}
		for _, statement := range statements {
			if _, err := tx.Exec(statement[0].(string), statement[1:]...); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, step := range queryPlan(t, r, currentTurnEndsQuery) {
		if strings.HasPrefix(step, "SCAN te") {
			t.Errorf("current turn ends scan every turn ever kept: %q", step)
		}
	}
	if ends, err := r.CurrentTurnEnds(); err != nil || len(ends) != 200 || ends["session-7"].CompletedTurnID != "turn-7-2" {
		t.Fatalf("current turn ends: %d, %+v, %v", len(ends), ends["session-7"], err)
	}
}
