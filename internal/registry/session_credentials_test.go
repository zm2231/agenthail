package registry

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func credentialFixture(t *testing.T) (*Registry, string) {
	t.Helper()
	r := openTestRegistry(t)
	if err := r.RegisterSession(surface.Session{ID: "worker", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: "/work"}); err != nil {
		t.Fatal(err)
	}
	return r, "worker"
}

var readCaps = []string{"read.self", "read.project"}

func TestSessionCredentialStoresOnlyTheHash(t *testing.T) {
	r, session := credentialFixture(t)
	now := time.Now()
	issued, err := r.IssueSessionCredential(session, readCaps, json.RawMessage(`{"mode":"default"}`), now)
	if err != nil || !strings.HasPrefix(issued.Token, SessionTokenPrefix) || len(issued.Token) < 40 {
		t.Fatalf("issued=%+v err=%v", issued, err)
	}
	var stored string
	if err := r.db.QueryRow(`SELECT token_hash FROM session_credentials WHERE id=?`, issued.Credential.ID).Scan(&stored); err != nil || stored != HashSessionToken(issued.Token) {
		t.Fatalf("stored=%q err=%v", stored, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		raw, err := os.ReadFile(r.diskPath() + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), issued.Token) {
			t.Fatalf("raw token found in %s", r.diskPath()+suffix)
		}
	}
}

func TestSessionCredentialAuthenticatesAndRefreshesIdle(t *testing.T) {
	r, session := credentialFixture(t)
	now := time.Now()
	issued, err := r.IssueSessionCredential(session, readCaps, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	later := now.Add(23 * time.Hour)
	credential, err := r.AuthenticateSessionToken(issued.Token, later)
	if err != nil || credential.SessionID != session || !credential.IdleExpiresAt.After(later.Add(23*time.Hour)) {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
	for _, token := range []string{"", "ahs_unknown", "dashboard-token", strings.TrimPrefix(issued.Token, SessionTokenPrefix)} {
		if _, err := r.AuthenticateSessionToken(token, later); !errors.Is(err, ErrCredentialUnknown) {
			t.Fatalf("token %q err=%v", token, err)
		}
	}
}

func TestSessionCredentialReuseAndRotation(t *testing.T) {
	r, session := credentialFixture(t)
	now := time.Now()
	first, err := r.IssueSessionCredential(session, []string{"read.project", "read.self", "read.self"}, json.RawMessage(`{"mode": "default"}`), now)
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.IssueSessionCredential(session, readCaps, json.RawMessage(`{"mode":"default"}`), now.Add(time.Hour))
	if err != nil || again.Token != "" || again.Credential.ID != first.Credential.ID {
		t.Fatalf("reuse=%+v err=%v", again, err)
	}
	if _, err := r.AuthenticateSessionToken(first.Token, now.Add(time.Hour)); err != nil {
		t.Fatalf("reused token refused: %v", err)
	}
	raised, err := r.IssueSessionCredential(session, append(readCaps, "spawn"), json.RawMessage(`{"mode":"default"}`), now.Add(2*time.Hour))
	if err != nil || raised.Token == "" || raised.Credential.ID == first.Credential.ID {
		t.Fatalf("rotation=%+v err=%v", raised, err)
	}
	var revoked CredentialRevokedError
	if _, err := r.AuthenticateSessionToken(first.Token, now.Add(2*time.Hour)); !errors.As(err, &revoked) || revoked.Reason != RevokedRotated {
		t.Fatalf("old token err=%v", err)
	}
	if _, err := r.AuthenticateSessionToken(raised.Token, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("new token refused: %v", err)
	}
}

func TestSessionCredentialIdleExpirySuspendsUntilATurn(t *testing.T) {
	r, session := credentialFixture(t)
	now := time.Now()
	issued, err := r.IssueSessionCredential(session, readCaps, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	idle := now.Add(25 * time.Hour)
	if _, err := r.AuthenticateSessionToken(issued.Token, idle); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("idle err=%v", err)
	}
	if err := r.ReviveSessionCredential(session, idle); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateSessionToken(issued.Token, idle.Add(time.Minute)); err != nil {
		t.Fatalf("revived token refused: %v", err)
	}
}

func TestSessionCredentialMaxExpiryNeedsANewCredential(t *testing.T) {
	r, session := credentialFixture(t)
	now := time.Now()
	issued, err := r.IssueSessionCredential(session, readCaps, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	past := now.Add(CredentialMaxLifetime + time.Minute)
	if err := r.ReviveSessionCredential(session, past); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateSessionToken(issued.Token, past); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("max err=%v", err)
	}
	fresh, err := r.IssueSessionCredential(session, readCaps, nil, past)
	if err != nil || fresh.Token == "" || fresh.Credential.ID == issued.Credential.ID {
		t.Fatalf("fresh=%+v err=%v", fresh, err)
	}
	if _, err := r.AuthenticateSessionToken(fresh.Token, past); err != nil {
		t.Fatalf("fresh token refused: %v", err)
	}
}

func TestSessionCredentialRevokedWhenTheSessionEnds(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		end    func(*Registry, string) error
		reason string
	}{
		{"archived", func(r *Registry, id string) error { return r.ArchiveSession(id, now) }, RevokedSessionArchive},
		{"moved", func(r *Registry, id string) error {
			_, err := r.db.Exec(`UPDATE sessions SET host_id='other-host' WHERE id=?`, id)
			return err
		}, RevokedSessionMoved},
		{"revoked", func(r *Registry, id string) error { return r.RevokeSessionCredentials(id, RevokedByPerson, now) }, RevokedByPerson},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, session := credentialFixture(t)
			issued, err := r.IssueSessionCredential(session, readCaps, nil, now)
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.end(r, session); err != nil {
				t.Fatal(err)
			}
			var revoked CredentialRevokedError
			if _, err := r.AuthenticateSessionToken(issued.Token, now.Add(time.Minute)); !errors.As(err, &revoked) || revoked.Reason != tc.reason {
				t.Fatalf("err=%v", err)
			}
			if _, err := r.AuthenticateSessionToken(issued.Token, now.Add(2*time.Minute)); !errors.As(err, &revoked) || revoked.Reason != tc.reason {
				t.Fatalf("second err=%v", err)
			}
			if _, err := r.IssueSessionCredential(session, readCaps, nil, now.Add(time.Minute)); tc.reason != RevokedByPerson && err == nil {
				t.Fatal("issued a credential for an ended session")
			}
		})
	}
}

func TestSessionCredentialGoneWithItsSession(t *testing.T) {
	r, session := credentialFixture(t)
	issued, err := r.IssueSessionCredential(session, readCaps, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`DELETE FROM sessions WHERE id=?`, session); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateSessionToken(issued.Token, time.Now()); !errors.Is(err, ErrCredentialUnknown) {
		t.Fatalf("err=%v", err)
	}
}
