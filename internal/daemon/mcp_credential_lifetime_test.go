package daemon

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type runningSurface struct {
	*daemonSurface
	running bool
	during  func()
}

func (s *runningSurface) RuntimeRunning(*surface.Session) bool { return s.running }

func (s *runningSurface) Send(ctx context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	if s.during != nil {
		s.during()
	}
	return s.daemonSurface.Send(ctx, session, message)
}

func credentialFixture(t *testing.T) (*Daemon, *registry.Registry, *runningSurface, surface.Session, string, *bytes.Buffer) {
	t.Helper()
	d, r, fake, from, _ := daemonFixture(t)
	adapter := &runningSurface{daemonSurface: fake}
	d.Surfaces = []surface.Surface{adapter}
	var logged bytes.Buffer
	d.log = log.New(&logged, "", 0)
	token := issueMCPToken(t, d, from)
	return d, r, adapter, from, token, &logged
}

func idleFor(t *testing.T, r *registry.Registry, sessionID string, column string) {
	t.Helper()
	if _, err := r.WriteDB().Exec(`UPDATE session_credentials SET `+column+`=? WHERE session_id=?`, time.Now().Add(-time.Minute).UnixMilli(), sessionID); err != nil {
		t.Fatal(err)
	}
}

func TestMCPCredentialUnusedForADayWorksInTheTurnAQueuedSendStarts(t *testing.T) {
	d, r, adapter, from, token, _ := credentialFixture(t)
	server := mcpServer(t, d)
	idleFor(t, r, from.ID, "idle_expires_at_ms")
	header := map[string]string{"Authorization": "Bearer " + token}
	if status, body := postMCP(t, server, header); status != http.StatusUnauthorized || errorCode(body) != "credential_expired" || body["retryable"] != true {
		t.Fatalf("expired status=%d body=%v", status, body)
	}
	var whoami map[string]any
	var refused bool
	adapter.during = func() { whoami, refused = callMCP(t, mcpSession(t, server, token), "agenthail_whoami", nil) }
	if err := r.QueueMessage(from.ID, "work after a quiet day"); err != nil {
		t.Fatal(err)
	}
	d.drainMessageQueue(context.Background(), adapter, &from)
	if len(adapter.sent) != 1 {
		t.Fatalf("sent=%v", adapter.sent)
	}
	if refused || whoami["sessionId"] != from.ID {
		t.Fatalf("whoami during the delivered turn=%v refused=%v", whoami, refused)
	}
}

func TestMCPRunningSessionKeepsItsCredentialPastADayUnused(t *testing.T) {
	d, r, adapter, from, token, _ := credentialFixture(t)
	server := mcpServer(t, d)
	idleFor(t, r, from.ID, "idle_expires_at_ms")
	adapter.running = true
	if whoami, refused := callMCP(t, mcpSession(t, server, token), "agenthail_whoami", nil); refused || whoami["sessionId"] != from.ID {
		t.Fatalf("whoami=%v refused=%v", whoami, refused)
	}
	adapter.running = false
	if status, body := postMCP(t, server, map[string]string{"Authorization": "Bearer " + token}); status != http.StatusOK {
		t.Fatalf("the running call did not renew the credential: status=%d body=%v", status, body)
	}
}

func TestMCPStoppedSessionPastADayUnusedIsRefusedAndLogged(t *testing.T) {
	d, r, _, from, token, logged := credentialFixture(t)
	if err := r.SetAlias("quiet", from.ID); err != nil {
		t.Fatal(err)
	}
	server := mcpServer(t, d)
	idleFor(t, r, from.ID, "idle_expires_at_ms")
	status, body := postMCP(t, server, map[string]string{"Authorization": "Bearer " + token})
	detail, _ := body["detail"].(map[string]any)
	if status != http.StatusUnauthorized || errorCode(body) != "credential_expired" || detail["reason"] != "idle" {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if line := logged.String(); !strings.Contains(line, "@quiet") || !strings.Contains(line, "not running") {
		t.Fatalf("daemon log=%q", line)
	}
}

func TestMCPRevokedAndMaxExpiredCredentialsStayRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expire func(t *testing.T, r *registry.Registry, sessionID string)
		code   string
	}{
		{"max", func(t *testing.T, r *registry.Registry, sessionID string) {
			idleFor(t, r, sessionID, "max_expires_at_ms")
		}, "credential_expired"},
		{"revoked", func(t *testing.T, r *registry.Registry, sessionID string) {
			if err := r.RevokeSessionCredentials(sessionID, registry.RevokedRotated, time.Now()); err != nil {
				t.Fatal(err)
			}
		}, "credential_revoked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, r, adapter, from, token, _ := credentialFixture(t)
			server := mcpServer(t, d)
			tc.expire(t, r, from.ID)
			idleFor(t, r, from.ID, "idle_expires_at_ms")
			adapter.running = true
			if err := r.QueueMessage(from.ID, "work for a retired credential"); err != nil {
				t.Fatal(err)
			}
			d.drainMessageQueue(context.Background(), adapter, &from)
			if status, body := postMCP(t, server, map[string]string{"Authorization": "Bearer " + token}); status != http.StatusUnauthorized || errorCode(body) != tc.code {
				t.Fatalf("status=%d body=%v", status, body)
			}
		})
	}
}

type goalTurnSurface struct {
	*goalCommandSurface
	during func()
}

func (s *goalTurnSurface) RunGoalCommand(ctx context.Context, session *surface.Session, command string) error {
	s.during()
	return s.goalCommandSurface.RunGoalCommand(ctx, session, command)
}

func TestMCPCredentialUnusedForADayWorksInTheTurnAQueuedGoalStarts(t *testing.T) {
	d, r, held, session := goalCommandFixture(t, surface.StatusIdle)
	server := mcpServer(t, d)
	token := issueMCPToken(t, d, session)
	idleFor(t, r, session.ID, "idle_expires_at_ms")
	status := make(chan int, 1)
	adapter := &goalTurnSurface{goalCommandSurface: held, during: func() {
		code, _ := postMCP(t, server, map[string]string{"Authorization": "Bearer " + token})
		status <- code
	}}
	d.Surfaces = []surface.Surface{adapter}
	if _, err := r.QueueGoal(session.ID, "/goal tests pass"); err != nil {
		t.Fatal(err)
	}
	d.drainMessageQueue(context.Background(), adapter, &session)
	select {
	case code := <-status:
		if code != http.StatusOK {
			t.Fatalf("a call during the goal's turn got status %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the queued goal never ran")
	}
}
