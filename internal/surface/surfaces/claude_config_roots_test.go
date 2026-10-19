package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestClaudeSessionInASecondConfigRootIsListedReadSentStoppedAndResumed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture, host, c := hostResume(t)
	root := fixture.AddRoot("claude-work")
	fixture.Listen()
	transcript := fixture.Transcript(fixtureConversation, fixture.Home)
	fixture.Record(fixtureConversation, fixture.Home, "")
	peers := claudetest.ListenPeerManager(t, fixture.Home)
	ctx := context.Background()

	sessions, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var session *surface.Session
	for i := range sessions {
		if sessions[i].ID == fixtureConversation {
			session = &sessions[i]
		}
	}
	if session == nil || session.Transcript != transcript || session.PID != fixture.PID {
		t.Fatalf("sessions=%+v, want the second root's session with its transcript", sessions)
	}

	read, err := c.ReadSession(ctx, &surface.Session{ID: fixtureConversation}, surface.SessionReadRequest{Limit: 5})
	if err != nil || read.Reply == nil || read.Reply.Text != "fixture answer" {
		t.Fatalf("read=%+v err=%v", read, err)
	}

	if err := surface.EnsureWritableSession(ctx, c, session); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(ctx, session, "to the second root"); err != nil || peers.Count("to the second root") != 1 {
		t.Fatalf("send err=%v messages=%v", err, peers.Messages())
	}

	fixture.Job("fe758937", fixtureConversation, fixture.Home, "working", fixture.PID)
	if _, err := c.SessionAction(ctx, &surface.Session{ID: fixtureConversation}, "stop"); err != nil {
		t.Fatal(err)
	}
	if fixture.InvocationsIn(root, "stop", "fe758937") != 1 {
		t.Fatal("stop did not run against the session's config root")
	}

	fixture.RemoveRecord()
	fixture.Job("fe758937", fixtureConversation, fixture.Home, "done", 0)
	result, err := c.SessionAction(ctx, &surface.Session{ID: fixtureConversation}, "resume")
	if err != nil || result["unchanged"] != false {
		t.Fatalf("result=%v err=%v", result, err)
	}
	spawns := host.Spawns(t, fixtureConversation)
	if len(spawns) != 1 || spawns[0].ConfigDir != root {
		t.Fatalf("spawns=%+v, want one resume in %s", spawns, root)
	}
}

func TestClaudeConversationInTwoConfigRootsIsRefused(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture := claudetest.New(t)
	fixture.Transcript(fixtureConversation, fixture.Home)
	fixture.AddRoot("claude-work")
	fixture.Transcript(fixtureConversation, fixture.Home)
	fixture.Record(fixtureConversation, fixture.Home, "session_fixture")
	c := NewClaude(nil, fixture.Home)
	ctx := context.Background()
	session := &surface.Session{ID: fixtureConversation, Surface: surface.KindClaude, Bridge: "session_fixture", PID: fixture.PID}
	ambiguous := func(err error) bool {
		return err != nil && strings.Contains(err.Error(), "more than one Claude config directory")
	}
	if err := surface.EnsureWritableSession(ctx, c, session); !ambiguous(err) {
		t.Fatalf("send check err=%v", err)
	}
	if _, err := c.ReadSession(ctx, session, surface.SessionReadRequest{Limit: 5}); !ambiguous(err) {
		t.Fatalf("read err=%v", err)
	}
	if _, err := c.SessionAction(ctx, session, "stop"); !ambiguous(err) {
		t.Fatalf("stop err=%v", err)
	}
	if _, err := c.Send(ctx, session, "hello"); !ambiguous(err) {
		t.Fatalf("send err=%v", err)
	}
	if _, err := c.SendHumanTurn(ctx, session, "hello", nil); !ambiguous(err) {
		t.Fatalf("human turn err=%v", err)
	}
	if err := c.Steer(ctx, session, "hello"); !ambiguous(err) {
		t.Fatalf("steer err=%v", err)
	}
	if err := c.Interrupt(ctx, session); !ambiguous(err) {
		t.Fatalf("interrupt err=%v", err)
	}
	if err := c.Compact(ctx, session); !ambiguous(err) {
		t.Fatalf("compact err=%v", err)
	}
	if _, err := c.Model(ctx, session, "opus"); !ambiguous(err) {
		t.Fatalf("model err=%v", err)
	}
}

func TestClaudeHostedConversationInTwoConfigRootsIsRefused(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	host := claudehosttest.New(t, "")
	claudetest.New(t)
	host.Start(t)
	c := NewClaude(nil, host.Home)
	ctx := context.Background()
	session, _, err := c.StartSession(ctx, surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	listedSession(t, c, session.ID)
	second := filepath.Join(host.Home, "claude-work")
	duplicate := filepath.Join(second, "projects", "copied", session.ID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(duplicate), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(duplicate, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(host.Home, ".agenthail", "config.json"), []byte(`{"claudeConfigDirs":["`+second+`"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"status", "stop", "resume"} {
		if _, err := c.SessionAction(ctx, session, action); err == nil || !strings.Contains(err.Error(), "more than one Claude config directory") {
			t.Fatalf("%s err=%v", action, err)
		}
	}
	if err := c.Interrupt(ctx, session); err == nil || !strings.Contains(err.Error(), "more than one Claude config directory") {
		t.Fatalf("interrupt err=%v", err)
	}
	if !claudehosttest.Alive(session.PID) || len(host.Spawns(t, session.ID)) != 1 {
		t.Fatal("a refused lifecycle action reached the host")
	}
}

func TestClaudeBadAgenthailConfigFailsDiscovery(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for name, config := range map[string]string{
		"not json":     `{"claudeConfigDirs":`,
		"null":         `null`,
		"array":        `["/tmp/claude"]`,
		"wrong type":   `{"claudeConfigDirs":"/tmp/claude"}`,
		"null list":    `{"claudeConfigDirs":null}`,
		"relative":     `{"claudeConfigDirs":["claude-work"]}`,
		"empty":        `{"claudeConfigDirs":[""]}`,
		"oversized":    `{"claudeConfigDirs":[],"padding":"` + strings.Repeat("x", 64*1024) + `"}`,
		"not a folder": `{"claudeConfigDirs":["/etc/hosts"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".agenthail", "config.json"), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			sessions, err := NewClaude(nil, home).List(context.Background())
			if err == nil || !strings.Contains(err.Error(), filepath.Join(home, ".agenthail", "config.json")) {
				t.Fatalf("sessions=%v err=%v", sessions, err)
			}
		})
	}
}
