package registry

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestPendingCredentialAuthenticatesOnlyOnceItsSessionRegisters(t *testing.T) {
	r := openTestRegistry(t)
	now := time.Now()
	pending, err := r.IssuePendingCredential(nil, now, time.Minute)
	if err != nil || pending.Token == "" || pending.Ref == "" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if _, err := r.AuthenticateSessionToken(pending.Token, now); !errors.Is(err, ErrCredentialUnknown) {
		t.Fatalf("unbound credential authenticated: %v", err)
	}
	if claimed, err := r.ClaimPendingCredential(pending.Ref, "thread", now); err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	if claimed, err := r.ClaimPendingCredential(pending.Ref, "other", now); err != nil || claimed {
		t.Fatalf("second claim=%v err=%v", claimed, err)
	}
	if _, err := r.AuthenticateSessionToken(pending.Token, now); !errors.Is(err, ErrCredentialUnknown) {
		t.Fatalf("claimed but unregistered credential authenticated: %v", err)
	}
	other := surface.Session{ID: "other", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}
	if err := r.RegisterSession(other); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateSessionToken(pending.Token, now); !errors.Is(err, ErrCredentialUnknown) {
		t.Fatalf("another session's registration bound the credential: %v", err)
	}
	thread := surface.Session{ID: "thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}
	if err := r.RegisterSession(thread); err != nil {
		t.Fatal(err)
	}
	credential, err := r.AuthenticateSessionToken(pending.Token, now)
	if err != nil || credential.SessionID != "thread" {
		t.Fatalf("bound credential=%+v err=%v", credential, err)
	}
	if err := r.RegisterSession(thread); err != nil {
		t.Fatal(err)
	}
	if again, err := r.AuthenticateSessionToken(pending.Token, now); err != nil || again.ID != credential.ID {
		t.Fatalf("re-registration changed the credential: %+v %v", again, err)
	}
}

func TestAClaimForAnAlreadyRegisteredSessionBindsAtOnce(t *testing.T) {
	r, session := credentialFixture(t)
	now := time.Now()
	pending, err := r.IssuePendingCredential(nil, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := r.ClaimPendingCredential(pending.Ref, session, now); err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	if credential, err := r.AuthenticateSessionToken(pending.Token, now); err != nil || credential.SessionID != session {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
}

func TestAPendingCeilingBecomesTheSessionsCeilingWhenItBinds(t *testing.T) {
	r, session := credentialFixture(t)
	now := time.Now()
	ceiling := json.RawMessage(`{"permission":"default","roots":["/w"]}`)
	pending, err := r.IssuePendingCredential(ceiling, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := r.ClaimPendingCredential(pending.Ref, session, now); err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	if got, err := r.SessionCeiling(session); err != nil || string(got) != string(ceiling) {
		t.Fatalf("ceiling=%s err=%v", got, err)
	}
	var left int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM pending_ceilings`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("pending ceilings left=%d err=%v", left, err)
	}
	unbounded, err := r.IssuePendingCredential(json.RawMessage(`{}`), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM pending_ceilings`).Scan(&left); err != nil || left != 0 || unbounded.Token == "" {
		t.Fatalf("an unbounded start stored a ceiling: left=%d err=%v", left, err)
	}
}

func TestBindingAPendingCredentialReplacesTheSessionsLiveOne(t *testing.T) {
	r, session := credentialFixture(t)
	now := time.Now()
	old, err := r.IssueSessionCredential(session, now)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := r.IssuePendingCredential(nil, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := r.ClaimPendingCredential(pending.Ref, session, now); err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
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
	stale, err := r.IssuePendingCredential(nil, later.Add(-2*time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claimedLate, err := r.IssuePendingCredential(nil, later.Add(-2*time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := r.ClaimPendingCredential(claimedLate.Ref, "slow", later.Add(-90*time.Second)); err != nil || !claimed {
		t.Fatalf("claim in time=%v err=%v", claimed, err)
	}
	if claimed, err := r.ClaimPendingCredential(stale.Ref, "late", later); err != nil || claimed {
		t.Fatalf("expired claim=%v err=%v", claimed, err)
	}
	for _, id := range []string{"late", "slow"} {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}); err != nil {
			t.Fatal(err)
		}
	}
	for _, token := range []string{stale.Token, claimedLate.Token} {
		if _, err := r.AuthenticateSessionToken(token, later); !errors.Is(err, ErrCredentialUnknown) {
			t.Fatalf("expired pending credential bound: %v", err)
		}
	}
	if err := r.PrunePendingCredentials(later); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM session_credentials WHERE session_id IS NULL`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rows=%d err=%v", rows, err)
	}
}

func TestBoundOnlyCredentialTableUpgradesKeepingCredentials(t *testing.T) {
	r, session := credentialFixture(t)
	now := time.Now()
	issued, err := r.IssueSessionCredential(session, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE session_credentials RENAME TO current_credentials`,
		`DROP INDEX session_credentials_live`,
		`CREATE TABLE session_credentials (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE, token_hash TEXT NOT NULL UNIQUE, capabilities TEXT NOT NULL DEFAULT '[]', ceiling TEXT NOT NULL DEFAULT '{}', created_at_ms INTEGER NOT NULL, last_used_at_ms INTEGER NOT NULL DEFAULT 0, idle_expires_at_ms INTEGER NOT NULL, max_expires_at_ms INTEGER NOT NULL, revoked_at_ms INTEGER NOT NULL DEFAULT 0, revoked_reason TEXT NOT NULL DEFAULT '')`,
		`CREATE UNIQUE INDEX session_credentials_live ON session_credentials(session_id) WHERE revoked_at_ms=0`,
		`INSERT INTO session_credentials(id,session_id,token_hash,capabilities,ceiling,created_at_ms,last_used_at_ms,idle_expires_at_ms,max_expires_at_ms,revoked_at_ms,revoked_reason) SELECT id,session_id,token_hash,'[]','{}',created_at_ms,last_used_at_ms,idle_expires_at_ms,max_expires_at_ms,revoked_at_ms,revoked_reason FROM current_credentials`,
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
	if _, err := reopened.RotateSessionCredential(session, now); err != nil {
		t.Fatalf("live index missing after upgrade: %v", err)
	}
	var live int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM session_credentials WHERE session_id=? AND revoked_at_ms=0`, session).Scan(&live); err != nil || live != 1 {
		t.Fatalf("live=%d err=%v", live, err)
	}
	if _, err := reopened.IssuePendingCredential(nil, now, time.Minute); err != nil {
		t.Fatalf("pending after upgrade: %v", err)
	}
}
