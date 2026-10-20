package surfaces

import (
	"context"
	"os"
	"testing"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAHostedClaudeSessionReportsTheWindowClaudeRunsItsModelWith(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	host := claudehosttest.New(t, "")
	host.Start(t)
	c := NewClaude(nil, host.Home)
	ctx := context.Background()
	session, _, err := c.StartSession(ctx, surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	if session.RuntimeKind != surface.RuntimeClaudeHost {
		t.Fatalf("session=%+v, want a hosted session", session)
	}
	var usage *surface.ContextUsage
	claudehosttest.WaitFor(t, "the reported window", func() bool {
		usage, err = c.ContextUsage(ctx, session)
		return err == nil && usage != nil && usage.ContextWindowSource == ClaudeContextWindowSourceProvider
	})
	if usage.ContextWindow != 1_000_000 || usage.UsedTokens != 1000 || usage.WindowEstimated {
		t.Fatalf("usage=%+v", usage)
	}

	terminal := *session
	terminal.RuntimeKind = surface.RuntimeClaudeCLI
	if usage, err := NewClaude(nil, host.Home).ContextUsage(ctx, &terminal); err != nil || usage == nil || usage.ContextWindow != 0 || usage.ContextWindowSource != ClaudeContextWindowSourceUnknown {
		t.Fatalf("a session the host does not run took its window: usage=%+v err=%v", usage, err)
	}

	path, err := c.sessionTranscript(session)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(`{"type":"assistant","timestamp":"2099-01-01T00:00:00Z","message":{"id":"switched","model":"claude-sonnet-5-5","usage":{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":90,"output_tokens":5}}}` + "\n")
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if usage, err := c.ContextUsage(ctx, session); err != nil || usage == nil || usage.UsedTokens != 100 || usage.ContextWindow != 0 || usage.ContextWindowSource != ClaudeContextWindowSourceUnknown {
		t.Fatalf("a window reported for another model was kept: usage=%+v err=%v", usage, err)
	}
}
