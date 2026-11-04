package daemon

import (
	"bytes"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	_ "modernc.org/sqlite"
)

func TestTheDaemonLogsWhatAnUpgradeSpent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	store, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var schema int
	if err := store.ReadDB().QueryRow(`PRAGMA user_version`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	store.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, schema-1)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err = registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var logged bytes.Buffer
	d := New(store, nil)
	d.log = log.New(&logged, "", 0)
	d.SetStartup(time.Now(), 12*time.Millisecond)
	d.logRegistryStartup()
	d.logStarted()
	output := logged.String()
	for _, want := range []string{
		"registry open took 12ms",
		fmt.Sprintf("registry migrated from schema %d to %d: backup ", schema-1, schema),
		", migration ",
		", compress ",
		"after launch",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("log lacks %q:\n%s", want, output)
		}
	}
}

func TestADaemonOnACurrentRegistryLogsNoMigration(t *testing.T) {
	store, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var logged bytes.Buffer
	d := New(store, nil)
	d.log = log.New(&logged, "", 0)
	d.SetStartup(time.Now(), time.Millisecond)
	d.logRegistryStartup()
	if output := logged.String(); !strings.Contains(output, "registry open took") || strings.Contains(output, "migrated") {
		t.Fatalf("log = %q", output)
	}
}
