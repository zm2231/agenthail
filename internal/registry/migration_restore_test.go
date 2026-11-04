package registry

import (
	"bufio"
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func plainCopyOfBackup(t *testing.T, path string, target int) string {
	t.Helper()
	file := existingMigrationBackup(path, target)
	if file == "" {
		t.Fatalf("no backup for schema %d", target)
	}
	plain := filepath.Join(t.TempDir(), "backup.db")
	if err := writeRestoredCopy(file, plain); err != nil {
		t.Fatal(err)
	}
	return plain
}

func stampSchema(t *testing.T, path string, schema int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA user_version=` + strconv.Itoa(schema)); err != nil {
		t.Fatal(err)
	}
}

func registryNewerThanBuildWithBackup(t *testing.T, compress bool) (path string, newer int) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "registry.db")
	registryAtSchema(t, path, schemaVersion)
	newer = schemaVersion + 1
	plain, err := connect(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.db.Exec(`VACUUM INTO ?`, migrationBackupPath(path, newer)); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.db.Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.db.Exec(`PRAGMA user_version=` + strconv.Itoa(newer)); err != nil {
		t.Fatal(err)
	}
	plain.Close()
	if compress {
		if err := compressMigrationBackups(path); err != nil {
			t.Fatal(err)
		}
	}
	return path, newer
}

func TestAMigrationCompressesItsCopyAndAnyOlderOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	registryAtSchema(t, path, schemaVersion-1)
	older := schemaVersion - 1
	registryAtSchemaCopy(t, path, migrationBackupPath(path, older), schemaVersion-2)
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	timing, _ := r.Migration()
	r.Close()
	if timing.CompressErr != nil || timing.Compress <= 0 {
		t.Fatalf("timing=%+v", timing)
	}
	for _, target := range []int{schemaVersion, older} {
		if _, err := os.Stat(migrationBackupPath(path, target)); !os.IsNotExist(err) {
			t.Fatalf("schema %d kept its plain copy: %v", target, err)
		}
		info, err := os.Stat(compressedBackupPath(path, target))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("schema %d compressed copy: %v %v", target, err, info)
		}
	}
	if got := backupSchema(compressedBackupPath(path, schemaVersion)); got != schemaVersion-1 {
		t.Fatalf("schema read from the compressed header = %d", got)
	}
	if got := backupSchema(compressedBackupPath(path, older)); got != schemaVersion-2 {
		t.Fatalf("schema read from the older compressed header = %d", got)
	}
}

func TestACopyThatCannotBeCompressedStaysAndTheMigrationStands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	registryAtSchema(t, path, schemaVersion-1)
	blocked := compressedBackupPath(path, schemaVersion) + ".tmp"
	if err := os.MkdirAll(filepath.Join(blocked, "occupied"), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	timing, _ := r.Migration()
	r.Close()
	if timing.CompressErr == nil {
		t.Fatal("the failure was not reported")
	}
	if got := schemaOf(path); got != schemaVersion {
		t.Fatalf("schema=%d", got)
	}
	if got := schemaOf(migrationBackupPath(path, schemaVersion)); got != schemaVersion-1 {
		t.Fatalf("the plain copy was lost: schema %d", got)
	}
}

func TestACrashAtAnyStepOfCompressionLeavesAUsableCopy(t *testing.T) {
	prepare := func(t *testing.T) (string, []byte) {
		path := filepath.Join(t.TempDir(), "registry.db")
		registryAtSchema(t, path, schemaVersion)
		registryAtSchemaCopy(t, path, migrationBackupPath(path, 7), schemaVersion)
		data, err := os.ReadFile(migrationBackupPath(path, 7))
		if err != nil {
			t.Fatal(err)
		}
		return path, data
	}
	assertRecovers := func(t *testing.T, path string, want []byte) {
		t.Helper()
		if got := backupSchema(existingMigrationBackup(path, 7)); got != schemaVersion {
			t.Fatalf("before recovery the copy reads as schema %d", got)
		}
		if err := compressMigrationBackups(path); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(migrationBackupPath(path, 7)); !os.IsNotExist(err) {
			t.Fatalf("plain copy stayed: %v", err)
		}
		if _, err := os.Stat(compressedBackupPath(path, 7) + ".tmp"); !os.IsNotExist(err) {
			t.Fatalf("temporary file stayed: %v", err)
		}
		got, err := os.ReadFile(plainCopyOfBackup(t, path, 7))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("copy changed: err=%v len=%d want %d", err, len(got), len(want))
		}
	}
	t.Run("during the write", func(t *testing.T) {
		path, want := prepare(t)
		if err := os.WriteFile(compressedBackupPath(path, 7)+".tmp", []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
		assertRecovers(t, path, want)
	})
	t.Run("after the rename", func(t *testing.T) {
		path, want := prepare(t)
		if err := writeCompressedCopy(migrationBackupPath(path, 7), compressedBackupPath(path, 7)); err != nil {
			t.Fatal(err)
		}
		assertRecovers(t, path, want)
	})
}

func TestASecondUpgradeToTheSameSchemaReplacesTheCompressedCopy(t *testing.T) {
	path, newer := registryNewerThanBuildWithBackup(t, true)
	stale, err := os.ReadFile(compressedBackupPath(path, newer))
	if err != nil {
		t.Fatal(err)
	}
	stampSchema(t, path, schemaVersion)
	target := compressedBackupPath(path, schemaVersion)
	if err := os.Rename(compressedBackupPath(path, newer), target); err != nil {
		t.Fatal(err)
	}
	stampSchema(t, path, schemaVersion-1)
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	fresh, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(stale, fresh) {
		t.Fatal("the older copy survived the new upgrade")
	}
	if got := backupSchema(target); got != schemaVersion-1 {
		t.Fatalf("copy is schema %d, want the one this upgrade started from", got)
	}
}

func TestOnlyTheTwoNewestCopiesAreKeptAcrossBothForms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	for _, file := range []string{migrationBackupPath(path, 5), compressedBackupPath(path, 5), compressedBackupPath(path, 6), migrationBackupPath(path, 7)} {
		if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pruneMigrationBackups(path)
	if got := migrationBackups(path); len(got) != 2 || got[0] != 7 || got[1] != 6 {
		t.Fatalf("kept %v", got)
	}
	for _, file := range []string{migrationBackupPath(path, 5), compressedBackupPath(path, 5)} {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("%s stayed: %v", file, err)
		}
	}
}

func TestARestoreWorksFromEitherFormOnARegistryNewerThanThisBuild(t *testing.T) {
	for _, compress := range []bool{true, false} {
		t.Run(strconv.FormatBool(compress), func(t *testing.T) {
			path, newer := registryNewerThanBuildWithBackup(t, compress)
			restored, err := restoreMigrationBackup(path, newer)
			if err != nil {
				t.Fatal(err)
			}
			if restored.Schema != schemaVersion || restored.Aside != filepath.Join(filepath.Dir(path), "registry-v"+strconv.Itoa(newer)+".db") {
				t.Fatalf("restored=%+v", restored)
			}
			if got := schemaOf(restored.Aside); got != newer {
				t.Fatalf("aside is schema %d", got)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("restored file: %v %v", err, info)
			}
			r, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if _, err := r.Session("kept"); err != nil {
				t.Fatalf("the restore lost the registry's data: %v", err)
			}
			if existingMigrationBackup(path, newer) == "" {
				t.Fatal("the restore consumed its copy")
			}
		})
	}
}

func TestRowsOnlyInTheWALSurviveInTheSetAsideRegistry(t *testing.T) {
	live := filepath.Join(t.TempDir(), "registry.db")
	registryAtSchema(t, live, schemaVersion)
	writer, err := connect(live)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.db.Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.db.Exec(`CREATE TABLE only_in_wal(id INTEGER)`); err != nil {
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
	registryAtSchemaCopy(t, live, migrationBackupPath(path, schemaVersion+1), schemaVersion)
	restored, err := restoreMigrationBackup(path, schemaVersion+1)
	if err != nil {
		t.Fatal(err)
	}
	aside, err := connect(restored.Aside)
	if err != nil {
		t.Fatal(err)
	}
	defer aside.Close()
	var count int
	if err := aside.read.QueryRow(`SELECT COUNT(*) FROM only_in_wal`).Scan(&count); err != nil {
		t.Fatalf("the set-aside registry lost what was in its WAL: %v", err)
	}
}

func TestARestoreNeverOverwritesAnEarlierSetAside(t *testing.T) {
	path, newer := registryNewerThanBuildWithBackup(t, true)
	taken := filepath.Join(filepath.Dir(path), "registry-v"+strconv.Itoa(newer)+".db")
	if err := os.WriteFile(taken, []byte("earlier"), 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := restoreMigrationBackup(path, newer)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Aside == taken {
		t.Fatal("reused the taken name")
	}
	if data, _ := os.ReadFile(taken); string(data) != "earlier" {
		t.Fatalf("overwrote the earlier file: %q", data)
	}
}

func TestARefusedRestoreLeavesTheRegistryAlone(t *testing.T) {
	cases := map[string]func(t *testing.T, path string, target int){
		"no copy": func(t *testing.T, path string, target int) {
			if err := os.Remove(compressedBackupPath(path, target)); err != nil {
				t.Fatal(err)
			}
		},
		"corrupt compressed copy": func(t *testing.T, path string, target int) {
			file := compressedBackupPath(path, target)
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, data[:len(data)/2], 0600); err != nil {
				t.Fatal(err)
			}
		},
		"damaged database inside": func(t *testing.T, path string, target int) {
			plain := plainCopyOfBackup(t, path, target)
			data, err := os.ReadFile(plain)
			if err != nil {
				t.Fatal(err)
			}
			for i := 4096; i < len(data); i++ {
				data[i] = 0xff
			}
			if err := os.WriteFile(migrationBackupPath(path, target), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(compressedBackupPath(path, target)); err != nil {
				t.Fatal(err)
			}
		},
		"copy newer than this build": func(t *testing.T, path string, target int) {
			plain := plainCopyOfBackup(t, path, target)
			stampSchema(t, plain, schemaVersion+1)
			if err := os.Remove(compressedBackupPath(path, target)); err != nil {
				t.Fatal(err)
			}
			if err := writeCompressedCopy(plain, compressedBackupPath(path, target)); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			path, newer := registryNewerThanBuildWithBackup(t, true)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			breakIt(t, path, newer)
			if _, err := restoreMigrationBackup(path, newer); err == nil {
				t.Fatal("the restore went ahead")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("the registry changed: %v", err)
			}
			names, _ := os.ReadDir(filepath.Dir(path))
			for _, entry := range names {
				if strings.Contains(entry.Name(), ".restore.tmp") || strings.HasPrefix(entry.Name(), "registry-v") {
					t.Fatalf("left %s behind", entry.Name())
				}
			}
		})
	}
}

func TestUndoingASetAsideLeavesTheRegistryAsItWas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	for _, file := range []string{path, path + "-wal"} {
		if err := os.WriteFile(file, []byte("current"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	aside, sidecars, err := linkRegistryAside(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := detachSidecars(path, sidecars); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + "-wal"); !os.IsNotExist(err) || aside == "" {
		t.Fatalf("not set aside: %v %q", err, aside)
	}
	if err := reattachSidecars(path, aside, sidecars); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{path, path + "-wal"} {
		if data, _ := os.ReadFile(file); string(data) != "current" {
			t.Fatalf("%s=%q", file, data)
		}
	}
	if _, err := os.Stat(aside); !os.IsNotExist(err) {
		t.Fatalf("the aside link stayed: %v", err)
	}
}

func TestTheRegistryPathNeverGoesMissingDuringARestore(t *testing.T) {
	path, newer := registryNewerThanBuildWithBackup(t, true)
	stop := make(chan struct{})
	missing := make(chan bool, 1)
	go func() {
		for {
			select {
			case <-stop:
				missing <- false
				return
			default:
				if _, err := os.Stat(path); os.IsNotExist(err) {
					missing <- true
					return
				}
			}
		}
	}()
	if _, err := restoreMigrationBackup(path, newer); err != nil {
		t.Fatal(err)
	}
	close(stop)
	if <-missing {
		t.Fatal("an opener in that moment would have created an empty registry")
	}
}

func TestAnOpenDuringARestoreWaitsForItAndGetsTheRestoredRegistry(t *testing.T) {
	shortRestoreHoldWait(t)
	path, newer := registryNewerThanBuildWithBackup(t, true)
	unlock, err := holdRestoreLock(path)
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan error, 1)
	go func() {
		r, err := Open(path)
		if err == nil {
			_, err = r.Session("kept")
			r.Close()
		}
		opened <- err
	}()
	select {
	case err := <-opened:
		t.Fatalf("the open went through during a restore: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := restoreMigrationBackup(path, newer); err == nil {
		t.Fatal("a second restore ran alongside the first")
	}
	if _, err := swapInMigrationBackup(path, compressedBackupPath(path, newer)); err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := <-opened; err != nil {
		t.Fatalf("the waiting open failed: %v", err)
	}
}

func shortRestoreHoldWait(t *testing.T) {
	t.Helper()
	previous := restoreHoldWait
	restoreHoldWait = 200 * time.Millisecond
	t.Cleanup(func() { restoreHoldWait = previous })
}

func registryAtSchemaWithBackup(t *testing.T) (path string, target int) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "registry.db")
	registryAtSchema(t, path, schemaVersion)
	target = schemaVersion + 1
	registryAtSchemaCopy(t, path, migrationBackupPath(path, target), schemaVersion)
	return path, target
}

func TestARestoreRefusesWhileAnotherProcessHasTheRegistryOpenAndSaysWhat(t *testing.T) {
	shortRestoreHoldWait(t)
	path, target := registryAtSchemaWithBackup(t)
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = restoreMigrationBackup(path, target)
	if err == nil {
		t.Fatal("the restore ran with the registry open")
	}
	for _, want := range []string{"agenthail command", "lsof", registryLockPath(path)} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err=%q, want it to mention %q", err, want)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the registry changed: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreMigrationBackup(path, target); err != nil {
		t.Fatalf("after the holder closed: %v", err)
	}
}

func TestAnOpenThatCannotTakeTheLockFails(t *testing.T) {
	path, _ := registryAtSchemaWithBackup(t)
	lock := registryLockPath(path)
	if err := os.Chmod(lock, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lock, 0o600) })
	if file, err := os.OpenFile(lock, os.O_RDWR, 0); err == nil {
		file.Close()
		t.Skip("this runner can open any file")
	}
	r, err := Open(path)
	if err == nil {
		r.Close()
		t.Fatal("the registry opened with nothing keeping a restore out")
	}
	if !strings.Contains(err.Error(), "lock the registry") || !strings.Contains(err.Error(), "sudo chown") || !strings.Contains(err.Error(), shellQuote(lock)) {
		t.Fatalf("the error does not say which file or how to fix it: %v", err)
	}
}

func TestALockPathThatIsASymlinkIsRefusedAndItsTargetLeftAlone(t *testing.T) {
	for name, hold := range map[string]func(string) (func(), error){
		"open": func(path string) (func(), error) {
			file, err := holdRegistryOpen(path)
			if err != nil {
				return nil, err
			}
			return func() { file.Close() }, nil
		},
		"restore": holdRestoreLock,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "registry.db")
			existing := filepath.Join(t.TempDir(), "protected")
			if err := os.WriteFile(existing, []byte("protected"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(existing, registryLockPath(path)); err != nil {
				t.Fatal(err)
			}
			if release, err := hold(path); err == nil {
				release()
				t.Fatal("took the lock through a symlink")
			} else if !strings.Contains(err.Error(), shellQuote(registryLockPath(path))) {
				t.Fatalf("the refusal does not name the file: %v", err)
			}
			if content, err := os.ReadFile(existing); err != nil || string(content) != "protected" {
				t.Fatalf("target=%q err=%v", content, err)
			}

			dangling := filepath.Join(t.TempDir(), "registry.db")
			missing := filepath.Join(t.TempDir(), "made-through-the-link")
			if err := os.Symlink(missing, registryLockPath(dangling)); err != nil {
				t.Fatal(err)
			}
			if release, err := hold(dangling); err == nil {
				release()
				t.Fatal("took the lock through a dangling symlink")
			}
			if _, err := os.Lstat(missing); !os.IsNotExist(err) {
				t.Fatalf("the symlink's target was created: %v", err)
			}
		})
	}
}

func TestAWriteFromAnOpenBeforeARestoreCannotLandInTheSetAsideRegistry(t *testing.T) {
	shortRestoreHoldWait(t)
	path, target := registryAtSchemaWithBackup(t)
	opener, err := holdRegistryOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restoreMigrationBackup(path, target); err == nil {
		t.Fatal("a restore started between an opener's lock and its first SQLite call")
	}
	closeOpenLock(opener)
	if _, err := restoreMigrationBackup(path, target); err != nil {
		t.Fatal(err)
	}
}

func TestAChildOfAnOpenRegistryDoesNotHoldItsLock(t *testing.T) {
	shortRestoreHoldWait(t)
	path, target := registryAtSchemaWithBackup(t)
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command("/bin/sh", "-c", "sleep 60; :")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		child.Process.Kill()
		child.Wait()
	})
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreMigrationBackup(path, target); err != nil {
		t.Fatalf("a spawned child kept the lock: %v", err)
	}
}

func TestAKilledHolderReleasesTheLock(t *testing.T) {
	shortRestoreHoldWait(t)
	path, target := registryAtSchemaWithBackup(t)
	holder := exec.Command(os.Args[0], "-test.run=^TestHolderProcessThatIsKilled$")
	holder.Env = append(os.Environ(), holderPathEnv+"="+path)
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer holder.Wait()
	defer holder.Process.Kill()
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("the holder never opened the registry: %v", err)
		}
		if strings.Contains(line, "holding") {
			break
		}
	}
	if _, err := restoreMigrationBackup(path, target); err == nil {
		t.Fatal("the restore ran while another process held the registry")
	}
	if err := holder.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	holder.Wait()
	if _, err := restoreMigrationBackup(path, target); err != nil {
		t.Fatalf("after the holder was killed: %v", err)
	}
}

const holderPathEnv = "AGENTHAIL_TEST_REGISTRY_HOLDER_PATH"

func TestHolderProcessThatIsKilled(t *testing.T) {
	path := os.Getenv(holderPathEnv)
	if path == "" {
		t.Skip("run by TestAKilledHolderReleasesTheLock")
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fmt.Println("holding")
	time.Sleep(time.Minute)
}

func TestListingNamesEachCopyOnce(t *testing.T) {
	path, newer := registryNewerThanBuildWithBackup(t, true)
	if err := os.WriteFile(migrationBackupPath(path, newer), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	got := listMigrationBackups(path)
	if len(got) != 1 || got[0].Target != newer || got[0].Schema != schemaVersion || got[0].Size <= 0 {
		t.Fatalf("listed %+v", got)
	}
}
