package surfaces

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
)

func spawnMCPConfig(spawn claudehosttest.Spawn) string {
	for index, arg := range spawn.Argv {
		if value, ok := strings.CutPrefix(arg, "--mcp-config="); ok {
			return value
		}
		if arg == "--mcp-config" && index+1 < len(spawn.Argv) {
			return spawn.Argv[index+1]
		}
	}
	return ""
}

func TestAStartedClaudeHostConversationCarriesItsClaimedCredential(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	host := claudehosttest.New(t, "")
	host.Start(t)
	c := NewClaude(nil, host.Home)
	credentials := &fakeCredentials{}
	c.SetMCPCredentials(credentials)
	session, _, err := c.StartSession(context.Background(), surface.SessionStartOptions{Cwd: host.Cwd, Message: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	spawns := host.Spawns(t, session.ID)
	if len(spawns) != 1 || !strings.Contains(spawnMCPConfig(spawns[0]), "Bearer ahs_pending") || !slices.Contains(spawns[0].Argv, "mcp__agenthail") && !slices.Contains(spawns[0].Argv, "--allowedTools=mcp__agenthail") {
		t.Fatalf("spawn argv=%v", spawns)
	}
	if len(credentials.claims) != 1 || credentials.claims[0] != "ref-1="+session.ID {
		t.Fatalf("claims=%v", credentials.claims)
	}
}

func TestACheckerOpensWithoutTheAgenthailServer(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	host := claudehosttest.New(t, "")
	host.Start(t)
	c := NewClaude(nil, host.Home)
	credentials := &fakeCredentials{}
	c.SetMCPCredentials(credentials)
	run := uuid.NewString()
	if _, err := c.StartChecker(context.Background(), surface.CheckerStart{Run: run, Token: "run-secret", Cwd: host.Cwd, Message: "check"}); err != nil {
		t.Fatal(err)
	}
	if spawns := host.Spawns(t, run); len(spawns) != 1 || strings.Contains(spawnMCPConfig(spawns[0]), "agenthail") || len(credentials.claims) != 0 {
		t.Fatalf("checker spawn=%v claims=%v", spawns, credentials.claims)
	}
}

func TestAResumedClaudeConversationKeepsRotatesOrClaimsItsCredential(t *testing.T) {
	held := &fakeCredentials{live: map[string]string{"c": "ahs_live"}}
	if token, err := claudeHostToken(held, "c", true); err != nil || token != "ahs_live" || held.rotations != 0 {
		t.Fatalf("held token=%q err=%v rotations=%d", token, err, held.rotations)
	}
	registered := &fakeCredentials{}
	if token, err := claudeHostToken(registered, "c", true); err != nil || token != "ahs_rotated" || len(registered.claims) != 0 {
		t.Fatalf("rotated token=%q err=%v claims=%v", token, err, registered.claims)
	}
	unregistered := &fakeCredentials{rotateErr: errors.New("session c: not found")}
	if token, err := claudeHostToken(unregistered, "c", true); err != nil || token != "ahs_pending" || len(unregistered.claims) != 1 {
		t.Fatalf("claimed token=%q err=%v claims=%v", token, err, unregistered.claims)
	}
	if _, err := claudeHostToken(&fakeCredentials{}, "", false); err == nil {
		t.Fatal("a conversation without an id got a credential")
	}
}
