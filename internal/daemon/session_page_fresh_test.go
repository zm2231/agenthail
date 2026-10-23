package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestAPageOfASessionNothingFollowsShowsWhatItsTranscriptGainedSince(t *testing.T) {
	previousGrace := sessionPageHandoffGrace
	sessionPageHandoffGrace = 10 * time.Millisecond
	t.Cleanup(func() { sessionPageHandoffGrace = previousGrace })
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	d, r, _, _, _ := daemonFixture(t)
	t.Cleanup(d.sources.shutdown)
	fixture := claudetest.New(t)
	const conversation = "page-fresh"
	path := filepath.Join(fixture.Home, ".claude", "projects", "fixture-project", conversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(claudeTurns(conversation, 1, 2)), 0600); err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: conversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: path, Cwd: fixture.Home}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d.Surfaces = []surface.Surface{surfaces.NewClaude(signin.Fixed{Browser: signin.Chrome}, fixture.Home)}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	if got := readDashboardExchanges(t, handler, conversation, ""); len(got) != 2 {
		t.Fatalf("first page exchanges=%+v", got)
	}
	waitForNoSources(t, d)
	appendToFile(t, path, claudeTurns(conversation, 3, 5))
	got := readDashboardExchanges(t, handler, conversation, "")
	if len(got) != 5 || got[4].Assistant != "answer 5" {
		t.Fatalf("page after the transcript grew with nothing following it: %+v, want answers 1-5", got)
	}
}

func waitForNoSources(t *testing.T, d *Daemon) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		d.sources.mu.Lock()
		running := len(d.sources.sources)
		d.sources.mu.Unlock()
		if running == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the page's source did not stop after its handoff grace")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
