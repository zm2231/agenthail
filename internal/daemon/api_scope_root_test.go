package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAnAgentKeepsItsClaudeRootAndItsCoordinatorPlacesAny(t *testing.T) {
	d, r, fake, worker, lead := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	own, other := filepath.Join(home, "own-root"), filepath.Join(home, "other-root")
	for _, dir := range []string{own, other, filepath.Join(home, ".agenthail")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config, _ := json.Marshal(map[string]any{"claudeConfigDirs": []string{own, other}})
	if err := os.WriteFile(surface.AgenthailConfigPath(home), config, 0o600); err != nil {
		t.Fatal(err)
	}
	widget := widgetRepo(t, "https://github.com/example/widget.git")
	worker.Cwd, lead.Cwd = widget, widget
	worker.Surface, worker.RuntimeKind = surface.KindClaude, surface.RuntimeClaudeHost
	worker.Transcript = filepath.Join(own, "projects", "widget", "worker.jsonl")
	for _, session := range []surface.Session{worker, lead} {
		session.LastActive = time.Now()
		fake.sessions[session.ID] = session
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())
	project, err := broadcast.FindProject(r, "widget")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: project.ID, ProjectRoot: widget, Name: "widget-lead", Surface: string(surface.KindClaude), Cwd: widget}, registry.CoordinatorChange{SessionID: lead.ID, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	workerCaller, err := d.scopeCaller(worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	leadCaller, err := d.scopeCaller(lead.ID)
	if err != nil {
		t.Fatal(err)
	}

	if root, err := d.agentConfigRoot(workerCaller, own); err != nil || root != own {
		t.Fatalf("a worker on its own root: %q %v", root, err)
	}
	if root, err := d.agentConfigRoot(workerCaller, "other-root"); err == nil || mcpRefusalFor(err).Detail["rule"] != "config_root" || mcpRefusalFor(err).target != "new:claude" {
		t.Fatalf("a worker on another root: %q %v", root, err)
	}
	if root, err := d.agentConfigRoot(leadCaller, "other-root"); err != nil || root != other {
		t.Fatalf("the coordinator placing on another root: %q %v", root, err)
	}
	if _, err := d.agentConfigRoot(leadCaller, filepath.Join(home, "unlisted")); err == nil || mcpRefusalFor(err).Code != "invalid" {
		t.Fatalf("an unlisted root: %v", err)
	}
	if root, err := d.agentConfigRoot(workerCaller, ""); err != nil || root != "" {
		t.Fatalf("no root: %q %v", root, err)
	}
}
