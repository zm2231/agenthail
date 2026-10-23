package registry

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// keptMigrationBackups is how many pre-migration copies stay beside a
// registry, the newest schemas first.
const keptMigrationBackups = 2

// DevUpgradeError refuses a development build's upgrade of the default
// registry: the installed agenthail would then refuse the schema it left.
type DevUpgradeError struct {
	Path   string
	Found  int
	Target int
}

func (e *DevUpgradeError) Error() string {
	return fmt.Sprintf("this is a development build: it would migrate %s from schema %d to %d, which the installed agenthail then refuses. Run it with a scratch HOME, or set AGENTHAIL_ALLOW_DEV_MIGRATION=1 to migrate (a copy goes to %s first)", e.Path, e.Found, e.Target, migrationBackupPath(e.Path, e.Target))
}

// migrationBackupPath is the copy of the registry at path taken before it
// was migrated to schema target.
func migrationBackupPath(path string, target int) string {
	stem := strings.TrimSuffix(path, filepath.Ext(path))
	return fmt.Sprintf("%s-before-v%d.db", stem, target)
}

// backUpBeforeMigration copies the registry, which the caller holds the
// write lock on at schema found, before it is migrated to target. The copy
// is made on its own connection, since VACUUM cannot run inside the lock's
// transaction, and is checked to hold schema found before it takes its name.
func (r *Registry) backUpBeforeMigration(found, target int) error {
	final := migrationBackupPath(r.diskPath(), target)
	temporary := final + ".tmp"
	_ = os.Remove(temporary)
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("back up the registry before migrating it: %w", err)
	}
	file.Close()
	defer os.Remove(temporary)
	source, err := sql.Open("sqlite", r.path+r.querySeparator()+"_pragma=busy_timeout(15000)")
	if err != nil {
		return fmt.Errorf("back up the registry before migrating it: %w", err)
	}
	defer source.Close()
	if _, err := source.Exec(`VACUUM INTO ?`, temporary); err != nil {
		return fmt.Errorf("back up the registry to %s before migrating it: %w", final, err)
	}
	if copied := schemaOf(temporary); copied != found {
		return fmt.Errorf("back up the registry before migrating it: the copy is schema %d, want %d", copied, found)
	}
	if err := os.Rename(temporary, final); err != nil {
		return fmt.Errorf("back up the registry before migrating it: %w", err)
	}
	pruneMigrationBackups(r.diskPath())
	return nil
}

// schemaOf is the user_version of the registry file at path, or -1 when it
// cannot be read.
func schemaOf(path string) int {
	db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return -1
	}
	defer db.Close()
	version := -1
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return -1
	}
	return version
}

func pruneMigrationBackups(path string) {
	backups := migrationBackups(path)
	for _, target := range backups[min(len(backups), keptMigrationBackups):] {
		_ = os.Remove(migrationBackupPath(path, target))
	}
}

// migrationBackups lists the schemas a pre-migration copy beside path was
// taken for, newest first.
func migrationBackups(path string) []int {
	stem := strings.TrimSuffix(path, filepath.Ext(path))
	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(filepath.Base(stem)) + `-before-v([0-9]+)\.db$`)
	entries, _ := os.ReadDir(filepath.Dir(path))
	var targets []int
	for _, entry := range entries {
		if match := pattern.FindStringSubmatch(entry.Name()); match != nil {
			if target, err := strconv.Atoi(match[1]); err == nil {
				targets = append(targets, target)
			}
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(targets)))
	return targets
}

// diskPath is the registry's file without connection parameters.
func (r *Registry) diskPath() string {
	path, _, _ := strings.Cut(r.path, "?")
	return path
}

func (r *Registry) querySeparator() string {
	if strings.Contains(r.path, "?") {
		return "&"
	}
	return "?"
}
