package surfaces

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

// hostInOtherRootThanCaller starts the Claude host as the daemon does, under
// the default root, and leaves this process running in a second root, as an
// agent's CLI in that root does.
func hostInOtherRootThanCaller(t *testing.T) (*claudetest.Fixture, *claudehosttest.Fixture, *Claude, string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture := claudetest.New(t)
	if err := surface.RecordDaemonClaudeRoot(fixture.Home); err != nil {
		t.Fatal(err)
	}
	second := fixture.AddRoot("second")
	host := claudehosttest.New(t, fixture.Home)
	host.Start(t)
	t.Setenv("CLAUDE_CONFIG_DIR", second)
	return fixture, host, NewClaude(nil, fixture.Home), second
}

func TestASendFromACallerInAnotherRootResumesTheConversationInTheRootThatHoldsIt(t *testing.T) {
	fixture, host, c, second := hostInOtherRootThanCaller(t)
	fixture.Transcript(fixtureConversation, fixture.Home)
	session := exitedSession(fixture)
	if _, err := c.SendHumanTurn(context.Background(), &session, "are you there", nil); err != nil {
		t.Fatal(err)
	}
	spawns := host.Spawns(t, fixtureConversation)
	if len(spawns) != 1 || spawns[0].ConfigDir == nil || *spawns[0].ConfigDir != second {
		t.Fatalf("spawns=%+v, want one resume told its config root %s", spawns, second)
	}
}

func TestASessionStartedFromACallerInAnotherRootRunsInTheRootItAskedFor(t *testing.T) {
	_, host, c, second := hostInOtherRootThanCaller(t)
	session, _, err := c.StartSession(context.Background(), surface.SessionStartOptions{Message: "hello", Cwd: t.TempDir(), ConfigRoot: second, Name: "in-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	spawns := host.Spawns(t, session.ID)
	if len(spawns) != 1 || spawns[0].ConfigDir == nil || *spawns[0].ConfigDir != second {
		t.Fatalf("spawns=%+v, want the new session told its config root %s", spawns, second)
	}
}

// A daemon restarted in another root attaches to the running host of its
// build, which keeps the environment it was started with.
func TestAResumeInTheDefaultRootThroughAHostStartedInAnotherRootRunsWithoutAConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture := claudetest.New(t)
	other := fixture.AddRoot("other")
	t.Setenv("CLAUDE_CONFIG_DIR", other)
	if err := surface.RecordDaemonClaudeRoot(fixture.Home); err != nil {
		t.Fatal(err)
	}
	host := claudehosttest.New(t, fixture.Home)
	host.Start(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := surface.RecordDaemonClaudeRoot(fixture.Home); err != nil {
		t.Fatal(err)
	}
	fixture.Root = filepath.Join(fixture.Home, ".claude")
	session := exitedSession(fixture)
	if _, err := NewClaude(nil, fixture.Home).SendHumanTurn(context.Background(), &session, "are you there", nil); err != nil {
		t.Fatal(err)
	}
	spawns := host.Spawns(t, fixtureConversation)
	if len(spawns) != 1 || spawns[0].ConfigDir != nil {
		t.Fatalf("spawns=%+v, want one resume run with CLAUDE_CONFIG_DIR unset, in the default root", spawns)
	}
}

func TestASessionStartedInThePrimaryRootThroughAHostStartedInAnotherRootRunsInThePrimary(t *testing.T) {
	for _, primary := range []string{"default", "configured"} {
		t.Run(primary, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			fixture := claudetest.New(t)
			other := fixture.AddRoot("other")
			t.Setenv("CLAUDE_CONFIG_DIR", other)
			host := claudehosttest.New(t, fixture.Home)
			host.Start(t)
			var want *string
			if primary == "configured" {
				root := filepath.Join(fixture.Home, "main")
				want = &root
				t.Setenv("CLAUDE_CONFIG_DIR", root)
			} else {
				t.Setenv("CLAUDE_CONFIG_DIR", "")
			}
			if err := surface.RecordDaemonClaudeRoot(fixture.Home); err != nil {
				t.Fatal(err)
			}
			session, _, err := NewClaude(nil, fixture.Home).StartSession(context.Background(), surface.SessionStartOptions{Message: "hello", Cwd: t.TempDir(), Name: "in-" + uuid.NewString()[:8]})
			if err != nil {
				t.Fatal(err)
			}
			spawns := host.Spawns(t, session.ID)
			if len(spawns) != 1 || (spawns[0].ConfigDir == nil) != (want == nil) || want != nil && *spawns[0].ConfigDir != *want {
				t.Fatalf("spawns=%+v, want one start with CLAUDE_CONFIG_DIR %v", spawns, want)
			}
		})
	}
}
