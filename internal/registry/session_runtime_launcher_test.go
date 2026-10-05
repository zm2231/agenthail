package registry

import (
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestSessionRuntimePersistsAcrossRegistryReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: "session-1", Surface: surface.KindClaude, Status: surface.StatusIdle}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	want := &surface.Runtime{Launcher: surface.LauncherTMUX, Location: &surface.Location{Session: "agenthail", Pane: "%3"}, Focusable: true}
	if err := r.SaveSessionRuntime(session.ID, want); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, found, err := r.SessionRuntime(session.ID)
	if err != nil || !found {
		t.Fatalf("runtime=%+v found=%v err=%v", got, found, err)
	}
	if got.Launcher != want.Launcher || got.Location == nil || *got.Location != *want.Location || !got.Focusable {
		t.Fatalf("runtime=%+v", got)
	}
}
