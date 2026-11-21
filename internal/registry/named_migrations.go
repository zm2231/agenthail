package registry

import (
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Run sees the converged schema and is skipped on a registry created in the same open.
type migration struct {
	Name string
	Run  func(*sql.Tx) error
}

var (
	migrationName = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}-[a-z0-9]+(?:-[a-z0-9]+)*$`)
	migrations    []migration
)

func registerMigration(name string, run func(*sql.Tx) error) {
	if !migrationName.MatchString(name) {
		panic(fmt.Sprintf("registry migration %q is not named yyyy-mm-dd-slug", name))
	}
	for _, known := range migrations {
		if known.Name == name {
			panic(fmt.Sprintf("registry migration %q is registered twice", name))
		}
	}
	migrations = append(migrations, migration{Name: name, Run: run})
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Name < migrations[j].Name })
}

func knownMigrationNames() []string {
	names := make([]string, len(migrations))
	for i, known := range migrations {
		names[i] = known.Name
	}
	return names
}

type queryer interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func appliedMigrations(db queryer) ([]string, error) {
	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&tables); err != nil {
		return nil, err
	}
	if tables == 0 {
		return nil, nil
	}
	rows, err := db.Query(`SELECT name FROM schema_migrations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func without(from, these []string) []string {
	var left []string
	for _, name := range from {
		if !slices.Contains(these, name) {
			left = append(left, name)
		}
	}
	return left
}

func pendingMigrations(applied []string) []migration {
	var pending []migration
	for _, known := range migrations {
		if !slices.Contains(applied, known.Name) {
			pending = append(pending, known)
		}
	}
	return pending
}

// Each upgrade's time is later than any recorded, so a refusal can tell its batch from the next.
func recordMigrations(tx *sql.Tx, done []migration, appliedAtMS int64) error {
	var latest int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(applied_at_ms), 0) FROM schema_migrations`).Scan(&latest); err != nil {
		return err
	}
	appliedAtMS = max(appliedAtMS, latest+1)
	for _, ran := range done {
		if _, err := tx.Exec(`INSERT INTO schema_migrations(name, applied_at_ms) VALUES(?, ?)`, ran.Name, appliedAtMS); err != nil {
			return fmt.Errorf("record migration %s: %w", ran.Name, err)
		}
	}
	return nil
}

func runMigrations(tx *sql.Tx, pending []migration) error {
	for _, step := range pending {
		if step.Run == nil {
			continue
		}
		if err := step.Run(tx); err != nil {
			return fmt.Errorf("migration %s: %w", step.Name, err)
		}
	}
	return nil
}

type UnknownMigrationsError struct {
	Path    string
	Unknown []string
	Restore string
}

func (e *UnknownMigrationsError) Error() string {
	refusal := fmt.Sprintf("registry has migrations this agenthail does not know (%s); install a newer agenthail", strings.Join(e.Unknown, ", "))
	if e.Restore == "" {
		return refusal
	}
	return fmt.Sprintf("%s, or go back to the copy taken before %s (anything changed since is lost): agenthail daemon stop; agenthail registry restore %s; agenthail daemon start", refusal, e.Restore, e.Restore)
}

// One upgrade records all its names at one time, so only the copy keyed by a name from the batch that brought the first unknown one holds none of them.
func unknownMigrationsError(db queryer, path string, unknown []string) (*UnknownMigrationsError, error) {
	refusal := &UnknownMigrationsError{Path: path, Unknown: unknown}
	appliedAt := map[string]int64{}
	rows, err := db.Query(`SELECT name, applied_at_ms FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	first := int64(-1)
	for rows.Next() {
		var name string
		var at int64
		if err := rows.Scan(&name, &at); err != nil {
			return nil, err
		}
		appliedAt[name] = at
		if slices.Contains(unknown, name) && (first < 0 || at < first) {
			first = at
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, key := range migrationBackups(path) {
		if at, ok := appliedAt[key]; ok && slices.Contains(unknown, key) && at == first {
			refusal.Restore = key
			return refusal, nil
		}
		if key == schemaBackupKey(schemaVersion) {
			refusal.Restore = key
		}
	}
	return refusal, nil
}
