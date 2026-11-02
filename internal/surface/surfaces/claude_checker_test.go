package surfaces

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAStartedCheckerRunsInTheHostSandboxAndCannotBeChanged(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	host := claudehosttest.New(t, "")
	host.Start(t)
	c := NewClaude(nil, host.Home)
	ctx := context.Background()
	run := uuid.NewString()
	session, err := c.StartChecker(ctx, surface.CheckerStart{Run: run, Cwd: host.Cwd, Message: "check these words"})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != run || session.RuntimeKind != surface.RuntimeClaudeHost {
		t.Fatalf("checker session = %+v", session)
	}
	listing, err := c.host.client.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, hosted := range listing.Sessions {
		if hosted.SessionID == run {
			found = hosted.Checker == run && hosted.PermissionMode == "dontAsk"
		}
	}
	if !found {
		t.Fatalf("the host does not run %s as a sandboxed checker: %+v", run, listing.Sessions)
	}
	if _, err := c.host.client.SetPermissionMode(ctx, run, "bypassPermissions"); err == nil {
		t.Fatal("a checker was moved out of its sandbox")
	}
}
