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
	aside, moved, err := setRegistryAside(path)
	if err != nil {
		return RestoredRegistry{}, fmt.Errorf("set the current registry aside: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return RestoredRegistry{}, errors.Join(fmt.Errorf("restore %s: %w", file, err), undoSetAside(aside, moved))
	}
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

type movedFile struct{ from, to string }

// The main file is linked rather than moved so the registry path never goes missing, since an opener would create an empty registry there.
// The sidecars move first because an old WAL must never meet the restored file. Opening the registry to learn its schema could remove its WAL.
func setRegistryAside(path string) (string, []movedFile, error) {
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
	var moved []movedFile
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		err := os.Rename(path+suffix, aside+suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", nil, errors.Join(err, undoSetAside(aside, moved))
		}
		moved = append(moved, movedFile{path + suffix, aside + suffix})
	}
	return aside, moved, nil
}

func undoSetAside(aside string, moved []movedFile) error {
	failures := []error{moveBack(moved)}
	if aside != "" {
		failures = append(failures, os.Remove(aside))
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

func moveBack(moved []movedFile) error {
	var failures []error
	for _, file := range moved {
		if err := os.Rename(file.to, file.from); err != nil {
			failures = append(failures, fmt.Errorf("move %s back: %w", file.to, err))
		}
	}
	return errors.Join(failures...)
}
