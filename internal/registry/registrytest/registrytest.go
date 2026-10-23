// Package registrytest gives tests a new registry without migrating one from
// an empty file each time.
package registrytest

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	_ "modernc.org/sqlite"
)

var template struct {
	once sync.Once
	data []byte
	err  error
}

// Path writes a new registry at this build's schema into the test's temporary
// directory and returns its path. The schema is migrated once per test
// process; each copy is opened with registry.Open as usual, which gives it its
// own host id and host epoch.
func Path(t testing.TB) string {
	t.Helper()
	template.once.Do(func() { template.data, template.err = build() })
	if template.err != nil {
		t.Fatal(template.err)
	}
	path := filepath.Join(t.TempDir(), "registry.db")
	if err := os.WriteFile(path, template.data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func build() ([]byte, error) {
	dir, err := os.MkdirTemp("", "registry-template")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "registry.db")
	r, err := registry.Open(path)
	if err != nil {
		return nil, err
	}
	if err := r.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	_, deleteErr := db.Exec(`DELETE FROM catalog_state; DELETE FROM host_identity`)
	_, checkpointErr := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if err := db.Close(); err != nil {
		return nil, err
	}
	if deleteErr != nil || checkpointErr != nil {
		return nil, fmt.Errorf("prepare the template registry: %v %v", deleteErr, checkpointErr)
	}
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > 0 {
		return nil, fmt.Errorf("the template registry kept %d bytes of WAL", info.Size())
	}
	return os.ReadFile(path)
}
