package registry

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func registryAtSchema(t *testing.T, path string, schema int) {
	t.Helper()
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.RegisterSession(surface.Session{ID: "kept", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Name: "kept"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`PRAGMA user_version=` + strconv.Itoa(schema)); err != nil {
		t.Fatal(err)
	}
}

func TestAMigrationKeepsACopyOfTheRegistryFromBeforeIt(t *testing.T) {
	for _, mode := range []string{"wal", "delete"} {
		t.Run(mode, func(t *testing.T) { migrationKeepsACopy(t, mode) })
	}
}

func migrationKeepsACopy(t *testing.T, journal string) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.db")
	registryAtSchema(t, path, schemaVersion-1)
	plain, err := connect(path)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := plain.db.QueryRow(`PRAGMA journal_mode=` + journal).Scan(&mode); err != nil || mode != journal {
		t.Fatalf("journal_mode=%q err=%v", mode, err)
	}
	plain.Close()
	for _, old := range []int{schemaVersion - 3, schemaVersion - 2} {
		if err := os.WriteFile(migrationBackupPath(path, old), []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()

	backup := migrationBackupPath(path, schemaVersion)
	info, err := os.Stat(backup)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("backup %s: err=%v mode=%v", backup, err, info.Mode().Perm())
	}
	if got := schemaOf(backup); got != schemaVersion-1 {
		t.Fatalf("backup schema=%d, want %d", got, schemaVersion-1)
	}
	copied, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := copied.Session("kept"); err != nil {
		t.Fatalf("the backup lost the registry's data: %v", err)
	}
	copied.Close()
	if got := migrationBackups(path); len(got) != 2 || got[0] != schemaVersion || got[1] != schemaVersion-2 {
		t.Fatalf("kept backups for schemas %v, want the newest two", got)
	}
	if _, err := os.Stat(backup + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("the temporary copy was left behind: %v", err)
	}
}

func TestARegistryThatCannotBeBackedUpIsNotMigrated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.db")
	registryAtSchema(t, path, schemaVersion-1)
	blocked := migrationBackupPath(path, schemaVersion)
	if err := os.MkdirAll(filepath.Join(blocked, "occupied"), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err == nil {
		r.Close()
		t.Fatal("a registry whose backup failed was migrated")
	}
	if !strings.Contains(err.Error(), "back up the registry") {
		t.Fatalf("err=%v, want it to name the backup", err)
	}
	if got := schemaOf(path); got != schemaVersion-1 {
		t.Fatalf("schema=%d after a failed backup, want %d", got, schemaVersion-1)
	}
}

func TestANewerSchemaRefusalNamesTheCopyFromBeforeItAndHowToRestoreIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), `home odd" it's $HOME back\slash -n`)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "registry.db")
	registryAtSchema(t, path, schemaVersion)
	newer := schemaVersion + 1
	plain, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.db.Exec(`VACUUM INTO ?`, migrationBackupPath(path, newer)); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.db.Exec(`PRAGMA user_version=` + strconv.Itoa(newer)); err != nil {
		t.Fatal(err)
	}
	plain.Close()

	_, err = Open(path)
	var refused *NewerSchemaError
	if !errors.As(err, &refused) || refused.Backup != migrationBackupPath(path, newer) || refused.BackupSchema != schemaVersion {
		t.Fatalf("err=%v, want a refusal naming the backup", err)
	}
	steps := err.Error()[strings.Index(err.Error(), "agenthail daemon stop; ")+len("agenthail daemon stop; "):]
	steps = strings.TrimSuffix(steps, "; agenthail daemon start")
	if output, err := exec.Command("/bin/sh", "-c", steps).CombinedOutput(); err != nil {
		t.Fatalf("restore steps %q: %v %s", steps, err, output)
	}
	restored, err := Open(path)
	if err != nil {
		t.Fatalf("after the restore steps: %v", err)
	}
	defer restored.Close()
	if _, err := restored.Session("kept"); err != nil {
		t.Fatalf("the restored registry lost its data: %v", err)
	}
	if got := schemaOf(filepath.Join(dir, "registry-v"+strconv.Itoa(newer)+".db")); got != newer {
		t.Fatalf("the newer registry was not set aside: schema %d", got)
	}
}

func TestANewerSchemaRefusalNamesNoCopyThisBuildCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	registryAtSchema(t, path, schemaVersion+2)
	registryAtSchemaCopy(t, path, migrationBackupPath(path, schemaVersion+2), schemaVersion+1)
	_, err := Open(path)
	var refused *NewerSchemaError
	if !errors.As(err, &refused) || refused.Backup != "" || strings.Contains(err.Error(), "daemon stop") {
		t.Fatalf("err=%v, want the plain refusal", err)
	}
}

func registryAtSchemaCopy(t *testing.T, from, to string, schema int) {
	t.Helper()
	db, err := connect(from)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.db.Exec(`VACUUM INTO ?`, to); err != nil {
		t.Fatal(err)
	}
	copied, err := connect(to)
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	if _, err := copied.db.Exec(`PRAGMA user_version=` + strconv.Itoa(schema)); err != nil {
		t.Fatal(err)
	}
}

func TestADevelopmentBuildCreatesButDoesNotUpgradeTheDefaultRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	r, err := OpenDefault(false)
	if err != nil {
		t.Fatalf("a development build could not create a registry in a scratch HOME: %v", err)
	}
	r.Close()
	path := filepath.Join(home, ".agenthail", "registry.db")
	registryAtSchemaCopy(t, path, path+".rewound", schemaVersion-1)
	if err := os.Rename(path+".rewound", path); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + "-wal")
	os.Remove(path + "-shm")

	_, err = OpenDefault(false)
	var refused *DevUpgradeError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "scratch HOME") || !strings.Contains(err.Error(), "AGENTHAIL_ALLOW_DEV_MIGRATION=1") {
		t.Fatalf("err=%v, want the development build refusal", err)
	}
	if got := schemaOf(path); got != schemaVersion-1 {
		t.Fatalf("schema=%d after the refusal, want %d", got, schemaVersion-1)
	}
	if backups := migrationBackups(path); len(backups) != 0 {
		t.Fatalf("a refused upgrade left backups %v", backups)
	}
	r, err = OpenDefault(true)
	if err != nil {
		t.Fatalf("an allowed upgrade: %v", err)
	}
	r.Close()
	if got := schemaOf(path); got != schemaVersion {
		t.Fatalf("schema=%d after an allowed upgrade", got)
	}
}
