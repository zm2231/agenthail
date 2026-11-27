package claudesurface

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/claudesurface/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func removedWorktreeSession(t *testing.T, roots func(worktree string) []string) (checkout, worktree string, session surface.Session, c *Claude, host *claudehosttest.Fixture, peers *claudetest.PeerManager, prompted func() string) {
	t.Helper()
	fixture, host, c := hostResume(t)
	peers = claudetest.ListenPeerManager(t, fixture.Home)
	checkout = filepath.Join(fixture.Home, "work", "repo")
	worktree = filepath.Join(checkout, ".worktrees", "task")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	session = exitedSession(fixture)
	session.Cwd = worktree
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	c.BoundFolders(func(id string) ([]string, error) {
		if id != fixtureConversation {
			t.Errorf("bounds asked for %s", id)
		}
		return roots(worktree), nil
	})
	prompted = func() string {
		data, err := os.ReadFile(filepath.Join(host.ClaudeDir, "hooks.jsonl"))
		if err != nil {
			return ""
		}
		var told []string
		for line := range strings.Lines(string(data)) {
			var record struct {
				Event  string `json:"event"`
				Output struct {
					HookSpecificOutput struct {
						AdditionalContext string `json:"additionalContext"`
					} `json:"hookSpecificOutput"`
				} `json:"output"`
			}
			if json.Unmarshal([]byte(line), &record) == nil && record.Event == "UserPromptSubmit" {
				told = append(told, record.Output.HookSpecificOutput.AdditionalContext)
			}
		}
		return strings.Join(told, "\n")
	}
	return checkout, worktree, session, c, host, peers, prompted
}

// forwardPeer hands a send the daemon's peer manager took to its target's
// messaging socket in the frame the manager's worker writes.
func forwardPeer(t *testing.T, send claudetest.PeerSend) {
	t.Helper()
	conn, err := net.Dial("unix", send.Target)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	id, from := uuid.NewString(), "/tmp/cc-socks/sender.sock"
	content := "<cross-session-message from=\"uds:" + from + "\" from-session=\"" + uuid.NewString() + "\" from-name=\"sender\">\n" + send.Message + "\n</cross-session-message>"
	frame, _ := json.Marshal(map[string]any{"msgV": 1, "msg_id": id, "uuid": id, "type": "user", "message": map[string]string{"role": "user", "content": content}, "priority": "next", "from": "uds:" + from})
	if _, err := conn.Write(append(frame, '\n')); err != nil {
		t.Fatal(err)
	}
}

func TestAMessageToAWorkerWhoseWorktreeWasRemovedResumesItInItsCheckoutAndSaysSo(t *testing.T) {
	for _, route := range []struct {
		name string
		send func(t *testing.T, c *Claude, session *surface.Session, peers *claudetest.PeerManager) (*surface.SendResult, error)
	}{
		{"a person's turn", func(t *testing.T, c *Claude, session *surface.Session, _ *claudetest.PeerManager) (*surface.SendResult, error) {
			return c.SendHumanTurn(context.Background(), session, "are you there?", nil)
		}},
		{"another session's message on the peer socket", func(t *testing.T, c *Claude, session *surface.Session, peers *claudetest.PeerManager) (*surface.SendResult, error) {
			result, err := c.Send(context.Background(), session, "are you there?")
			if sends := peers.Sends(); err == nil && len(sends) == 1 {
				forwardPeer(t, sends[0])
			}
			return result, err
		}},
	} {
		t.Run(route.name, func(t *testing.T) {
			checkout, worktree, session, c, host, peers, prompted := removedWorktreeSession(t, func(worktree string) []string { return []string{worktree} })
			result, err := route.send(t, c, &session, peers)
			if err != nil || !result.Accepted {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if want := "Its folder " + worktree + " is gone, so it resumed in " + checkout + "."; result.Notice != want {
				t.Fatalf("notice %q, want %q", result.Notice, want)
			}
			if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 1 || !samePath(t, session.Cwd, checkout) {
				t.Fatalf("spawns=%+v session cwd %s, want one resume in %s", spawns, session.Cwd, checkout)
			}
			testkit.Eventually(t, "the turn", 15*time.Second, func() bool {
				turns := host.Turns(t, fixtureConversation)
				return len(turns) == 1 && strings.Contains(turns[0].Text, "are you there?")
			})
			line := (surface.FolderMove{Gone: worktree, Into: checkout, Checkout: true}).Line()
			if told := prompted(); !strings.Contains(told, line) {
				t.Fatalf("the first prompt told Claude %q, want %q", told, line)
			}
			if _, err := os.Stat(worktree); !os.IsNotExist(err) {
				t.Fatalf("the removed worktree was recreated: %v", err)
			}
		})
	}
}

func TestAMessageToASessionWhoseFolderIsGoneAndWhoseRootsAreElsewhereIsRefusedWithoutAStart(t *testing.T) {
	_, worktree, session, c, host, _, _ := removedWorktreeSession(t, func(worktree string) []string {
		return []string{filepath.Join(filepath.Dir(worktree), "..", "..", "elsewhere")}
	})
	_, err := c.SendHumanTurn(context.Background(), &session, "are you there?", nil)
	if !surface.IsDeliveryTerminal(err) || !strings.Contains(err.Error(), "its folder "+worktree+" is gone") || !strings.Contains(err.Error(), "or archive the session") {
		t.Fatalf("err=%v, want a terminal refusal that says what to do", err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
		t.Fatalf("spawns=%+v, want none", spawns)
	}
}

func TestAMessageToASessionWhoseFolderIsGoneResumesItInTheFolderAboveWithItsTranscript(t *testing.T) {
	fixture, host, c := hostResume(t)
	parent := t.TempDir()
	gone := filepath.Join(parent, "removed")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	session := exitedSession(fixture)
	session.Cwd = gone
	session.Transcript = fixture.Transcript(fixtureConversation, gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	result, err := c.SendHumanTurn(surface.WithMessageUUID(context.Background(), uuid.NewString()), &session, "are you there", nil)
	if err != nil || !result.Accepted || result.Notice != "Its folder "+gone+" is gone, so it resumed in "+parent+"." {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 1 || !samePath(t, spawns[0].Cwd, parent) {
		t.Fatalf("spawns=%+v, want one resume in %s", spawns, parent)
	}
	testkit.Eventually(t, "the turn", 15*time.Second, func() bool {
		turns := host.Turns(t, fixtureConversation)
		return len(turns) == 1 && turns[0].Text == "are you there"
	})
}
