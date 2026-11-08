package registry

import (
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAnEmptyTargetNamesNoSessionEvenWhenOnlyOneExists(t *testing.T) {
	r := openTestRegistry(t)
	if err := r.RegisterSession(surface.Session{ID: "only-session", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"", "  ", "@"} {
		if got, err := r.ResolveTarget(target); err == nil {
			t.Fatalf("ResolveTarget(%q) resolved to %q", target, got)
		}
		if got, err := r.ResolveSurfaceTarget(surface.KindClaude, target); err == nil {
			t.Fatalf("ResolveSurfaceTarget(claude, %q) resolved to %q", target, got)
		}
	}
}
