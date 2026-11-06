package registry

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresentationChangeConditionsNameOnlyColumnsEveryHistoricalSchemaHas(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("testdata", "migrations", "v*.sql"))
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("fixtures %v: %v", fixtures, err)
	}
	for _, fixture := range fixtures {
		dump, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(dump)); err != nil {
			db.Close()
			t.Fatal(err)
		}
		for _, input := range presentationInputs {
			if !input.changed {
				continue
			}
			var exists bool
			if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name=?)`, input.table).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if !exists {
				continue
			}
			for column := range strings.SplitSeq(input.columns, ",") {
				var found bool
				if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name=?)`, input.table, column).Scan(&found); err != nil {
					t.Fatal(err)
				}
				if !found {
					t.Errorf("%s: %s.%s is in a change condition but this schema lacks it", filepath.Base(fixture), input.table, column)
				}
			}
		}
		db.Close()
	}
}
