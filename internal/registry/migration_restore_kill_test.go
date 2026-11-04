package registry

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	killStepEnv   = "AGENTHAIL_TEST_RESTORE_KILL_STEP"
	killPathEnv   = "AGENTHAIL_TEST_RESTORE_KILL_PATH"
	killTargetEnv = "AGENTHAIL_TEST_RESTORE_KILL_TARGET"
)

func TestRestoreProcessThatIsKilled(t *testing.T) {
	step := os.Getenv(killStepEnv)
	if step == "" {
		t.Skip("run by TestKillingARestoreAtAnyStepLeavesAnOpenableRegistry")
	}
	restoreStep = func(reached string) {
		if reached == step {
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
			time.Sleep(time.Minute)
		}
	}
	target, err := strconv.Atoi(os.Getenv(killTargetEnv))
	if err != nil {
		t.Fatal(err)
	}
	_, err = restoreMigrationBackup(os.Getenv(killPathEnv), target)
	t.Fatalf("the restore finished without reaching %q: %v", step, err)
}

func TestKillingARestoreAtAnyStepLeavesAnOpenableRegistry(t *testing.T) {
	for _, test := range []struct {
		step string
		// where the rows only the WAL holds can be read after the kill
		walRowsInRegistry, walRowsAside bool
	}{
		{step: "verified", walRowsInRegistry: true},
		{step: "linked", walRowsInRegistry: true, walRowsAside: true},
		{step: "detached -wal", walRowsAside: true},
		{step: "swapped", walRowsAside: true},
	} {
		t.Run(test.step, func(t *testing.T) {
			live := filepath.Join(t.TempDir(), "registry.db")
			registryAtSchema(t, live, schemaVersion)
			holder, err := connect(live)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Close()
			if _, err := holder.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				t.Fatal(err)
			}
			if _, err := holder.db.Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
				t.Fatal(err)
			}
			if _, err := holder.db.Exec(`CREATE TABLE only_in_wal(id INTEGER)`); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "registry.db")
			for _, suffix := range []string{"", "-wal"} {
				data, err := os.ReadFile(live + suffix)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path+suffix, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			target := schemaVersion + 1
			registryAtSchemaCopy(t, live, migrationBackupPath(path, target), schemaVersion)
			backup, err := connect(migrationBackupPath(path, target))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := backup.db.Exec(`DROP TABLE only_in_wal`); err != nil {
				t.Fatal(err)
			}
			backup.Close()
			if err := compressMigrationBackups(path); err != nil {
				t.Fatal(err)
			}
			aside := filepath.Join(filepath.Dir(path), "registry-v"+strconv.Itoa(schemaVersion)+".db")

			helper := exec.Command(os.Args[0], "-test.run=^TestRestoreProcessThatIsKilled$")
			helper.Env = append(os.Environ(), killStepEnv+"="+test.step, killPathEnv+"="+path, killTargetEnv+"="+strconv.Itoa(target))
			output, err := helper.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(exit.String(), "killed") {
				t.Fatalf("the restore was not killed: %v\n%s", err, output)
			}

			began := time.Now()
			r, err := Open(path)
			if err != nil {
				t.Fatalf("open after the kill: %v", err)
			}
			defer r.Close()
			if took := time.Since(began); took > 5*time.Second {
				t.Fatalf("the open took %s", took)
			}
			var check string
			if err := r.read.QueryRow(`PRAGMA quick_check(1)`).Scan(&check); err != nil || check != "ok" {
				t.Fatalf("quick_check=%q err=%v", check, err)
			}
			if _, err := r.Session("kept"); err != nil {
				t.Fatalf("the registry lost its data: %v", err)
			}
			if got := hasWALRows(t, r); got != test.walRowsInRegistry {
				t.Fatalf("the registry has the WAL's rows: %v, want %v", got, test.walRowsInRegistry)
			}
			r.Close()
			if test.walRowsAside {
				kept, err := connect(aside)
				if err != nil {
					t.Fatal(err)
				}
				defer kept.Close()
				if !hasWALRows(t, kept) {
					t.Fatal("the set-aside registry lost what was in the WAL")
				}
				kept.Close()
			}

			retried, err := restoreMigrationBackup(path, target)
			if err != nil {
				t.Fatalf("the restore could not be run again: %v", err)
			}
			if retried.Schema != schemaVersion {
				t.Fatalf("retried=%+v", retried)
			}
		})
	}
}

func hasWALRows(t *testing.T, r *Registry) bool {
	t.Helper()
	var tables int
	if err := r.read.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='only_in_wal'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	return tables == 1
}

func TestAnOpenGivesUpWhenARestoreNeverFinishes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	unlock, err := holdRestoreLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	previous := restoreWait
	restoreWait = 100 * time.Millisecond
	defer func() { restoreWait = previous }()
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "still being restored") {
		t.Fatalf("err=%v", err)
	}
}

func TestAFailedUndoKeepsEveryFileAndSaysWhere(t *testing.T) {
	dir := t.TempDir()
	aside := filepath.Join(dir, "registry-v9.db")
	for _, file := range []string{aside, aside + "-wal"} {
		if err := os.WriteFile(file, []byte("rows"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	err := reattachSidecars(filepath.Join(dir, "gone", "registry.db"), aside, []string{"-wal"})
	if err == nil || !strings.Contains(err.Error(), aside) {
		t.Fatalf("err=%v, want it to name %s", err, aside)
	}
	for _, file := range []string{aside, aside + "-wal"} {
		if data, _ := os.ReadFile(file); string(data) != "rows" {
			t.Fatalf("%s was lost", file)
		}
	}
}
