package registry

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
)

func TestASchema116RegistryUpgradesToIndexedOpenQueueReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`DROP INDEX message_queue_open`, `PRAGMA user_version=116`} {
		if _, err := old.db.Exec(statement); err != nil {
			old.Close()
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	now := time.Now()
	work := awaitingTurnArgs(now)
	watchedArgs := append(append(append([]any{}, work...), work...), now.Add(-TurnEndWatch).UnixMilli())
	for _, read := range []struct {
		name  string
		query string
		args  []any
	}{
		{"queue counts", queueCountsQuery, nil},
		{"watched sessions", watchedSessionsQuery, watchedArgs},
		{"one session's queue", `SELECT COUNT(*) FROM message_queue WHERE session_id=? AND status IN ('pending','inflight')`, []any{"s1"}},
		{"claim check", claimCheckQuery(claimCandidateFilter("", deliverypolicy.Queue)), []any{"s1", now.UnixMilli()}},
		{"steer claim check", claimCheckQuery(claimCandidateFilter("steer", deliverypolicy.Steer)), []any{"s1", now.UnixMilli()}},
	} {
		for _, step := range queryPlan(t, r, read.query, read.args...) {
			if strings.Contains(step, "message_queue") && !strings.Contains(step, "USING INDEX message_queue_open") {
				t.Errorf("%s reads message_queue without the open-row index: %q", read.name, step)
			}
		}
	}
}

func queryPlan(t *testing.T, r *Registry, query string, args ...any) []string {
	t.Helper()
	rows, err := r.db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var steps []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatalf("no plan for %s", query)
	}
	return steps
}
