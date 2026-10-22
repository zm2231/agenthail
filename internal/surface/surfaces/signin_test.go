package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

const fixtureNotionSpace = "11111111-2222-3333-4444-555555555555"

type cookieFixture struct {
	bridge     string
	bridgeLog  string
	sidecarLog string
}

// The fake node stands in for the cookie bridge; the fake sidecar answers like
// app.notion.com. Both log what they were asked.
func newCookieFixture(t *testing.T) cookieFixture {
	t.Helper()
	dir := t.TempDir()
	fixture := cookieFixture{bridge: filepath.Join(dir, "cookie.mjs"), bridgeLog: filepath.Join(dir, "bridge.log"), sidecarLog: filepath.Join(dir, "sidecar.log")}
	if err := os.WriteFile(fixture.bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	node := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(fixture.bridgeLog) + "\nprintf 'sid=fixture'\n"
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte(node), 0700); err != nil {
		t.Fatal(err)
	}
	sidecar := `import json, sys
req = json.loads(sys.stdin.read())
with open(` + pyQuote(fixture.sidecarLog) + `, "a") as log:
    log.write(json.dumps({"url": req["url"], "cookie": req["headers"].get("cookie", "")}) + "\n")
url = req["url"]
if url.endswith("/getSpaces"):
    body = {"user-1": {"notion_user": {"user-1": {"value": {"value": {"name": "Fixture User"}}}}, "space": {"` + fixtureNotionSpace + `": {}}}}
elif url.endswith("/getInferenceTranscriptsForUser"):
    body = {"transcripts": [{"id": "thread-1", "title": "Fixture thread", "updated_at": 1700000000000}]}
else:
    body = {}
sys.stdout.write(json.dumps({"status": 200, "body": json.dumps(body), "error": ""}))
`
	sidecarPath := filepath.Join(dir, "sidecar.py")
	if err := os.WriteFile(sidecarPath, []byte(sidecar), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AGENTHAIL_SIDECAR", sidecarPath)
	return fixture
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'" }

func pyQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

type switchableSignin struct{ source signin.Source }

func (s *switchableSignin) ResolveSignin(signin.Target) (signin.Source, error) { return s.source, nil }

func (s *switchableSignin) SigninCandidates(signin.Consumer) ([]signin.Source, error) {
	return []signin.Source{s.source}, nil
}

func TestNotionWithSigninOffReadsNoCookiesAndSaysHowToTurnItOn(t *testing.T) {
	fixture := newCookieFixture(t)
	notion := NewNotion(nil, "", "")
	notion.cookieBridge = fixture.bridge

	_, err := notion.List(context.Background())
	if !signin.IsOff(err) || !strings.Contains(err.Error(), "Notion: browser sign-in is turned off") || !strings.Contains(err.Error(), "agenthail signin use notion auto") || !strings.Contains(err.Error(), "Settings > Browser Sign-in") {
		t.Fatalf("list err=%v", err)
	}
	_, err = notion.Send(context.Background(), &surface.Session{ID: "thread-1"}, "hello")
	if surface.DeliveryTerminalReason(err) != surface.DeliverySigninOff || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("send err=%v", err)
	}
	if lines := readLines(t, fixture.bridgeLog); lines != nil {
		t.Fatalf("cookie bridge ran without a profile: %v", lines)
	}
	if lines := readLines(t, fixture.sidecarLog); lines != nil {
		t.Fatalf("request sent without a profile: %v", lines)
	}
}

func TestNotionDetectsContextThroughTheBoundProfile(t *testing.T) {
	fixture := newCookieFixture(t)
	profileDir := filepath.Join(t.TempDir(), "Profile 2")
	resolver := &switchableSignin{source: signin.Source{ProfileID: "sp_aaaaaaaaaaaa", Browser: signin.Chrome, Dir: profileDir}}
	notion := NewNotion(resolver, "", "")
	notion.cookieBridge = fixture.bridge

	sessions, err := notion.List(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "thread-1" {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	if account := notion.account; account == nil || account.spaceID != fixtureNotionSpace || account.userID != "user-1" || account.userName != "Fixture User" {
		t.Fatalf("account=%+v", account)
	}
	bridgeCalls := readLines(t, fixture.bridgeLog)
	want := fixture.bridge + " --browser chrome --profile-dir " + profileDir + " https://app.notion.com/"
	if len(bridgeCalls) != 1 || bridgeCalls[0] != want {
		t.Fatalf("bridge calls=%q want one %q", bridgeCalls, want)
	}
	requests := readLines(t, fixture.sidecarLog)
	if len(requests) != 2 || !strings.Contains(requests[0], "/getSpaces") || !strings.Contains(requests[0], `"cookie": "sid=fixture"`) {
		t.Fatalf("requests=%q", requests)
	}

	otherDir := filepath.Join(t.TempDir(), "Default")
	resolver.source = signin.Source{ProfileID: "sp_bbbbbbbbbbbb", Browser: signin.Edge, Dir: otherDir}
	if _, err := notion.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	bridgeCalls = readLines(t, fixture.bridgeLog)
	if len(bridgeCalls) != 2 || bridgeCalls[1] != fixture.bridge+" --browser edge --profile-dir "+otherDir+" https://app.notion.com/" {
		t.Fatalf("a new profile must read its own cookies: %q", bridgeCalls)
	}
	requests = readLines(t, fixture.sidecarLog)
	if len(requests) != 4 || !strings.Contains(requests[2], "/getSpaces") {
		t.Fatalf("a new profile must detect its own Notion account: %q", requests)
	}
}

type unreadableSignin struct{}

func (unreadableSignin) ResolveSignin(target signin.Target) (signin.Source, error) {
	return signin.Source{}, signin.ProblemError{Consumer: target.Consumer, Err: errors.New("permission denied reading Safari data at /fixture/Cookies.binarycookies")}
}

func (u unreadableSignin) SigninCandidates(consumer signin.Consumer) ([]signin.Source, error) {
	_, err := u.ResolveSignin(signin.Target{Consumer: consumer})
	return nil, err
}

func TestClaudeRemoteControlWithAnUnreadableProfileFailsBeforePosting(t *testing.T) {
	posted := false
	claude := NewClaudeWithRequest(unreadableSignin{}, t.TempDir(), func(context.Context, string, string, map[string]string, string, CookieSource, time.Duration) (int, string, error) {
		posted = true
		return 200, "{}", nil
	})
	_, err := claude.postMessage(context.Background(), &surface.Session{ID: "conversation", Bridge: "session_fixture"}, "hello")
	if surface.DeliveryTerminalReason(err) != surface.DeliverySigninUnreadable || !strings.Contains(err.Error(), "permission denied reading Safari data") || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("err=%v", err)
	}
	if posted {
		t.Fatal("posted to claude.ai without a readable sign-in profile")
	}
}

func TestClaudeRemoteControlWithSigninOffFailsBeforePosting(t *testing.T) {
	posted := false
	claude := NewClaudeWithRequest(nil, t.TempDir(), func(context.Context, string, string, map[string]string, string, CookieSource, time.Duration) (int, string, error) {
		posted = true
		return 200, "{}", nil
	})
	_, err := claude.postMessage(context.Background(), &surface.Session{ID: "conversation", Bridge: "session_fixture"}, "hello")
	if surface.DeliveryTerminalReason(err) != surface.DeliverySigninOff || !strings.Contains(err.Error(), "agenthail signin use claude auto") {
		t.Fatalf("err=%v", err)
	}
	if posted {
		t.Fatal("posted to claude.ai without a sign-in profile")
	}
}

func TestClaudeRemoteControlPassesTheResolvedProfile(t *testing.T) {
	var got CookieSource
	source := signin.Source{ProfileID: "sp_cccccccccccc", Browser: signin.Firefox, Dir: "/fixture/firefox/abc.default"}
	claude := NewClaudeWithRequest(signin.Fixed(source), t.TempDir(), func(_ context.Context, _, _ string, _ map[string]string, _ string, cookies CookieSource, _ time.Duration) (int, string, error) {
		got = cookies
		return 200, "{}", nil
	})
	if _, err := claude.postMessage(context.Background(), &surface.Session{ID: "conversation", Bridge: "session_fixture"}, "hello"); err != nil {
		t.Fatal(err)
	}
	if got.Signin != source || got.URL != "https://claude.ai/" {
		t.Fatalf("cookies=%+v", got)
	}
}

func TestCookieCacheIsKeyedByProfile(t *testing.T) {
	fixture := newCookieFixture(t)
	a := CookieSource{Bridge: fixture.bridge, URL: "https://claude.ai/", Signin: signin.Source{ProfileID: "sp_a", Browser: signin.Chrome, Dir: "/a"}}
	b := CookieSource{Bridge: fixture.bridge, URL: "https://claude.ai/", Signin: signin.Source{ProfileID: "sp_b", Browser: signin.Chrome, Dir: "/b"}}
	for _, source := range []CookieSource{a, a, b} {
		if header, err := loadCookieHeader(context.Background(), source); err != nil || header != "sid=fixture" {
			t.Fatalf("header=%q err=%v", header, err)
		}
	}
	if calls := readLines(t, fixture.bridgeLog); len(calls) != 2 {
		t.Fatalf("calls=%q, want one read per profile", calls)
	}
	invalidateCookieHeader(a)
	if _, err := loadCookieHeader(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if calls := readLines(t, fixture.bridgeLog); len(calls) != 2 {
		t.Fatalf("invalidating one profile reread another: %q", calls)
	}
	if _, err := loadCookieHeader(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if calls := readLines(t, fixture.bridgeLog); len(calls) != 3 {
		t.Fatalf("calls=%q", calls)
	}
}

type lockedSignin struct {
	mu     sync.Mutex
	source signin.Source
}

func (s *lockedSignin) ResolveSignin(signin.Target) (signin.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.source, nil
}

func (s *lockedSignin) SigninCandidates(signin.Consumer) ([]signin.Source, error) {
	source, err := s.ResolveSignin(signin.Target{})
	return []signin.Source{source}, err
}

func (s *lockedSignin) use(source signin.Source) {
	s.mu.Lock()
	s.source = source
	s.mu.Unlock()
}

type notionAccountRequest struct {
	URL      string `json:"url"`
	Cookie   string `json:"cookie"`
	User     string `json:"user"`
	Space    string `json:"space"`
	Timezone string `json:"timezone"`
	Context  struct {
		UserID   string `json:"userId"`
		SpaceID  string `json:"spaceId"`
		Timezone string `json:"timezone"`
	} `json:"context"`
}

// Two Notion accounts behind two profiles. Account A's detection blocks until
// released, so a binding switch to B happens while A's operation is in flight.
func TestNotionBindingSwitchKeepsEachOperationOnOneAccount(t *testing.T) {
	dir := t.TempDir()
	log, started, release := filepath.Join(dir, "requests.log"), filepath.Join(dir, "a-started"), filepath.Join(dir, "a-release")
	node := "#!/bin/sh\nwhile [ $# -gt 0 ]; do case \"$1\" in --profile-dir) shift; printf 'sid=%s' \"$(basename \"$1\")\";; esac; shift; done\n"
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte(node), 0700); err != nil {
		t.Fatal(err)
	}
	sidecar := `import json, os, sys, time
req = json.loads(sys.stdin.read())
headers, url = req["headers"], req["url"]
sid = headers.get("cookie", "").removeprefix("sid=")
entry = {"url": url, "cookie": sid, "user": headers.get("x-notion-active-user-header", ""), "space": headers.get("x-notion-space-id", "")}
status, body = 200, {}
if url.endswith("/getSpaces"):
    if sid == "A":
        open(` + pyQuote(started) + `, "w").close()
        for _ in range(200):
            if os.path.exists(` + pyQuote(release) + `):
                break
            time.sleep(0.05)
    space = {"A": "aaaaaaaa-0000-0000-0000-000000000000", "B": "bbbbbbbb-0000-0000-0000-000000000000"}[sid]
    user = "user-" + sid
    body = {user: {"notion_user": {user: {"value": {"value": {"name": "Name " + sid}}}}, "user_settings": {user: {"value": {"value": {"settings": {"time_zone": "Zone/" + sid}}}}}, "space": {space: {}}}}
elif url.endswith("/getInferenceTranscriptsForUser"):
    body = {"transcripts": []}
elif url.endswith("/getAvailableModels"):
    body = {"models": [{"model": "almond-croissant-low", "modelMessage": "Default"}]}
elif url.endswith("/runInferenceTranscript"):
    payload = json.loads(req["body"])
    entry["context"] = next(item["value"] for item in payload["transcript"] if item["type"] == "context")
    status = 500
with open(` + pyQuote(log) + `, "a") as out:
    out.write(json.dumps(entry) + "\n")
sys.stdout.write(json.dumps({"status": status, "body": json.dumps(body), "error": ""}))
`
	sidecarPath := filepath.Join(dir, "sidecar.py")
	if err := os.WriteFile(sidecarPath, []byte(sidecar), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AGENTHAIL_SIDECAR", sidecarPath)
	t.Setenv("AGENTHAIL_NOTION_TZ", "")

	a := signin.Source{ProfileID: "sp_aaaaaaaaaaaa", Browser: signin.Chrome, Dir: filepath.Join(dir, "A")}
	b := signin.Source{ProfileID: "sp_bbbbbbbbbbbb", Browser: signin.Chrome, Dir: filepath.Join(dir, "B")}
	resolver := &lockedSignin{source: a}
	notion := NewNotion(resolver, "", "")
	notion.cookieBridge = filepath.Join(dir, "cookie.mjs")

	listed := make(chan error, 1)
	go func() {
		_, err := notion.List(context.Background())
		listed <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("account A detection never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	resolver.use(b)
	if _, err := notion.Send(context.Background(), &surface.Session{ID: "new"}, "for B"); !surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("send as B: %v", err)
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-listed; err != nil {
		t.Fatalf("list as A: %v", err)
	}
	resolver.use(a)
	if _, err := notion.Send(context.Background(), &surface.Session{ID: "new"}, "for A"); !surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("send as A: %v", err)
	}

	sends := map[string]int{}
	for _, line := range readLines(t, log) {
		var request notionAccountRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatal(err)
		}
		space := map[string]string{"A": "aaaaaaaa-0000-0000-0000-000000000000", "B": "bbbbbbbb-0000-0000-0000-000000000000"}[request.Cookie]
		if strings.HasSuffix(request.URL, "/getSpaces") {
			continue
		}
		if request.User != "user-"+request.Cookie || request.Space != space {
			t.Fatalf("request mixed accounts: %s", line)
		}
		if strings.HasSuffix(request.URL, "/runInferenceTranscript") {
			sends[request.Cookie]++
			if request.Context.UserID != request.User || request.Context.SpaceID != space || request.Context.Timezone != "Zone/"+request.Cookie {
				t.Fatalf("send context mixed accounts: %s", line)
			}
		}
	}
	if sends["A"] != 1 || sends["B"] != 1 {
		t.Fatalf("sends=%v", sends)
	}
}
