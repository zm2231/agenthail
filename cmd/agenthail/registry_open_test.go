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

	_ "modernc.org/sqlite"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestMain(m *testing.M) {
	if os.Getenv("AGENTHAIL_TEST_RUN_MAIN") == "1" {
		os.Args = append([]string{"agenthail"}, os.Args[1:]...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
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
