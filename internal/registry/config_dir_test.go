package registry

import (
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudeConfigDirSurvivesQueueAndRegistryReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: "gentle", Surface: surface.KindClaude, Name: "gentle", ConfigDir: "/fixture/gentle/.claude", Status: surface.StatusIdle}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(session.ID, "queued question"); err != nil {
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
	stored, err := r.Session(session.ID)
	if err != nil || stored.ConfigDir != session.ConfigDir {
		t.Fatalf("stored=%v err=%v", stored, err)
	}
	list, err := r.ListSessions(10)
	if err != nil || len(list) != 1 || list[0].ConfigDir != session.ConfigDir {
		t.Fatalf("list=%v err=%v", list, err)
	}
	results, err := r.SearchSessions(surface.KindClaude, "gentle", 10)
	if err != nil || len(results) != 1 || results[0].ConfigDir != session.ConfigDir {
		t.Fatalf("search=%v err=%v", results, err)
	}
}
