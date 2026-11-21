package surfaces

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestListingTheQueueNeverListsClaudeProjects(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture := claudetest.New(t)
	c := NewClaude(nil, fixture.Home)
	projects := filepath.Join(fixture.Root, "projects")
	if err := os.MkdirAll(filepath.Join(projects, "project"), 0700); err != nil {
		t.Fatal(err)
	}
	var listings int
	c.transcripts.readDir = func(dir string) ([]os.DirEntry, error) {
		if dir == projects {
			listings++
		}
		return os.ReadDir(dir)
	}
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	const queued = 4
	for i := range queued {
		id := "0000000" + string(rune('0'+i)) + "-0caa-4912-823f-927677775b2b"
		if err := r.RecordObservedSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", Bridge: "bridge"}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.QueueMessageWithKey(id, "hello", "k"+id); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := r.ListQueue(false, surface.Adapters{c})
	if err != nil || len(rows) != queued {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if listings != 0 {
		t.Fatalf("listing %d queued rows listed the Claude projects %d times", queued, listings)
	}
}

func TestSteersMatchesTheSessionCapabilities(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture := claudetest.New(t)
	sessions := []*surface.Session{
		nil,
		{ID: "hosted", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transport: "uds"},
		{ID: "native", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds"},
		{ID: "bridged", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", Bridge: "bridge"},
		{ID: "background", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeBackground},
	}
	for _, localOnly := range []bool{false, true} {
		c := NewClaude(nil, fixture.Home)
		if localOnly {
			c.DisableRemoteControl()
		}
		for _, session := range sessions {
			if got, want := surface.CanSteer(c, session), surface.EffectiveCapabilities(c, session).Steer; got != want {
				t.Errorf("localOnly=%v session=%+v: CanSteer=%v, capabilities say %v", localOnly, session, got, want)
			}
		}
	}
}
