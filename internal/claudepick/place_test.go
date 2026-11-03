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

func configured() []surface.ClaudeConfigRoot {
	return []surface.ClaudeConfigRoot{
		{Name: "default", Root: "/home/dev/.claude", Primary: true, SignedInAs: &surface.ClaudeLogin{Email: "work@example.com"}},
		{Name: "team", Root: "/home/dev/.team", SignedInAs: &surface.ClaudeLogin{Email: "team@example.com"}},
		{Name: "proxy", Root: "/home/dev/.proxy", Proxy: "forge"},
	}
}

func transcriptIn(root string) string { return root + "/projects/-repo/session.jsonl" }

func TestAnAgentsNewSessionStartsInTheAgentsRoot(t *testing.T) {
	got := Place(configured(), "proxy", Origin{CreatorTranscript: transcriptIn("/home/dev/.team")})
	if got.Root != "/home/dev/.team" || got.Name != "team" || got.Account != "team@example.com" || got.Reason != "the root of the session that created it" {
		t.Fatalf("the creator's root wins over the person's default: %+v", got)
	}
	got = Place(configured(), "proxy", Origin{CreatorTranscript: transcriptIn("/home/dev/.claude")})
	if got.Root != "" || got.Name != "default" {
		t.Fatalf("a creator in the primary root keeps its session in the primary: %+v", got)
	}
}

func TestAReplacementLeadStartsInTheReplacedLeadsRoot(t *testing.T) {
	got := Place(configured(), "proxy", Origin{PreviousTranscript: transcriptIn("/home/dev/.team")})
	if got.Root != "/home/dev/.team" || got.Reason != "the root of the lead it replaces" {
		t.Fatalf("%+v", got)
	}
	got = Place(configured(), "", Origin{CreatorTranscript: transcriptIn("/home/dev/.claude"), PreviousTranscript: transcriptIn("/home/dev/.team")})
	if got.Name != "default" {
		t.Fatalf("an agent creating the session outranks the replaced lead: %+v", got)
	}
}

func TestAPersonsNewSessionStartsInTheDefaultRootElseThePrimary(t *testing.T) {
	if got := Place(configured(), "proxy", Origin{}); got.Root != "/home/dev/.proxy" || got.Reason != "your default root for new sessions" {
		t.Fatalf("%+v", got)
	}
	if got := Place(configured(), "", Origin{}); got.Root != "" || got.Name != "default" || got.Reason != "the primary root" {
		t.Fatalf("%+v", got)
	}
	if got := Place(configured(), "gone", Origin{}); got.Root != "" || got.Reason != "the primary root; the default root gone is not configured" {
		t.Fatalf("%+v", got)
	}
}

func TestAnOriginOutsideEveryConfiguredRootFallsThrough(t *testing.T) {
	got := Place(configured(), "team", Origin{CreatorTranscript: "/elsewhere/projects/-repo/session.jsonl", PreviousTranscript: ""})
	if got.Name != "team" || got.Reason != "your default root for new sessions" {
		t.Fatalf("a Codex creator or an unconfigured root does not choose: %+v", got)
	}
	if got := Place(nil, "", Origin{}); got != (Pick{Reason: "the primary root"}) {
		t.Fatalf("no configured root is the primary: %+v", got)
	}
}
