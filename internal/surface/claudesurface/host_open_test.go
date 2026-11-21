package claudesurface

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
)

func TestAStartedClaudeHostConversationCarriesItsClaimedCredential(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	host := claudehosttest.New(t, "")
	host.Start(t)
	c := surfacetest.InDaemon(NewClaude(nil, host.Home))
	credentials := &surfacetest.FakeCredentials{}
	c.SetMCPCredentials(credentials)
	session, _, err := c.StartSession(context.Background(), surface.SessionStartOptions{Cwd: host.Cwd, Message: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	spawns := host.Spawns(t, session.ID)
	if len(spawns) != 1 {
		t.Fatalf("spawns=%d", len(spawns))
	}
	attached := host.Attached(t, session.ID)
	bearer := len(attached) == 1 && attached[0].PID == spawns[0].PID && strings.HasPrefix(attached[0].Bearer, "ahs_pending") && !strings.Contains(strings.Join(spawns[0].Argv, " "), attached[0].Bearer)
	allowed := slices.Contains(spawns[0].Argv, "mcp__agenthail") || slices.Contains(spawns[0].Argv, "--allowedTools=mcp__agenthail")
	if !bearer || !allowed {
		t.Fatalf("pending bearer passed=%v, agenthail tools allowed=%v", bearer, allowed)
	}
	if len(credentials.Claims) != 1 || credentials.Claims[0] != "ref-1="+session.ID {
		t.Fatalf("claims=%v", credentials.Claims)
	}
}

func TestACheckerOpensWithoutTheAgenthailServer(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	host := claudehosttest.New(t, "")
	host.Start(t)
	c := surfacetest.InDaemon(NewClaude(nil, host.Home))
	credentials := &surfacetest.FakeCredentials{}
	c.SetMCPCredentials(credentials)
	run := uuid.NewString()
	if _, err := c.StartChecker(context.Background(), surface.CheckerStart{Run: run, Token: "run-secret", Cwd: host.Cwd, Message: "check"}); err != nil {
		t.Fatal(err)
	}
	if spawns := host.Spawns(t, run); len(spawns) != 1 || len(host.Attached(t, run)) != 0 || len(credentials.Claims) != 0 {
		t.Fatalf("checker spawns=%d claims=%d, a checker must not get the agenthail server", len(spawns), len(credentials.Claims))
	}
}

func TestAResumedClaudeConversationKeepsRotatesOrClaimsItsCredential(t *testing.T) {
	ctx := context.Background()
	held := &surfacetest.FakeCredentials{LiveTokens: map[string]string{"c": "ahs_live"}}
	if token, err := claudeHostToken(ctx, held, "c", true); err != nil || token != "ahs_live" || held.Rotations != 0 {
		t.Fatalf("err=%v rotations=%d, the held token was not reused", err, held.Rotations)
	}
	registered := &surfacetest.FakeCredentials{}
	if token, err := claudeHostToken(ctx, registered, "c", true); err != nil || token != "ahs_rotated" || len(registered.Claims) != 0 {
		t.Fatalf("err=%v claims=%d, the credential was not rotated", err, len(registered.Claims))
	}
	unregistered := &surfacetest.FakeCredentials{RotateErr: errors.New("session c: not found")}
	if token, err := claudeHostToken(ctx, unregistered, "c", true); err != nil || token != "ahs_pending" || len(unregistered.Claims) != 1 {
		t.Fatalf("err=%v claims=%d, no pending credential was claimed", err, len(unregistered.Claims))
	}
	if _, err := claudeHostToken(ctx, &surfacetest.FakeCredentials{}, "", false); err == nil {
		t.Fatal("a conversation without an id got a credential")
	}
}
