package claudepick

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAStoppedSessionIsOfferedTheSignedInRootWithMostHeadroom(t *testing.T) {
	candidates := append(roots(), Candidate{Name: "ops", Root: "/home/dev/.ops", Account: "ops@example.com", Login: surface.ClaudeLoginClaudeAI, Session: reading(50, time.Hour)})
	got, ok := MoveTarget(candidates, "/home/dev/.claude", DefaultPolicy())
	if !ok || got.Name != "team" || got.Root != "/home/dev/.team" || got.Reason != "most headroom, session 20%, week 27%" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	if got, ok := MoveTarget(candidates, "/home/dev/.team", DefaultPolicy()); !ok || got.Name != "ops" {
		t.Fatalf("the session's own root is never offered: %+v ok=%v", got, ok)
	}
}

func TestNoMoveIsOfferedWithoutARootUnderItsCeilings(t *testing.T) {
	candidates := roots()
	candidates[1].Session = reading(95, time.Hour)
	if got, ok := MoveTarget(candidates, "/home/dev/.claude", DefaultPolicy()); ok {
		t.Fatalf("a root over its ceiling was offered: %+v", got)
	}
	unread := []Candidate{roots()[0], {Name: "fresh", Root: "/home/dev/.fresh", Login: surface.ClaudeLoginClaudeAI}}
	if got, ok := MoveTarget(unread, "/home/dev/.claude", DefaultPolicy()); ok {
		t.Fatalf("a root without a reading was offered: %+v", got)
	}
	proxied := []Candidate{roots()[0], {Name: "pool", Root: "/home/dev/.pool", Login: surface.ClaudeLoginProxy, Session: reading(10, time.Hour)}}
	if got, ok := MoveTarget(proxied, "/home/dev/.claude", DefaultPolicy()); ok {
		t.Fatalf("a proxy root was offered: %+v", got)
	}
	if got, ok := MoveTarget(roots()[:1], "/home/dev/.claude", DefaultPolicy()); ok {
		t.Fatalf("a single root offered a move: %+v", got)
	}
}
