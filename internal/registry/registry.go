package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
	_ "modernc.org/sqlite"
)

type Registry struct {
	db           *sql.DB
	read         *sql.DB
	path         string
	now          func() time.Time
	busyPolicy   func() (deliverypolicy.Mode, error)
	seen         deviceSeen
	retryBackoff time.Duration
}

func (r *Registry) SetRetryBackoff(backoff time.Duration) {
	r.retryBackoff = backoff
}

func (r *Registry) SetBusyPolicy(policy func() (deliverypolicy.Mode, error)) {
	r.busyPolicy = policy
}

func (r *Registry) busyMode() (deliverypolicy.Mode, error) {
	if r.busyPolicy == nil {
		return deliverypolicy.Queue, nil
	}
	return r.busyPolicy()
}

func (r *Registry) Steers(busyDelivery string) (bool, error) {
	if busyDelivery != "" {
		return busyDelivery == string(deliverypolicy.Steer), nil
	}
	mode, err := r.busyMode()
	return mode == deliverypolicy.Steer, err
}

var generatedAliasCharacters = regexp.MustCompile(`[^a-z0-9._-]+`)

const (
	schemaVersion    = 39
	readConnections  = 4
	deadReviewWindow = time.Hour
)

func SchemaVersion() int { return schemaVersion }

type NewerSchemaError struct {
	Found        int
	Supported    int
	Path         string
	Backup       string
	BackupSchema int
}

func (e *NewerSchemaError) Error() string {
	refusal := fmt.Sprintf("registry is schema %d; this agenthail supports %d; install a newer agenthail", e.Found, e.Supported)
	if e.Backup == "" {
		return refusal
	}
	aside := strings.TrimSuffix(e.Path, filepath.Ext(e.Path)) + fmt.Sprintf("-v%d.db", e.Found)
	return fmt.Sprintf("%s, or go back to the copy taken before that upgrade (schema %d; anything changed since is lost): agenthail daemon stop; mv -- %s %s; rm -f -- %s %s; cp -- %s %s; agenthail daemon start", refusal, e.BackupSchema, shellQuote(e.Path), shellQuote(aside), shellQuote(e.Path+"-wal"), shellQuote(e.Path+"-shm"), shellQuote(e.Backup), shellQuote(e.Path))
}

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

func defaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "registry.db")
}

func Open(path string) (*Registry, error) {
	return open(path, false, true)
}

func OpenDefault(allowUpgrade bool) (*Registry, error) {
	return open(defaultPath(), true, allowUpgrade)
}

func open(path string, private, allowUpgrade bool) (*Registry, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create registry directory: %w", err)
	}
	if private {
		if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
			return nil, fmt.Errorf("restrict registry directory: %w", err)
		}
	}
	r, err := connect(path)
	if err != nil {
		return nil, err
	}
	current, err := r.current()
	if err != nil {
		r.Close()
		return nil, err
	}
	if current {
		return r, nil
	}
	if err := r.migrate(allowUpgrade); err != nil {
		r.Close()
		return nil, err
	}
	if _, err := r.db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		r.Close()
		return nil, fmt.Errorf("enable registry WAL: %w", err)
	}
	return r, nil
}

func OpenCurrent(path string) (*Registry, error) {
	if path == "" {
		path = defaultPath()
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	r, err := connect(path)
	if err != nil {
		return nil, err
	}
	var version int
	if err := r.read.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		r.Close()
		return nil, err
	}
	if version != schemaVersion {
		r.Close()
		return nil, fmt.Errorf("registry is schema %d; this agenthail uses %d", version, schemaVersion)
	}
	return r, nil
}

func (r *Registry) current() (bool, error) {
	var version int
	if err := r.read.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return false, err
	}
	var mode string
	if err := r.read.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		return false, err
	}
	return version == schemaVersion && mode == "wal", nil
}

func connect(path string) (*Registry, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	db, err := sql.Open("sqlite", path+sep+"_pragma=foreign_keys(1)&_pragma=busy_timeout(15000)&_pragma=synchronous(NORMAL)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	read, err := sql.Open("sqlite", path+sep+"_pragma=busy_timeout(15000)&_pragma=query_only(1)")
	if err != nil {
		db.Close()
		return nil, err
	}
	read.SetMaxOpenConns(readConnections)
	read.SetMaxIdleConns(readConnections)
	return &Registry{db: db, read: read, path: path, now: time.Now, retryBackoff: QueueRetryBackoff}, nil
}

func (r *Registry) Path() string { return r.path }

func (r *Registry) Close() error {
	r.seen.mu.Lock()
	r.seen.closed = true
	r.seen.mu.Unlock()
	r.seen.writes.Wait()
	r.seen.mu.Lock()
	pending := r.seen.pending
	r.seen.pending = nil
	r.seen.mu.Unlock()
	r.writeStamps(pending)
	return errors.Join(r.read.Close(), r.db.Close())
}
