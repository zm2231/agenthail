package registry

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEveryHistoricalRegistryEndsWithTheTriggersAFreshOneHas(t *testing.T) {
	want := presentationTriggers(t, openTestRegistry(t))
	if len(want) != 3*len(presentationInputs) {
		t.Fatalf("a fresh registry has %d presentation triggers, want %d", len(want), 3*len(presentationInputs))
	}
	fixtures, err := filepath.Glob(filepath.Join("testdata", "migrations", "v*.sql"))
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("fixtures %v: %v", fixtures, err)
	}
	for _, fixture := range fixtures {
		name := strings.TrimSuffix(filepath.Base(fixture), ".sql")
		got := presentationTriggers(t, openMigrationFixture(t, name))
		for trigger, sql := range want {
			if got[trigger] != sql {
				t.Errorf("%s: %s is\n%s\nwant\n%s", name, trigger, got[trigger], sql)
			}
		}
		if len(got) != len(want) {
			t.Errorf("%s: %d presentation triggers, want %d", name, len(got), len(want))
		}
	}
}

func presentationTriggers(t *testing.T, r *Registry) map[string]string {
	t.Helper()
	rows, err := r.db.Query(`SELECT name,sql FROM sqlite_schema WHERE type='trigger' AND name LIKE 'catalog_presentation_version_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	triggers := map[string]string{}
	for rows.Next() {
		var name, sql string
		if err := rows.Scan(&name, &sql); err != nil {
			t.Fatal(err)
		}
		triggers[name] = sql
	}
	return triggers
}

// asBeforePresentationTriggers leaves a registry as one from before its
// columns were watched, so a test can drop a column to stand in for an older
// schema.
func asBeforePresentationTriggers(t *testing.T, r *Registry) {
	t.Helper()
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := dropPresentationTriggers(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
