package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

// closedInHost makes the fixture's conversation an idle one the Claude host
// ran and has closed.
func closedInHost(t *testing.T, e exitedClaude) {
	t.Helper()
	closed := e.session
	closed.RuntimeKind, closed.Bridge, closed.PID, closed.Status = surface.RuntimeClaudeHost, "", 0, surface.StatusIdle
	if err := e.r.RegisterSession(closed); err != nil {
		t.Fatal(err)
	}
}

func closedConversationCommands() map[string][]string {
	return map[string][]string{
		"send":   {"send", terminalConversation, "hello"},
		"resume": {"thread", "resume", terminalConversation},
	}
}

func TestACommandThatMustOpenAClosedClaudeConversationWithoutADaemonSaysToStartIt(t *testing.T) {
	for name, args := range closedConversationCommands() {
		t.Run(name, func(t *testing.T) {
			e := exitedClaudeWithoutDaemon(t)
			closedInHost(t, e)
			output, err := captureStdout(t, func() error { return e.app.Run(args) })
			if err == nil || !strings.Contains(err.Error(), "agenthail daemon start") {
				t.Fatalf("output=%q err=%v, want the daemon-start hint", output, err)
			}
			if spawns := e.host.Spawns(t, terminalConversation); len(spawns) != 0 || e.peers.Count("hello") != 0 || e.r.QueueCount(terminalConversation) != 0 {
				t.Fatalf("spawns=%+v peer=%d queued=%d, want nothing opened, delivered or queued", spawns, e.peers.Count("hello"), e.r.QueueCount(terminalConversation))
			}
		})
	}
}

func TestACommandThatOpensAClosedClaudeConversationOpensItThroughTheDaemonWithItsAgenthailTools(t *testing.T) {
	for name, args := range closedConversationCommands() {
		t.Run(name, func(t *testing.T) {
			e := exitedClaudeApp(t)
			closedInHost(t, e)
			if output, err := captureStdout(t, func() error { return e.app.Run(args) }); err != nil {
				t.Fatalf("output=%q err=%v", output, err)
			}
			var bearer string
			testkit.Eventually(t, "the agenthail MCP server attached at the open", 15*time.Second, func() bool {
				for _, attach := range e.host.Attached(t, terminalConversation) {
					if attach.Bearer != "" {
						bearer = attach.Bearer
						return true
					}
				}
				return false
			})
			hash, live, err := e.r.LiveCredentialHash(terminalConversation, time.Now())
			if err != nil || !live || registry.HashSessionToken(bearer) != hash {
				t.Fatalf("live=%v err=%v, want the token the open carried to be the conversation's live credential", live, err)
			}
			if spawns := e.host.Spawns(t, terminalConversation); len(spawns) != 1 {
				t.Fatalf("spawns=%+v, want the conversation opened once", spawns)
			}
		})
	}
}

func TestAReplySendToAClosedClaudeConversationFollowsTheDaemonsDelivery(t *testing.T) {
	e := exitedClaudeApp(t)
	output, err := captureStdout(t, func() error { return e.app.Run([]string{"send", terminalConversation, "hello", "--reply"}) })
	if err != nil || e.peers.Count("hello") != 1 || len(e.host.Spawns(t, terminalConversation)) != 1 {
		t.Fatalf("output=%q err=%v peer=%d, want one delivery through one open", output, err, e.peers.Count("hello"))
	}
}

func TestAdoptingAnExitedTerminalConversationOpensItOnlyThroughTheDaemon(t *testing.T) {
	alone := exitedClaudeWithoutDaemon(t)
	if _, err := captureStdout(t, func() error { return alone.app.Run([]string{"thread", "adopt", terminalConversation}) }); err == nil || !strings.Contains(err.Error(), "agenthail daemon start") || len(alone.host.Spawns(t, terminalConversation)) != 0 {
		t.Fatalf("err=%v, want the daemon-start hint and nothing opened", err)
	}
	e := exitedClaudeApp(t)
	if output, err := captureStdout(t, func() error { return e.app.Run([]string{"thread", "adopt", terminalConversation}) }); err != nil {
		t.Fatalf("output=%q err=%v", output, err)
	}
	testkit.Eventually(t, "the agenthail MCP server attached at the open", 15*time.Second, func() bool {
		for _, attach := range e.host.Attached(t, terminalConversation) {
			if attach.Bearer != "" {
				return true
			}
		}
		return false
	})
}
