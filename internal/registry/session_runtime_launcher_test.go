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

func TestSessionRuntimeDoesNotReplaceCreatedSessionIdentity(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	want := surface.Session{ID: "provider-session", Surface: surface.KindClaude, Name: "Build", Cwd: "/repo", Status: surface.StatusUnknown, Runtime: &surface.Runtime{Launcher: surface.LauncherClaudeBG}}
	if err := r.RegisterSession(want); err != nil {
		t.Fatal(err)
	}
	got, err := r.Session(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Surface != want.Surface || got.Cwd != want.Cwd || got.Name != want.Name || got.Runtime == nil || got.Runtime.Launcher != surface.LauncherClaudeBG {
		t.Fatalf("session=%+v", got)
	}
}
