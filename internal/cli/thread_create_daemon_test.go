package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestATerminalThreadCreateStartsThroughTheDaemonWithTheAgenthailMCPServer(t *testing.T) {
	c := newHostCLI(t)
	t.Setenv("HOME", t.TempDir())
	for _, source := range surface.CallerIdentity {
		if os.Getenv(source.Variable) != "" {
			t.Fatalf("%s is set; this is an agent's shell, not a terminal", source.Variable)
		}
	}
	serveAppDaemon(t, c.app, c.r, []surface.Surface{surfaces.NewClaude(nil, c.claude.Home)})
	output, err := c.run(t, "thread", "create", "claude", "start the work", "--cwd", c.host.Cwd, "--json")
	var created threadCreateOutput
	if err != nil || json.Unmarshal([]byte(output), &created) != nil || !created.OK || created.Session == nil {
		t.Fatalf("output=%q err=%v", output, err)
	}
	id := created.Session.ID
	var bearer string
	testkit.Eventually(t, "the agenthail MCP server attached at the open", 15*time.Second, func() bool {
		for _, attach := range c.host.Attached(t, id) {
			if attach.Bearer != "" {
				bearer = attach.Bearer
				return true
			}
		}
		return false
	})
	hash, live, err := c.r.LiveCredentialHash(id, time.Now())
	if err != nil || !live || hash == "" {
		t.Fatalf("live credential for %s: hash=%q live=%v err=%v, want the one the open carried", id, hash, live, err)
	}
	if registry.HashSessionToken(bearer) != hash {
		t.Fatal("the token the open carried is not the session's live credential")
	}
}

func TestThreadCreateWithoutADaemonSaysHowToStartIt(t *testing.T) {
	starter := &starterCLISurface{cliSurface: &cliSurface{kind: surface.KindCodex}}
	app, _ := cliFixture(t, starter.cliSurface)
	app.Surfaces[0].Surface = starter
	err := app.Run([]string{"thread", "create", "codex", "Build this", "--cwd", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "agenthail daemon start") || len(starter.options) != 0 {
		t.Fatalf("err=%v starts=%d, want the daemon-start hint and nothing started", err, len(starter.options))
	}
}
