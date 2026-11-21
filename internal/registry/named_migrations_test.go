package registry

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// withMigrations replaces the registered migrations for one test, which therefore must not run in parallel.
func withMigrations(t *testing.T, add ...migration) {
	t.Helper()
	saved := migrations
	migrations = nil
	t.Cleanup(func() { migrations = saved })
	for _, step := range add {
		registerMigration(step.Name, step.Run)
	}
}

func countingMigration(name string, runs *int) migration {
	return migration{Name: name, Run: func(tx *sql.Tx) error {
		*runs++
		_, err := tx.Exec(`INSERT INTO sessions(id, surface, name) VALUES(?, 'codex', ?)`, "made-by-"+name, name)
		return err
	}}
}

func openAndClose(t *testing.T, path string) {
	t.Helper()
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}

func TestTheSchemaNumberStaysFrozen(t *testing.T) {
	const frozen = 127
	path := filepath.Join(t.TempDir(), "registry.db")
	openAndClose(t, path)
	if got := schemaOf(path); schemaVersion != frozen || got != frozen {
		t.Fatalf("schemaVersion=%d and a new registry's user_version=%d; both stay at %d, the number every build before named migrations refuses. Add a dated migration instead, as docs/maintainers/registry-migrations.md describes", schemaVersion, got, frozen)
	}
}

func TestANewRegistryRecordsEveryMigrationWithoutRunningIt(t *testing.T) {
	runs := 0
	withMigrations(t, countingMigration("2026-12-01-first", &runs), countingMigration("2026-12-02-second", &runs))
	path := filepath.Join(t.TempDir(), "registry.db")
	openAndClose(t, path)
	if level := levelOf(path); runs != 0 || !slices.Equal(level.Applied, []string{"2026-12-01-first", "2026-12-02-second"}) || level.Schema != schemaVersion {
		t.Fatalf("runs=%d level=%+v", runs, level)
	}
	if backups := migrationBackups(path); len(backups) != 0 {
		t.Fatalf("a new registry kept copies %v", backups)
	}
}

func TestAnExistingRegistryRunsEachPendingMigrationOnceAfterACopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	runs := 0
	withMigrations(t, countingMigration("2026-12-01-first", &runs))
	openAndClose(t, path)
	withMigrations(t, countingMigration("2026-12-01-first", &runs), countingMigration("2026-12-03-third", &runs), countingMigration("2026-12-02-second", &runs))
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	timing, migrated := r.Migration()
	var made []string
	rows, err := r.read.Query(`SELECT name FROM sessions WHERE id LIKE 'made-by-%' ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		rows.Scan(&name)
		made = append(made, name)
	}
	rows.Close()
	r.Close()
	if !slices.Equal(made, []string{"2026-12-02-second", "2026-12-03-third"}) {
		t.Fatalf("ran %v, want the two pending migrations in name order", made)
	}
	if !migrated || timing.From != "2026-12-01-first" || timing.To != "2026-12-03-third" {
		t.Fatalf("timing=%+v migrated=%v", timing, migrated)
	}
	copied := levelOf(plainCopyOfBackup(t, path, "2026-12-03-third"))
	if !slices.Equal(copied.Applied, []string{"2026-12-01-first"}) {
		t.Fatalf("the copy is at %+v, want the level before the upgrade", copied)
	}
	openAndClose(t, path)
	if runs != 2 {
		t.Fatalf("runs=%d after a second open, want each migration once", runs)
	}
}

func TestTheFirstNamedMigrationsOnASchemaNumberedRegistryKeepTheCopyOlderBuildsRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	registryAtSchema(t, path, schemaVersion-1)
	runs := 0
	withMigrations(t, countingMigration("2026-12-01-first", &runs))
	openAndClose(t, path)
	if level := levelOf(path); runs != 1 || level.Schema != schemaVersion || !slices.Equal(level.Applied, []string{"2026-12-01-first"}) {
		t.Fatalf("runs=%d level=%+v", runs, level)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "registry-before-v"+strconv.Itoa(schemaVersion)+".db.zst")); err != nil {
		t.Fatalf("the copy is not where a schema-numbered build's restore looks: %v", err)
	}
}

func TestAFailedMigrationLeavesTheRegistryWhereItWas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	runs := 0
	withMigrations(t, countingMigration("2026-12-01-first", &runs))
	openAndClose(t, path)
	withMigrations(t, countingMigration("2026-12-01-first", &runs), countingMigration("2026-12-02-second", &runs), migration{Name: "2026-12-03-broken", Run: func(*sql.Tx) error { return errors.New("broken") }})
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "migration 2026-12-03-broken: broken") {
		t.Fatalf("err=%v", err)
	}
	if level := levelOf(path); !slices.Equal(level.Applied, []string{"2026-12-01-first"}) {
		t.Fatalf("level=%+v after a failed migration", level)
	}
	check, err := connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var made int
	if err := check.read.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id LIKE 'made-by-%'`).Scan(&made); err != nil || made != 0 {
		t.Fatalf("made=%d err=%v; the migration before the broken one must roll back with it", made, err)
	}
}

func TestARegistryWithAMigrationThisBuildDoesNotKnowIsRefusedAndRestorable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	runs := 0
	withMigrations(t, countingMigration("2026-12-01-first", &runs))
	openAndClose(t, path)
	withMigrations(t, countingMigration("2026-12-01-first", &runs), countingMigration("2026-12-02-second", &runs))
	openAndClose(t, path)
	withMigrations(t, countingMigration("2026-12-01-first", &runs))
	before := registryFiles(t, path)

	_, err := Open(path)
	var refused *UnknownMigrationsError
	if !errors.As(err, &refused) || !slices.Equal(refused.Unknown, []string{"2026-12-02-second"}) || refused.Restore != "2026-12-02-second" {
		t.Fatalf("err=%v", err)
	}
	if want := "agenthail daemon stop; agenthail registry restore 2026-12-02-second; agenthail daemon start"; !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("err=%v, want it to end %q", err, want)
	}
	if after := registryFiles(t, path); len(after) != len(before) || after["registry.db"] != before["registry.db"] {
		t.Fatalf("a refused open changed the registry")
	}
	if _, err := OpenCurrent(path); err == nil {
		t.Fatal("OpenCurrent accepted a registry with an unknown migration")
	}
	restored, err := restoreMigrationBackup(path, refused.Restore)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Level.String() != "2026-12-01-first" {
		t.Fatalf("restored=%+v", restored)
	}
	openAndClose(t, path)
}

func TestARestoreRefusesACopyWithAMigrationThisBuildDoesNotKnow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	runs := 0
	withMigrations(t, countingMigration("2026-12-01-first", &runs))
	openAndClose(t, path)
	withMigrations(t, countingMigration("2026-12-01-first", &runs), countingMigration("2026-12-02-second", &runs))
	openAndClose(t, path)
	withMigrations(t, countingMigration("2026-12-01-first", &runs), countingMigration("2026-12-02-second", &runs), countingMigration("2026-12-03-third", &runs))
	openAndClose(t, path)
	withMigrations(t)
	if _, err := restoreMigrationBackup(path, "2026-12-03-third"); err == nil || !strings.Contains(err.Error(), "2026-12-01-first") {
		t.Fatalf("err=%v, want a refusal naming the migration the copy has", err)
	}
}

func TestAMigrationNameMustBeDatedAndUnique(t *testing.T) {
	for _, names := range [][]string{{"first"}, {"2026-12-01-First"}, {"2026-12-01-"}, {"2026-12-01-a", "2026-12-01-a"}} {
		t.Run(strings.Join(names, ","), func(t *testing.T) {
			withMigrations(t)
			defer func() {
				if recover() == nil {
					t.Fatalf("%v registered", names)
				}
			}()
			for _, name := range names {
				registerMigration(name, nil)
			}
		})
	}
}

func TestARefusalAfterTwoUnknownUpgradesNamesTheCopyThisBuildCanRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	registryAtSchema(t, path, schemaVersion-1)
	runs := 0
	first := countingMigration("2026-12-01-first", &runs)
	second := countingMigration("2026-12-02-second", &runs)
	third := countingMigration("2026-12-03-third", &runs)
	withMigrations(t, first)
	openAndClose(t, path)
	withMigrations(t, first, second)
	openAndClose(t, path)
	withMigrations(t, first, second, third)
	openAndClose(t, path)
	withMigrations(t, first)
	_, err := Open(path)
	var refused *UnknownMigrationsError
	if !errors.As(err, &refused) || refused.Restore != "2026-12-02-second" {
		t.Fatalf("err=%v, want the copy from before the first unknown migration", err)
	}
	if _, err := restoreMigrationBackup(path, refused.Restore); err != nil {
		t.Fatal(err)
	}
	openAndClose(t, path)
}

func TestARefusalPrefersTheCopyFromBeforeTheUnknownMigrationOverTheSchemaNumberedOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	registryAtSchema(t, path, schemaVersion-1)
	runs := 0
	first := countingMigration("2026-12-01-first", &runs)
	withMigrations(t, first)
	openAndClose(t, path)
	withMigrations(t, first, countingMigration("2026-12-02-second", &runs))
	openAndClose(t, path)
	withMigrations(t, first)
	var refused *UnknownMigrationsError
	if _, err := Open(path); !errors.As(err, &refused) || refused.Restore != "2026-12-02-second" {
		t.Fatalf("err=%v, want the copy that keeps 2026-12-01-first", err)
	}
}

func TestARefusalOffersNoRestoreWhenEveryKeptCopyHoldsAnUnknownMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	runs := 0
	known := []migration{countingMigration("2026-12-01-first", &runs)}
	withMigrations(t, known...)
	openAndClose(t, path)
	for _, name := range []string{"2026-12-02-second", "2026-12-03-third", "2026-12-04-fourth"} {
		known = append(known, countingMigration(name, &runs))
		withMigrations(t, known...)
		openAndClose(t, path)
	}
	withMigrations(t, known[0])
	_, err := Open(path)
	var refused *UnknownMigrationsError
	if !errors.As(err, &refused) || refused.Restore != "" || strings.Contains(err.Error(), "registry restore") {
		t.Fatalf("err=%v, want a refusal that offers no copy, since each kept one holds 2026-12-02-second", err)
	}
	for _, key := range migrationBackups(path) {
		if _, err := restoreMigrationBackup(path, key); err == nil {
			t.Fatalf("copy %s restored, so the refusal should have offered it", key)
		}
	}
}

func TestUpgradesInTheSameMillisecondStayApartForARefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	registryAtSchema(t, path, schemaVersion-1)
	runs := 0
	first := countingMigration("2026-12-01-first", &runs)
	second := countingMigration("2026-12-02-second", &runs)
	third := countingMigration("2026-12-03-third", &runs)
	stopped := time.UnixMilli(1_800_000_000_000)
	openStopped := func() {
		r, err := connect(path)
		if err != nil {
			t.Fatal(err)
		}
		r.now = func() time.Time { return stopped }
		if err := r.migrate(true); err != nil {
			t.Fatal(err)
		}
		r.Close()
	}
	withMigrations(t, first)
	openStopped()
	withMigrations(t, first, second)
	openStopped()
	withMigrations(t, first, second, third)
	openStopped()
	withMigrations(t, first)
	_, err := Open(path)
	var refused *UnknownMigrationsError
	if !errors.As(err, &refused) || refused.Restore != "2026-12-02-second" {
		t.Fatalf("err=%v, want the copy from before the earlier of two upgrades in one millisecond", err)
	}
	if _, err := restoreMigrationBackup(path, refused.Restore); err != nil {
		t.Fatal(err)
	}
}
