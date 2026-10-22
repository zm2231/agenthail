package registry

import (
	"path/filepath"
	"testing"
)

func TestARegistryAtSchema30GainsAnswerableWaitsAndUsageLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE session_runtime DROP COLUMN waiting_answerable`,
		`ALTER TABLE catalog_sessions DROP COLUMN usage_limit`,
		`PRAGMA user_version = 30`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	r.Close()

	if current, err := OpenCurrent(path); err == nil {
		current.Close()
		t.Fatal("a hook opened a schema 30 registry that lacks the columns this build reads")
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for table, column := range map[string]string{"session_runtime": "waiting_answerable", "catalog_sessions": "usage_limit"} {
		var found int
		if err := r.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&found); err != nil || found != 1 {
			t.Fatalf("%s.%s found=%d err=%v", table, column, found, err)
		}
	}
}
