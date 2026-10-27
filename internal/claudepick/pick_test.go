package claudepick

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var now = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func reading(percent float64, resetsIn time.Duration) *Window {
	return &Window{Percent: percent, ResetsAt: now.Add(resetsIn)}
}

func roots() []Candidate {
	return []Candidate{
		{Name: "default", Root: "/home/dev/.claude", Account: "work@example.com", Login: surface.ClaudeLoginClaudeAI, Primary: true, Session: reading(70, 2*time.Hour), Weekly: reading(40, 72*time.Hour)},
		{Name: "team", Root: "/home/dev/.team", Account: "team@example.com", Login: surface.ClaudeLoginClaudeAI, Session: reading(20, time.Hour), Weekly: reading(27, 48*time.Hour)},
		{Name: "keys", Root: "/home/dev/.keys", Login: surface.ClaudeLoginAPIKey},
		{Name: "spare", Root: "/home/dev/.spare", Login: surface.ClaudeLoginSignedOut},
	}
}

func TestOneRootChangesNothing(t *testing.T) {
	got := Choose(roots()[:1], DefaultPolicy(), now)
	if got.Root != "" || got.Name != "default" || got.Reason != "the only account" {
		t.Fatalf("a single root starts in the primary exactly as before: %+v", got)
	}
}

func TestAutoOffKeepsThePrimary(t *testing.T) {
	policy := DefaultPolicy()
	policy.Auto = false
	if got := Choose(roots(), policy, now); got.Root != "" || got.Reason != "automatic choice is off" {
		t.Fatalf("%+v", got)
	}
}

func TestMostHeadroomWins(t *testing.T) {
	got := Choose(roots(), DefaultPolicy(), now)
	if got.Root != "/home/dev/.team" || got.Account != "team@example.com" || got.Reason != "most headroom, session 20%, week 27%" {
		t.Fatalf("%+v", got)
	}
}

func TestPreferredWinsWhileUnderItsCeilings(t *testing.T) {
	policy := DefaultPolicy()
	policy.Preferred = "default"
	if got := Choose(roots(), policy, now); got.Root != "" || got.Reason != "preferred, session 70%, week 40%" {
		t.Fatalf("%+v", got)
	}
	policy.SessionCeiling = 60
	if got := Choose(roots(), policy, now); got.Name != "team" {
		t.Fatalf("a preferred root over its ceiling gives way: %+v", got)
	}
}

func TestAnAPIKeyRootIsChosenOnlyWhenPreferred(t *testing.T) {
	candidates := roots()
	candidates[0].Session, candidates[0].Weekly = nil, nil
	candidates[1].Session, candidates[1].Weekly = nil, nil
	if got := Choose(candidates, DefaultPolicy(), now); got.Name != "default" || got.Reason != "no usage reading" {
		t.Fatalf("with no readings the primary is used, never the API key: %+v", got)
	}
	policy := DefaultPolicy()
	policy.Preferred = "keys"
	if got := Choose(roots(), policy, now); got.Root != "/home/dev/.keys" || got.Reason != "preferred; uses an API key" {
		t.Fatalf("%+v", got)
	}
}

func TestASignedOutRootIsNeverChosen(t *testing.T) {
	policy := DefaultPolicy()
	policy.Preferred = "spare"
	if got := Choose(roots(), policy, now); got.Name != "team" {
		t.Fatalf("%+v", got)
	}
}

func TestWhenEveryRootIsFullTheFirstToFreeUpWins(t *testing.T) {
	candidates := roots()
	candidates[0].Session = reading(95, 3*time.Hour)
	candidates[1].Weekly = reading(97, 26*time.Hour)
	candidates[1].Session = reading(99, time.Hour)
	got := Choose(candidates, DefaultPolicy(), now)
	if got.Name != "default" || got.Reason != "every account is near its limit; this one frees up first, in 3h 0m" {
		t.Fatalf("a root frees up when every window over its ceiling resets: %+v", got)
	}
}

func TestARootWithNoReadingRanksAfterKnownOnes(t *testing.T) {
	candidates := roots()
	candidates[1].Session, candidates[1].Weekly = nil, nil
	candidates[0].Session = reading(95, time.Hour)
	if got := Choose(candidates, DefaultPolicy(), now); got.Name != "default" {
		t.Fatalf("a known root, even a full one, ranks before an unread one: %+v", got)
	}
}
