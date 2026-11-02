package registry

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestForkAncestorsWalkTheChainWithTheLatestRegistration(t *testing.T) {
	r := openTestRegistry(t)
	for _, s := range []surface.Session{
		{ID: "a", Surface: surface.KindClaude},
		{ID: "b", Surface: surface.KindClaude, ForkedFrom: "a"},
		{ID: "c", Surface: surface.KindClaude, ForkedFrom: "b"},
		{ID: "x", Surface: surface.KindClaude, ForkedFrom: "y"},
		{ID: "y", Surface: surface.KindClaude, ForkedFrom: "x"},
	} {
		s.RuntimeKind = surface.RuntimeClaudeCLI
		if err := r.RegisterSession(s); err != nil {
			t.Fatal(err)
		}
	}
	set := func(id, at string) {
		if _, err := r.WriteDB().Exec(`UPDATE sessions SET registered_at=? WHERE id=?`, at, id); err != nil {
			t.Fatal(err)
		}
	}
	set("b", "2026-11-02 10:00:00")
	set("c", "2026-11-02 09:00:00")

	ancestors, err := r.ForkAncestors("c")
	if err != nil {
		t.Fatal(err)
	}
	nine := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	ten := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	if len(ancestors) != 2 || ancestors[0] != (ForkAncestor{SessionID: "b", Since: nine}) || ancestors[1] != (ForkAncestor{SessionID: "a", Since: ten}) {
		t.Fatalf("ancestors = %+v", ancestors)
	}
	if ancestors, err := r.ForkAncestors("a"); err != nil || len(ancestors) != 0 {
		t.Fatalf("an unforked session has ancestors: %+v %v", ancestors, err)
	}
	if ancestors, err := r.ForkAncestors("x"); err != nil || len(ancestors) != 1 || ancestors[0].SessionID != "y" {
		t.Fatalf("a cycle must stop the walk: %+v %v", ancestors, err)
	}
	if ancestors, err := r.ForkAncestors("missing"); err != nil || len(ancestors) != 0 {
		t.Fatalf("an unknown session has ancestors: %+v %v", ancestors, err)
	}
}
