// Package registrytest gives tests a new registry without migrating one from
// an empty file each time.
package registrytest

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	_ "modernc.org/sqlite"
)

var template struct {
	once  sync.Once
	data  []byte
	host  int
	epoch int
	err   error
}

const (
	hostIDBytes = 6
	epochBytes  = 16
)

// Path writes a new registry at this build's schema into the test's temporary
// directory and returns its path. The schema is migrated once per test
// process. Each copy gets its own host id and host epoch here, because
// registry.Open does not touch a registry already at this schema. They are
// rewritten in the file's bytes with same-length hex, which leaves every
// SQLite record its size.
func Path(t testing.TB) string {
	t.Helper()
	template.once.Do(func() { template.err = build() })
	if template.err != nil {
		t.Fatal(template.err)
	}
	data := bytes.Clone(template.data)
	raw := make([]byte, hostIDBytes+epochBytes)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	hex.Encode(data[template.host+1:], raw[:hostIDBytes])
	hex.Encode(data[template.epoch:], raw[hostIDBytes:])
	path := filepath.Join(t.TempDir(), "registry.db")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func build() error {
	dir, err := os.MkdirTemp("", "registry-template")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "registry.db")
	r, err := registry.Open(path)
	if err != nil {
		return err
	}
	identity, identityErr := r.HostIdentity()
	epoch, _, epochErr := r.CatalogState()
	if err := r.Close(); err != nil {
		return err
	}
	if identityErr != nil || epochErr != nil {
		return fmt.Errorf("read the template registry's identity: %v %v", identityErr, epochErr)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	_, checkpointErr := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if err := db.Close(); err != nil {
		return err
	}
	if checkpointErr != nil {
		return fmt.Errorf("prepare the template registry: %v", checkpointErr)
	}
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > 0 {
		return fmt.Errorf("the template registry kept %d bytes of WAL", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	host, err := onlyOffset(data, identity.ID, 1+2*hostIDBytes)
	if err != nil {
		return fmt.Errorf("template host id: %w", err)
	}
	at, err := onlyOffset(data, epoch, 2*epochBytes)
	if err != nil {
		return fmt.Errorf("template host epoch: %w", err)
	}
	template.data, template.host, template.epoch = data, host, at
	return nil
}

func onlyOffset(data []byte, value string, length int) (int, error) {
	if len(value) != length {
		return 0, fmt.Errorf("%q is %d bytes, want %d", value, len(value), length)
	}
	if n := bytes.Count(data, []byte(value)); n != 1 {
		return 0, fmt.Errorf("%q appears %d times in the file, want once", value, n)
	}
	return bytes.Index(data, []byte(value)), nil
}
