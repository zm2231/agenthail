package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// MigrationBackup is a pre-migration copy kept beside the registry.
type MigrationBackup struct {
	Target int
	Schema int
	Path   string
	Size   int64
}

// RestoredRegistry says what a restore put in place and where the previous registry went.
type RestoredRegistry struct {
	Schema int
	From   string
	Aside  string
}

func ListMigrationBackups() []MigrationBackup {
	return listMigrationBackups(defaultPath())
}

func listMigrationBackups(path string) []MigrationBackup {
	var backups []MigrationBackup
	for _, target := range migrationBackups(path) {
		file := existingMigrationBackup(path, target)
		if file == "" {
			continue
		}
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		backups = append(backups, MigrationBackup{Target: target, Schema: backupSchema(file), Path: file, Size: info.Size()})
	}
	return backups
}

// RestoreMigrationBackup never opens the current registry through Open, because it must work on a registry newer than this build.
func RestoreMigrationBackup(target int) (RestoredRegistry, error) {
	return restoreMigrationBackup(defaultPath(), target)
}

func restoreMigrationBackup(path string, target int) (RestoredRegistry, error) {
	if err := refuseSudoHomeFor(path); err != nil {
		return RestoredRegistry{}, err
	}
	file := existingMigrationBackup(path, target)
	if file == "" {
		return RestoredRegistry{}, fmt.Errorf("no copy was kept from before schema %d", target)
	}
	unlock, err := holdRestoreLock(path)
	if err != nil {
		return RestoredRegistry{}, err
	}
	defer unlock()
	return swapInMigrationBackup(path, file)
}

var restoreStep = func(string) {}

func swapInMigrationBackup(path, file string) (RestoredRegistry, error) {
	schema := backupSchema(file)
	if schema < 1 {
		return RestoredRegistry{}, fmt.Errorf("%s cannot be read", file)
	}
	if schema > schemaVersion {
		return RestoredRegistry{}, fmt.Errorf("%s is schema %d; this agenthail supports %d; install a newer agenthail", file, schema, schemaVersion)
	}
	temporary := path + ".restore.tmp"
	removeCopy(temporary)
	defer removeCopy(temporary)
	if err := writeRestoredCopy(file, temporary); err != nil {
		return RestoredRegistry{}, fmt.Errorf("restore %s: %w", file, err)
	}
	if err := verifyRestoredCopy(temporary, schema); err != nil {
		return RestoredRegistry{}, fmt.Errorf("restore %s: %w", file, err)
	}
	restoreStep("verified")
	checkpointBeforeRestore(path)
	aside, sidecars, err := linkRegistryAside(path)
	if err != nil {
		return RestoredRegistry{}, fmt.Errorf("set the current registry aside: %w", err)
	}
	restoreStep("linked")
	if err := detachSidecars(path, sidecars); err != nil {
		return RestoredRegistry{}, errors.Join(fmt.Errorf("restore %s: %w", file, err), reattachSidecars(path, aside, sidecars))
	}
	if err := os.Rename(temporary, path); err != nil {
		return RestoredRegistry{}, errors.Join(fmt.Errorf("restore %s: %w", file, err), reattachSidecars(path, aside, sidecars))
	}
	restoreStep("swapped")
	restored := RestoredRegistry{Schema: schema, From: file, Aside: aside}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return restored, fmt.Errorf("the registry was restored from %s, but it may not survive a power loss until the disk recovers: %w", file, err)
	}
	return restored, nil
}

func writeRestoredCopy(file, to string) error {
	source, err := os.Open(file)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer destination.Close()
	var reader io.Reader = source
	if strings.HasSuffix(file, compressedBackupSuffix) {
		decoder, err := zstd.NewReader(source, zstd.WithDecoderConcurrency(1))
		if err != nil {
			return err
		}
		defer decoder.Close()
		reader = decoder
	}
	if _, err := io.Copy(destination, reader); err != nil {
		return err
	}
	if err := destination.Sync(); err != nil {
		return err
	}
	return destination.Close()
}

func verifyRestoredCopy(path string, schema int) error {
	db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return err
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version != schema {
		return fmt.Errorf("the copy is schema %d, want %d", version, schema)
	}
	var check string
	if err := db.QueryRow(`PRAGMA quick_check(1)`).Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("the copy is damaged: %s", check)
	}
	return nil
}

// A WAL with nothing in it leaves nothing to lose if a kill lands between unlinking it and the swap. Failing here changes nothing: the WAL is then linked aside with the rest.
func checkpointBeforeRestore(path string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(1000)")
	if err != nil {
		return
	}
	defer db.Close()
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
}

// Links keep every original reachable and the registry path present, because an opener would create an empty registry at a missing path.
func linkRegistryAside(path string) (string, []string, error) {
	current, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	schema := schemaInHeader(current)
	current.Close()
	aside := registryAsidePath(path, schema, time.Now())
	if err := os.Link(path, aside); err != nil {
		return "", nil, err
	}
	var sidecars []string
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		err := os.Link(path+suffix, aside+suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", nil, errors.Join(err, removeAsideLinks(aside, sidecars))
		}
		sidecars = append(sidecars, suffix)
	}
	return aside, sidecars, nil
}

// SQLite applies an old WAL to whatever file it finds and reports the result as malformed, so these go before the restored file arrives.
func detachSidecars(path string, sidecars []string) error {
	for _, suffix := range sidecars {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
		restoreStep("detached " + suffix)
	}
	return nil
}

func reattachSidecars(path, aside string, sidecars []string) error {
	if aside == "" {
		return nil
	}
	for _, suffix := range sidecars {
		if err := os.Link(aside+suffix, path+suffix); err != nil {
			return fmt.Errorf("the registry's previous files are still at %s: %w", aside, err)
		}
	}
	return removeAsideLinks(aside, sidecars)
}

func removeAsideLinks(aside string, sidecars []string) error {
	failures := []error{os.Remove(aside)}
	for _, suffix := range sidecars {
		failures = append(failures, os.Remove(aside+suffix))
	}
	return errors.Join(failures...)
}

func registryAsidePath(path string, schema int, now time.Time) string {
	stem := strings.TrimSuffix(path, filepath.Ext(path))
	name := fmt.Sprintf("%s-v%d.db", stem, schema)
	if schema < 1 {
		name = stem + "-unreadable.db"
	}
	if _, err := os.Stat(name); os.IsNotExist(err) {
		return name
	}
	return fmt.Sprintf("%s-%d.db", strings.TrimSuffix(name, ".db"), now.UnixMilli())
}
