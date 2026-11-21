package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"
)

// keptMigrationBackups is how many pre-migration copies stay beside a
// registry, the newest schemas first.
const keptMigrationBackups = 2

// DevUpgradeError refuses a development build's upgrade of the default
// registry: the installed agenthail would then refuse the schema it left.
type DevUpgradeError struct {
	Path   string
	Found  string
	Target string
	Key    string
}

func (e *DevUpgradeError) Error() string {
	return fmt.Sprintf("this is a development build: it would migrate %s from %s to %s, which the installed agenthail then refuses. Run it with a scratch HOME, or set AGENTHAIL_ALLOW_DEV_MIGRATION=1 to migrate (a copy goes to %s first)", e.Path, e.Found, e.Target, migrationBackupPath(e.Path, e.Key))
}

// A copy is keyed by the upgrade it preceded: v<N> for a schema number, or the newest migration that upgrade applied.
var backupKey = regexp.MustCompile(`^(?:v[0-9]+|[0-9]{4}-[0-9]{2}-[0-9]{2}-[a-z0-9]+(?:-[a-z0-9]+)*)$`)

func ValidBackupKey(key string) bool { return backupKey.MatchString(key) }

func schemaBackupKey(version int) string { return "v" + strconv.Itoa(version) }

// Named keys sort after every schema number, since the numbers stopped at the first named migration.
func newerBackupKey(a, b string) bool {
	aSchema, bSchema := strings.HasPrefix(a, "v"), strings.HasPrefix(b, "v")
	if aSchema != bSchema {
		return bSchema
	}
	if aSchema {
		an, _ := strconv.Atoi(a[1:])
		bn, _ := strconv.Atoi(b[1:])
		return an > bn
	}
	return a > b
}

func migrationBackupPath(path, key string) string {
	stem := strings.TrimSuffix(path, filepath.Ext(path))
	return fmt.Sprintf("%s-before-%s.db", stem, key)
}

// MigrationTiming is what an open that upgraded the registry spent on it.
type MigrationTiming struct {
	From, To string
	Backup   time.Duration
	Migrate  time.Duration
	Compress time.Duration
	// CompressErr leaves the uncompressed copy in place, which still restores.
	CompressErr error
}

// Every other open waits on the caller's write lock, so this copies pages rather than rebuilding with VACUUM.
func (r *Registry) backUpBeforeMigration(found int, key string) error {
	final := migrationBackupPath(r.diskPath(), key)
	temporary := final + ".tmp"
	removeCopy(temporary)
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("back up the registry before migrating it: %w", err)
	}
	file.Close()
	defer removeCopy(temporary)
	source, err := sql.Open("sqlite", r.path+r.querySeparator()+"_pragma=busy_timeout(15000)")
	if err != nil {
		return fmt.Errorf("back up the registry before migrating it: %w", err)
	}
	defer source.Close()
	if err := copyPages(source, temporary); err != nil {
		return fmt.Errorf("back up the registry to %s before migrating it: %w", final, err)
	}
	if err := leaveWAL(temporary); err != nil {
		return fmt.Errorf("back up the registry to %s before migrating it: %w", final, err)
	}
	if copied := schemaOf(temporary); copied != found {
		return fmt.Errorf("back up the registry before migrating it: the copy is schema %d, want %d", copied, found)
	}
	// A restore leaves its copy in place, and this newer copy must replace it.
	if err := os.Remove(final + compressedBackupSuffix); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("back up the registry before migrating it: %w", err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return fmt.Errorf("back up the registry before migrating it: %w", err)
	}
	pruneMigrationBackups(r.diskPath())
	return nil
}

func removeCopy(path string) {
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
}

func copyPages(source *sql.DB, destination string) error {
	conn, err := source.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(driver any) error {
		backup, err := driver.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		}).NewBackup(destination)
		if err != nil {
			return err
		}
		for {
			more, err := backup.Step(-1)
			if err != nil {
				return errors.Join(err, backup.Finish())
			}
			if !more {
				return backup.Finish()
			}
		}
	})
}

// A page copy keeps the WAL header; the restore command expects one self-contained file.
func leaveWAL(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil {
		return err
	}
	if mode != "delete" {
		return fmt.Errorf("the copy stayed in journal mode %s", mode)
	}
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
	for _, key := range backups[min(len(backups), keptMigrationBackups):] {
		plain := migrationBackupPath(path, key)
		for _, file := range []string{plain, plain + compressedBackupSuffix, plain + compressedBackupSuffix + ".tmp"} {
			_ = os.Remove(file)
		}
	}
}

// migrationBackups lists the keys of the pre-migration copies beside path, newest first.
func migrationBackups(path string) []string {
	prefix := filepath.Base(strings.TrimSuffix(path, filepath.Ext(path))) + "-before-"
	entries, _ := os.ReadDir(filepath.Dir(path))
	var keys []string
	for _, entry := range entries {
		key, found := strings.CutPrefix(entry.Name(), prefix)
		if !found {
			continue
		}
		key, plain := strings.CutSuffix(key, ".db")
		if !plain {
			key, found = strings.CutSuffix(key, ".db"+compressedBackupSuffix)
		}
		if (plain || found) && ValidBackupKey(key) {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b string) int {
		if a == b {
			return 0
		}
		if newerBackupKey(a, b) {
			return -1
		}
		return 1
	})
	return slices.Compact(keys)
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
