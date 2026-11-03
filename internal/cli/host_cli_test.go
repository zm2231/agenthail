package cli

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

// hostCLI runs the CLI against the real Claude adapter and a running Claude
// host, which starts and resumes conversations.
type hostCLI struct {
	app    *App
	r      *registry.Registry
	claude *claudetest.Fixture
	host   *claudehosttest.Fixture
}

func newHostCLI(t *testing.T) *hostCLI {
	t.Helper()
	c := &hostCLI{claude: claudetest.New(t)}
	c.host = claudehosttest.New(t, c.claude.Home)
	c.host.Start(t)
	posts := &claudePosts{}
	c.app, c.r = cliFixture(t, &cliSurface{kind: surface.KindClaude})
	lazyRegistry(c.app)
	c.app.DefaultTimeout = claudeFixtureTimeout
	c.app.Surfaces[0].Surface = surfaces.NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, c.claude.Home, posts.request)
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(key, "")
	}
	return c
}

func (c *hostCLI) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return c.app.Run(args) })
}

func (c *hostCLI) firstTurn(t *testing.T, conversation string) string {
	t.Helper()
	testkit.Eventually(t, "the first turn", 15*time.Second, func() bool { return len(c.host.Turns(t, conversation)) > 0 })
	return c.host.Turns(t, conversation)[0].Text
}
