package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestMain(m *testing.M) {
	if os.Getenv("AGENTHAIL_TEST_RUN_MAIN") == "1" {
		os.Args = append([]string{"agenthail"}, os.Args[1:]...)
		os.Exit(testkit.RunAwayFromLiveDashboard(func() int {
			main()
			return 0
		}, daemon.StandardDashboardListen, &daemon.DefaultDashboardListen))
	}
	os.Exit(testkit.RunAwayFromLiveDashboard(m.Run, daemon.StandardDashboardListen, &daemon.DefaultDashboardListen))
}

// runAgenthail runs this binary's main with args under a scratch HOME.
func runAgenthail(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = append(os.Environ(),
		"AGENTHAIL_TEST_RUN_MAIN=1",
		"HOME="+home,
		"CFFIXED_USER_HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"AGENTHAIL_SIDECAR=",
		"AGENTHAIL_SESSION_ID=fixture-session",
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	return output.String(), err
}

func TestHelpVersionAndArgumentErrorsLeaveTheRegistryAlone(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--help"},
		{"help"},
		{"hosts", "--help"},
		{"version"},
		{"--version", "--json"},
		{"send"},
		{"list", "--no-such-flag"},
		{"no-such-command"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			output, _ := runAgenthail(t, home, args...)
			if _, err := os.Stat(filepath.Join(home, ".agenthail", "registry.db")); !os.IsNotExist(err) {
				t.Fatalf("agenthail %s created a registry (stat err %v); output:\n%s", strings.Join(args, " "), err, output)
			}
		})
	}
}

// newerRegistryWithCompressedCopy leaves what a downgrade finds: a registry this build is too old for, and the compressed copy the newer build kept.
func newerRegistryWithCompressedCopy(t *testing.T, home string) (path string, future int) {
	t.Helper()
	path = filepath.Join(home, ".agenthail", "registry.db")
	reg, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reg.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	future = registry.SchemaVersion() + 1
	plain := path + ".plain"
	if _, err := db.Exec(`VACUUM INTO ?`, plain); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = ` + strconv.Itoa(future)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	compressed := filepath.Join(home, ".agenthail", "registry-before-v"+strconv.Itoa(future)+".db.zst")
	if err := os.WriteFile(compressed, encoder.EncodeAll(data, nil), 0600); err != nil {
		t.Fatal(err)
	}
	return path, future
}

func TestTheBuildThatRefusesANewerRegistryCanRestoreItsCopy(t *testing.T) {
	home := t.TempDir()
	path, future := newerRegistryWithCompressedCopy(t, home)

	refusal, err := runAgenthail(t, home, "whoami")
	restore := "agenthail registry restore " + strconv.Itoa(future)
	if err == nil || !strings.Contains(refusal, restore) {
		t.Fatalf("err=%v output=%q, want the refusal to offer %q", err, refusal, restore)
	}
	listing, err := runAgenthail(t, home, "registry", "restore")
	if err != nil || !strings.Contains(listing, "schema "+strconv.Itoa(registry.SchemaVersion())) {
		t.Fatalf("listing against a newer registry: err=%v output=%q", err, listing)
	}

	args := strings.Fields(strings.TrimPrefix(restore, "agenthail "))
	output, err := runAgenthail(t, home, args...)
	if err != nil {
		t.Fatalf("%s: %v\n%s", restore, err, output)
	}
	aside := filepath.Join(home, ".agenthail", "registry-v"+strconv.Itoa(future)+".db")
	if !strings.Contains(output, aside) {
		t.Fatalf("the output does not name %s:\n%s", aside, output)
	}
	if _, err := os.Stat(aside); err != nil {
		t.Fatal(err)
	}
	if after, err := runAgenthail(t, home, "whoami"); strings.Contains(after, "registry is schema") {
		t.Fatalf("still refused after the restore (err %v):\n%s", err, after)
	}
	reg, err := registry.OpenCurrent(path)
	if err != nil {
		t.Fatal(err)
	}
	reg.Close()
}

func TestARestoreRefusesARunningDaemon(t *testing.T) {
	home := t.TempDir()
	path, future := newerRegistryWithCompressedCopy(t, home)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	daemonLike := exec.Command("/bin/sh", "-c", "sleep 60; :", "agenthail", "daemon-run")
	if err := daemonLike.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		daemonLike.Process.Kill()
		daemonLike.Wait()
	})
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "daemon.pid"), []byte(strconv.Itoa(daemonLike.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := runAgenthail(t, home, "registry", "restore", strconv.Itoa(future))
	if err == nil || !strings.Contains(output, "agenthail daemon stop") {
		t.Fatalf("err=%v output=%q", err, output)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the registry changed: %v", err)
	}
}

func TestACommandRefusesARegistryANewerAgenthailMigrated(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".agenthail", "registry.db")
	reg, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reg.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	future := registry.SchemaVersion() + 1
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = " + strconv.Itoa(future)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	want := "registry is schema " + strconv.Itoa(future) + "; this agenthail supports " + strconv.Itoa(registry.SchemaVersion()) + "; install a newer agenthail"
	for _, args := range [][]string{{"whoami"}, {"daemon-run"}} {
		output, err := runAgenthail(t, home, args...)
		if err == nil || !strings.Contains(output, want) {
			t.Fatalf("agenthail %s against a newer registry: err %v, output %q, want it to fail with %q", args[0], err, output, want)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("refusing the registry wrote to it")
	}
}
