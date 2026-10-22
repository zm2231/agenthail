package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

type candidateSignin struct{ sources []signin.Source }

func (c candidateSignin) ResolveSignin(signin.Target) (signin.Source, error) {
	return c.sources[0], nil
}

func (c candidateSignin) SigninCandidates(signin.Consumer) ([]signin.Source, error) {
	return c.sources, nil
}

type claudeRootFixture struct {
	home    string
	session *surface.Session
	mu      sync.Mutex
	calls   []string
}

func newClaudeRootFixture(t *testing.T, accountUUID, email string) *claudeRootFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	config, _ := json.Marshal(map[string]any{"oauthAccount": map[string]any{"accountUuid": accountUUID, "emailAddress": email, "organizationUuid": "org-" + accountUUID}})
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(home, ".claude", "projects", "-work", "conversation.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(`{"type":"user","sessionId":"conversation","cwd":"/work"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return &claudeRootFixture{home: home, session: &surface.Session{ID: "conversation", Surface: surface.KindClaude, Bridge: "session_fixture", Transcript: transcript}}
}

func (f *claudeRootFixture) request(_ context.Context, method, url string, _ map[string]string, _ string, cookies CookieSource, _ time.Duration) (int, string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, method+" "+strings.TrimPrefix(url, "https://claude.ai")+" "+cookies.Signin.Dir)
	f.mu.Unlock()
	switch {
	case strings.HasSuffix(url, "/api/account"):
		account := strings.TrimPrefix(cookies.Signin.Dir, "/")
		return 200, `{"uuid":"` + account + `","email_address":"` + account + `@example.com"}`, nil
	case strings.HasSuffix(url, "/usage"):
		return 200, `{"limits":[{"kind":"session","group":"session","percent":100,"severity":"critical","resets_at":"2099-01-01T00:00:00Z"},{"kind":"weekly_scoped","percent":35,"scope":{"model":{"display_name":"Fable"}}}]}`, nil
	}
	return 200, "{}", nil
}

func (f *claudeRootFixture) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

var twoProfiles = candidateSignin{sources: []signin.Source{
	{ProfileID: "sp_a", Label: "a", Browser: signin.Chrome, Dir: "/a"},
	{ProfileID: "sp_b", Label: "b", Browser: signin.Chrome, Dir: "/b"},
}}

func TestClaudeRemoteControlUsesTheProfileSignedIntoTheSessionsAccount(t *testing.T) {
	fixture := newClaudeRootFixture(t, "b", "b@example.com")
	claude := NewClaudeWithRequest(twoProfiles, fixture.home, fixture.request)
	for range 2 {
		if _, err := claude.postMessage(context.Background(), fixture.session, "hello"); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"GET /api/account /a", "GET /api/account /b", "POST /v1/code/sessions/cse_fixture/events /b", "POST /v1/code/sessions/cse_fixture/events /b"}
	if calls := fixture.callList(); strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the chosen profile is signed into another account, so the post must carry b's cookies and ask each profile once: %q", calls)
	}
}

func TestClaudeRemoteControlWithNoProfileForTheAccountFailsBeforePosting(t *testing.T) {
	fixture := newClaudeRootFixture(t, "c", "c@example.com")
	claude := NewClaudeWithRequest(twoProfiles, fixture.home, fixture.request)
	_, err := claude.postMessage(context.Background(), fixture.session, "hello")
	if surface.DeliveryTerminalReason(err) != surface.DeliverySigninUnreadable || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("err=%v", err)
	}
	for _, want := range []string{"no sign-in profile is signed into claude.ai as c@example.com", "a is a@example.com", "b is b@example.com", "agenthail signin add"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err=%v, missing %q", err, want)
		}
	}
	for _, call := range fixture.callList() {
		if strings.HasPrefix(call, "POST") {
			t.Fatalf("posted with another account's cookies: %q", fixture.callList())
		}
	}
}

func TestClaudeConfigRootsReportTheSigninAndLiveUsage(t *testing.T) {
	fixture := newClaudeRootFixture(t, "b", "b@example.com")
	claude := NewClaudeWithRequest(twoProfiles, fixture.home, fixture.request)
	roots, err := claude.ClaudeConfigRoots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	account := roots[0]
	if account.Signin != "b" || account.SigninProblem != "" || account.UsageProblem != "" || account.Usage == nil || len(account.Usage.Limits) != 2 {
		t.Fatalf("account=%+v", account)
	}
	if limit := account.Usage.Limits[1]; limit.Kind != "weekly_scoped" || limit.Percent != 35 || limit.Scope != "Fable" {
		t.Fatalf("limit=%+v", limit)
	}
	if _, out := account.Usage.Exhausted(time.Now()); !out {
		t.Fatal("a session limit at 100% must read as exhausted")
	}
	if calls := fixture.callList(); calls[len(calls)-1] != "GET /api/organizations/org-b/usage /b" {
		t.Fatalf("usage must be read for the root's organization with its account's profile: %q", calls)
	}
}

func TestClaudeRemoteControlForALiveSessionWithoutATranscriptUsesItsRootsAccount(t *testing.T) {
	fixture := newClaudeRootFixture(t, "b", "b@example.com")
	record, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": "fresh", "bridgeSessionId": "session_fresh"})
	sessions := filepath.Join(fixture.home, ".claude", "sessions")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, "1.json"), record, 0600); err != nil {
		t.Fatal(err)
	}
	claude := NewClaudeWithRequest(twoProfiles, fixture.home, fixture.request)
	if _, err := claude.postMessage(context.Background(), &surface.Session{ID: "fresh", Surface: surface.KindClaude, Bridge: "session_fresh"}, "hello"); err != nil {
		t.Fatal(err)
	}
	if calls := fixture.callList(); calls[len(calls)-1] != "POST /v1/code/sessions/cse_fresh/events /b" {
		t.Fatalf("a session whose transcript is not written yet still belongs to its record's root: %q", calls)
	}
}

func TestClaudeRemoteControlWithAKnownTranscriptDoesNotRescanSessionRecords(t *testing.T) {
	fixture := newClaudeRootFixture(t, "b", "b@example.com")
	sessions := filepath.Join(fixture.home, ".claude", "sessions")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	claude := NewClaudeWithRequest(twoProfiles, fixture.home, fixture.request)
	if err := os.Chmod(sessions, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sessions, 0700) })
	for range 3 {
		if _, err := claude.remoteRequest(context.Background(), fixture.session, "GET", "/v1/code/sessions/cse_fixture", ""); err != nil {
			t.Fatalf("a request about a session with a known transcript read the session records: %v", err)
		}
	}
}

func TestClaudeRemoteControlThatCannotReadSessionRecordsIsHeldNotRefused(t *testing.T) {
	fixture := newClaudeRootFixture(t, "b", "b@example.com")
	sessions := filepath.Join(fixture.home, ".claude", "sessions")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sessions, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sessions, 0700) })
	claude := NewClaudeWithRequest(twoProfiles, fixture.home, fixture.request)
	_, err := claude.postMessage(context.Background(), &surface.Session{ID: "fresh", Surface: surface.KindClaude, Bridge: "session_fresh"}, "hello")
	if !surface.IsDeliveryUnavailable(err) || surface.IsDeliveryTerminal(err) {
		t.Fatalf("an unreadable session directory is not a sign-in problem: %v", err)
	}
}
