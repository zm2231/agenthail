package claudepick

import (
	"errors"
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

func placed(t *testing.T, roots []surface.ClaudeConfigRoot, defaultRoot string, origin Origin) Pick {
	t.Helper()
	got, err := Place(roots, origin, func() (string, error) { return defaultRoot, nil })
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAnAgentsNewSessionStartsInTheAgentsRoot(t *testing.T) {
	got := placed(t, configured(), "proxy", Origin{CreatorTranscript: transcriptIn("/home/dev/.team")})
	if got.Root != "/home/dev/.team" || got.Name != "team" || got.Account != "team@example.com" || got.Reason != "the root of the session that created it" {
		t.Fatalf("the creator's root wins over the person's default: %+v", got)
	}
	got = placed(t, configured(), "proxy", Origin{CreatorTranscript: transcriptIn("/home/dev/.claude")})
	if got.Root != "" || got.Name != "default" {
		t.Fatalf("a creator in the primary root keeps its session in the primary: %+v", got)
	}
}

func TestAReplacementLeadStartsInTheReplacedLeadsRoot(t *testing.T) {
	got := placed(t, configured(), "proxy", Origin{PreviousTranscript: transcriptIn("/home/dev/.team")})
	if got.Root != "/home/dev/.team" || got.Reason != "the root of the lead it replaces" {
		t.Fatalf("%+v", got)
	}
	got = placed(t, configured(), "", Origin{CreatorTranscript: transcriptIn("/home/dev/.claude"), PreviousTranscript: transcriptIn("/home/dev/.team")})
	if got.Name != "default" {
		t.Fatalf("an agent creating the session outranks the replaced lead: %+v", got)
	}
}

func TestAPersonsNewSessionStartsInTheDefaultRootElseThePrimary(t *testing.T) {
	if got := placed(t, configured(), "proxy", Origin{}); got.Root != "/home/dev/.proxy" || got.Reason != "your default root for new sessions" {
		t.Fatalf("%+v", got)
	}
	if got := placed(t, configured(), "", Origin{}); got.Root != "" || got.Name != "default" || got.Reason != "the primary root" {
		t.Fatalf("%+v", got)
	}
	if got := placed(t, configured(), "gone", Origin{}); got.Root != "" || got.Reason != "the primary root; the default root gone is not configured" {
		t.Fatalf("%+v", got)
	}
}

func TestAnOriginOutsideEveryConfiguredRootFallsThrough(t *testing.T) {
	got := placed(t, configured(), "team", Origin{CreatorTranscript: "/elsewhere/projects/-repo/session.jsonl", PreviousTranscript: ""})
	if got.Name != "team" || got.Reason != "your default root for new sessions" {
		t.Fatalf("a Codex creator or an unconfigured root does not choose: %+v", got)
	}
	if got := placed(t, nil, "", Origin{}); got != (Pick{Reason: "the primary root"}) {
		t.Fatalf("no configured root is the primary: %+v", got)
	}
}

func TestTheDefaultIsReadOnlyWhenNoOriginDecides(t *testing.T) {
	broken := func() (string, error) { return "", errors.New("unreadable") }
	got, err := Place(configured(), Origin{CreatorTranscript: transcriptIn("/home/dev/.team")}, broken)
	if err != nil || got.Name != "team" {
		t.Fatalf("a creator's root needs no default: %+v err=%v", got, err)
	}
	got, err = Place(configured(), Origin{PreviousTranscript: transcriptIn("/home/dev/.team")}, broken)
	if err != nil || got.Name != "team" {
		t.Fatalf("a replaced lead's root needs no default: %+v err=%v", got, err)
	}
	if got, err := Place(configured(), Origin{}, broken); err == nil {
		t.Fatalf("with nothing else to decide, an unreadable default refuses rather than picking the primary: %+v", got)
	}
}
