package registry

import (
	"errors"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestPendingCredentialAuthenticatesOnlyOnceItsSessionRegisters(t *testing.T) {
	r := openTestRegistry(t)
	now := time.Now()
	pending, err := r.IssuePendingCredential(readCaps, nil, now, time.Minute)
	if err != nil || pending.Token == "" || pending.Ref == "" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if _, err := r.AuthenticateSessionToken(pending.Token, now); !errors.Is(err, ErrCredentialUnknown) {
		t.Fatalf("unbound credential authenticated: %v", err)
	}
	thread := surface.Session{ID: "thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, CredentialRef: pending.Ref}
	if err := r.RegisterSession(thread); err != nil {
		t.Fatal(err)
	}
	credential, err := r.AuthenticateSessionToken(pending.Token, now)
	if err != nil || credential.SessionID != "thread" {
		t.Fatalf("bound credential=%+v err=%v", credential, err)
	}
	other := surface.Session{ID: "other", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, CredentialRef: pending.Ref}
	if err := r.RegisterSession(other); err != nil {
		t.Fatal(err)
	}
	if credential, err := r.AuthenticateSessionToken(pending.Token, now); err != nil || credential.SessionID != "thread" {
		t.Fatalf("a second registration rebound the credential: %+v %v", credential, err)
	}
	thread.CredentialRef = ""
	if err := r.RegisterSession(thread); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateSessionToken(pending.Token, now); err != nil {
		t.Fatalf("re-registration without a ref changed the credential: %v", err)
	}
}

func TestBindingAPendingCredentialReplacesTheSessionsLiveOne(t *testing.T) {
	r, session := credentialFixture(t)
	now := time.Now()
	old, err := r.IssueSessionCredential(session, readCaps, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := r.IssuePendingCredential(readCaps, nil, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{ID: session, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: "/work", CredentialRef: pending.Ref}); err != nil {
		t.Fatal(err)
	}
	var revoked CredentialRevokedError
	if _, err := r.AuthenticateSessionToken(old.Token, now); !errors.As(err, &revoked) || revoked.Reason != RevokedRotated {
		t.Fatalf("old credential err=%v", err)
	}
	if _, err := r.AuthenticateSessionToken(pending.Token, now); err != nil {
		t.Fatalf("bound credential refused: %v", err)
	}
}

func TestExpiredPendingCredentialIsDeletedAndNeverBinds(t *testing.T) {
	r := openTestRegistry(t)
	later := time.Now()
	stale, err := r.IssuePendingCredential(readCaps, nil, later.Add(-2*time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	thread := surface.Session{ID: "late", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, CredentialRef: stale.Ref}
	if err := r.RegisterSession(thread); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateSessionToken(stale.Token, later); !errors.Is(err, ErrCredentialUnknown) {
		t.Fatalf("expired pending credential bound: %v", err)
	}
	if err := r.PrunePendingCredentials(later); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM session_credentials WHERE token_hash=?`, HashSessionToken(stale.Token)).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rows=%d err=%v", rows, err)
	}
}

func TestBoundOnlyCredentialTableUpgradesKeepingCredentials(t *testing.T) {
	r, session := credentialFixture(t)
	now := time.Now()
	issued, err := r.IssueSessionCredential(session, readCaps, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE session_credentials RENAME TO current_credentials`,
		`DROP INDEX session_credentials_live`,
		`CREATE TABLE session_credentials (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE, token_hash TEXT NOT NULL UNIQUE, capabilities TEXT NOT NULL DEFAULT '[]', ceiling TEXT NOT NULL DEFAULT '{}', created_at_ms INTEGER NOT NULL, last_used_at_ms INTEGER NOT NULL DEFAULT 0, idle_expires_at_ms INTEGER NOT NULL, max_expires_at_ms INTEGER NOT NULL, revoked_at_ms INTEGER NOT NULL DEFAULT 0, revoked_reason TEXT NOT NULL DEFAULT '')`,
		`CREATE UNIQUE INDEX session_credentials_live ON session_credentials(session_id) WHERE revoked_at_ms=0`,
		`INSERT INTO session_credentials(id,session_id,token_hash,capabilities,ceiling,created_at_ms,last_used_at_ms,idle_expires_at_ms,max_expires_at_ms,revoked_at_ms,revoked_reason) SELECT id,session_id,token_hash,capabilities,ceiling,created_at_ms,last_used_at_ms,idle_expires_at_ms,max_expires_at_ms,revoked_at_ms,revoked_reason FROM current_credentials`,
		`DROP TABLE current_credentials`,
		`PRAGMA user_version=42`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	path := r.diskPath()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if credential, err := reopened.AuthenticateSessionToken(issued.Token, now); err != nil || credential.SessionID != session {
		t.Fatalf("credential after upgrade=%+v err=%v", credential, err)
	}
	if _, err := reopened.IssueSessionCredential(session, append(readCaps, "spawn"), nil, now); err != nil {
		t.Fatalf("live index missing after upgrade: %v", err)
	}
	var live int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM session_credentials WHERE session_id=? AND revoked_at_ms=0`, session).Scan(&live); err != nil || live != 1 {
		t.Fatalf("live=%d err=%v", live, err)
	}
	if _, err := reopened.IssuePendingCredential(readCaps, nil, now, time.Minute); err != nil {
		t.Fatalf("pending after upgrade: %v", err)
	}
}
