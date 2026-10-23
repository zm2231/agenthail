package surfaces

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/testkit"

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
	testkit.Eventually(t, "the reported window", 15*time.Second, func() bool {
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
	testkit.AppendLines(t, path, `{"type":"assistant","timestamp":"2099-01-01T00:00:00Z","message":{"id":"switched","model":"claude-sonnet-5-5","content":[{"type":"text","text":"from another model"}]}}`)
	if usage, err := c.ContextUsage(ctx, session); err != nil || usage == nil || usage.UsedTokens != 1000 || usage.ContextWindow != 0 || usage.ContextWindowSource != ClaudeContextWindowSourceUnknown {
		t.Fatalf("a window reported for another model was kept: usage=%+v err=%v", usage, err)
	}
	testkit.AppendLines(t, path, `{"type":"assistant","timestamp":"2099-01-01T00:00:01Z","message":{"id":"modelless","content":[{"type":"text","text":"no model named"}]}}`)
	testkit.AppendLines(t, path, `{"type":"assistant","timestamp":"2099-01-01T00:00:02Z","message":{"id":"back","model":"claude-opus-5-5","content":[{"type":"text","text":"back"}]}}`)
	if usage, err := c.ContextUsage(ctx, session); err != nil || usage == nil || usage.ContextWindowSource != ClaudeContextWindowSourceProvider {
		t.Fatalf("the window was not restored for the model it was reported for: usage=%+v err=%v", usage, err)
	}
	testkit.AppendLines(t, path, `{"type":"assistant","timestamp":"2099-01-01T00:00:03Z","message":{"id":"modelless-2","content":[{"type":"text","text":"no model named"}]}}`)
	if usage, err := c.ContextUsage(ctx, session); err != nil || usage == nil || usage.ContextWindowSource == ClaudeContextWindowSourceProvider {
		t.Fatalf("a reply that names no model kept the window: usage=%+v err=%v", usage, err)
	}
}

func TestAHostThatDoesNotAnswerDelaysOnlyTheSessionsItRuns(t *testing.T) {
	home, err := os.MkdirTemp("", "ch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	if err := os.MkdirAll(filepath.Dir(claudehost.SocketPath(home)), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", claudehost.SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	asked := make(chan net.Conn, 1)
	go func() {
		if conn, err := listener.Accept(); err == nil {
			asked <- conn
		}
	}()
	transcript := func(name string) string {
		path := filepath.Join(home, name+".jsonl")
		writeTestTranscript(t, path, `{"type":"assistant","timestamp":"2026-10-20T10:00:00Z","message":{"model":"claude-opus-5-5","usage":{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":990,"output_tokens":5}}}`)
		return path
	}
	c := NewClaude(nil, home)
	hosted := &surface.Session{ID: "hosted", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: os.Getpid(), Transcript: transcript("hosted")}
	terminal := &surface.Session{ID: "terminal", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transcript: transcript("terminal")}
	hostedDone := make(chan struct{})
	go func() {
		defer close(hostedDone)
		_, _ = c.ContextUsage(context.Background(), hosted)
	}()
	conn := <-asked
	defer conn.Close()
	replyless := &surface.Session{ID: "replyless", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: os.Getpid(), Transcript: filepath.Join(home, "absent.jsonl")}
	started := time.Now()
	if usage, err := c.ContextUsage(context.Background(), replyless); err != nil || usage != nil || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("a hosted session with no reply asked the host: usage=%+v err=%v after %s", usage, err, time.Since(started))
	}
	if usage, err := c.ContextUsage(context.Background(), terminal); err != nil || usage == nil || usage.UsedTokens != 1000 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	select {
	case <-hostedDone:
		t.Fatal("the hosted read finished before the host answered; the check proves nothing")
	default:
	}
	select {
	case <-hostedDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the hosted read did not give up on a host that does not answer")
	}
}

func TestAHostedClaudeSessionReportsWhenClaudeCompactsIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	host := claudehosttest.New(t, "")
	host.Start(t)
	if _, err := surface.WriteClaudeCompactSettings(surface.ClaudeConfigDir(host.Home), surface.ClaudeCompactChange{Window: "300000"}); err != nil {
		t.Fatal(err)
	}
	c := NewClaude(nil, host.Home)
	ctx := context.Background()
	session, _, err := c.StartSession(ctx, surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	var usage *surface.ContextUsage
	testkit.Eventually(t, "the reported autocompact", 15*time.Second, func() bool {
		usage, err = c.ContextUsage(ctx, session)
		return err == nil && usage != nil && usage.AutoCompact != nil
	})
	if want := (surface.AutoCompact{Enabled: true, Window: 300000, Threshold: 267000, DecidedBy: "settings", Source: surface.AutoCompactSourceRuntime}); *usage.AutoCompact != want {
		t.Fatalf("autoCompact=%+v", *usage.AutoCompact)
	}
	terminal := *session
	terminal.RuntimeKind = surface.RuntimeClaudeCLI
	if usage, err := NewClaude(nil, host.Home).ContextUsage(ctx, &terminal); err != nil || usage == nil || usage.AutoCompact != nil {
		t.Fatalf("a session the host does not run reported autocompact: usage=%+v err=%v", usage, err)
	}
}
